package cxlclient

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestClient_Allocate(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
		wantErr    error
	}{
		{
			name:       "success",
			statusCode: http.StatusOK,
			wantErr:    nil,
		},
		{
			name:       "insufficient memory",
			statusCode: http.StatusInsufficientStorage,
			wantErr:    ErrInsufficientMemory,
		},
		{
			name:       "invalid request",
			statusCode: http.StatusBadRequest,
			wantErr:    ErrInvalidRequest,
		},
		{
			name:       "server error",
			statusCode: http.StatusInternalServerError,
			wantErr:    nil, // non-sentinel error
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost {
					t.Errorf("expected POST, got %s", r.Method)
				}
				if r.URL.Path != "/allocate" {
					t.Errorf("expected /allocate, got %s", r.URL.Path)
				}
				if ct := r.Header.Get("Content-Type"); ct != "application/json" {
					t.Errorf("expected Content-Type application/json, got %s", ct)
				}

				var req request
				if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
					t.Errorf("failed to decode request: %v", err)
				}
				if req.NodeName != "node-1" {
					t.Errorf("expected node-1, got %s", req.NodeName)
				}
				if req.SizeGB != 64 {
					t.Errorf("expected 64, got %d", req.SizeGB)
				}

				w.WriteHeader(tt.statusCode)
			}))
			defer server.Close()

			client := New(server.URL)
			err := client.Allocate(context.Background(), "node-1", 64)

			if tt.wantErr != nil {
				if err != tt.wantErr {
					t.Errorf("expected error %v, got %v", tt.wantErr, err)
				}
			} else if tt.statusCode == http.StatusOK && err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
}

func TestClient_Release(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/release" {
			t.Errorf("expected /release, got %s", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	client := New(server.URL)
	err := client.Release(context.Background(), "node-1", 64)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestClient_ContextCancellation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(100 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	client := New(server.URL)
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	err := client.Allocate(ctx, "node-1", 64)
	if err == nil {
		t.Error("expected error from cancelled context")
	}
}

func TestClient_SwitchUnavailable(t *testing.T) {
	client := New("http://localhost:59999") // nothing listening
	err := client.Allocate(context.Background(), "node-1", 64)
	if err == nil {
		t.Error("expected error for unavailable switch")
	}
	// should wrap ErrSwitchUnavailable
	if !containsError(err, ErrSwitchUnavailable) {
		t.Errorf("expected ErrSwitchUnavailable, got %v", err)
	}
}

func TestClient_ConnectionReuse(t *testing.T) {
	var requestCount atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	client := New(server.URL)
	for i := 0; i < 10; i++ {
		if err := client.Allocate(context.Background(), "node-1", 64); err != nil {
			t.Fatalf("request %d failed: %v", i, err)
		}
	}

	if requestCount.Load() != 10 {
		t.Errorf("expected 10 requests, got %d", requestCount.Load())
	}
}

func containsError(err, target error) bool {
	if err == nil {
		return false
	}
	return err.Error() == target.Error() || (len(err.Error()) > len(target.Error()) && err.Error()[:len(target.Error())] == target.Error()[:len(target.Error())])
}
