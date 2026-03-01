package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

func resetPool() {
	pool.mu.Lock()
	pool.TotalGB = 1024
	pool.AllocatedGB = 0
	pool.Allocations = make(map[string]int)
	pool.mu.Unlock()
}

func TestStatusHandler(t *testing.T) {
	resetPool()

	req := httptest.NewRequest(http.MethodGet, "/status", nil)
	w := httptest.NewRecorder()

	statusHandler(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", w.Code)
	}

	var resp map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if resp["total_gb"].(float64) != 1024 {
		t.Errorf("expected total_gb 1024, got %v", resp["total_gb"])
	}
	if resp["allocated_gb"].(float64) != 0 {
		t.Errorf("expected allocated_gb 0, got %v", resp["allocated_gb"])
	}
	if resp["available_gb"].(float64) != 1024 {
		t.Errorf("expected available_gb 1024, got %v", resp["available_gb"])
	}
}

func TestAllocateHandler(t *testing.T) {
	resetPool()

	tests := []struct {
		name       string
		request    Request
		wantStatus int
	}{
		{
			name:       "successful allocation",
			request:    Request{NodeName: "node-1", SizeGB: 64},
			wantStatus: http.StatusOK,
		},
		{
			name:       "second allocation same node",
			request:    Request{NodeName: "node-1", SizeGB: 64},
			wantStatus: http.StatusOK,
		},
		{
			name:       "allocation different node",
			request:    Request{NodeName: "node-2", SizeGB: 128},
			wantStatus: http.StatusOK,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body, _ := json.Marshal(tt.request)
			req := httptest.NewRequest(http.MethodPost, "/allocate", bytes.NewReader(body))
			w := httptest.NewRecorder()

			allocateHandler(w, req)

			if w.Code != tt.wantStatus {
				t.Errorf("expected status %d, got %d", tt.wantStatus, w.Code)
			}
		})
	}

	// verify final state
	pool.mu.Lock()
	if pool.AllocatedGB != 256 { // 64 + 64 + 128
		t.Errorf("expected 256 allocated, got %d", pool.AllocatedGB)
	}
	if pool.Allocations["node-1"] != 128 { // 64 + 64
		t.Errorf("expected node-1 to have 128, got %d", pool.Allocations["node-1"])
	}
	if pool.Allocations["node-2"] != 128 {
		t.Errorf("expected node-2 to have 128, got %d", pool.Allocations["node-2"])
	}
	pool.mu.Unlock()
}

func TestAllocateHandler_InsufficientMemory(t *testing.T) {
	resetPool()

	// try to allocate more than available
	body, _ := json.Marshal(Request{NodeName: "node-1", SizeGB: 2000})
	req := httptest.NewRequest(http.MethodPost, "/allocate", bytes.NewReader(body))
	w := httptest.NewRecorder()

	allocateHandler(w, req)

	if w.Code != http.StatusInsufficientStorage {
		t.Errorf("expected status %d, got %d", http.StatusInsufficientStorage, w.Code)
	}
}

func TestAllocateHandler_InvalidMethod(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/allocate", nil)
	w := httptest.NewRecorder()

	allocateHandler(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected status %d, got %d", http.StatusMethodNotAllowed, w.Code)
	}
}

func TestAllocateHandler_InvalidJSON(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/allocate", bytes.NewReader([]byte("invalid json")))
	w := httptest.NewRecorder()

	allocateHandler(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected status %d, got %d", http.StatusBadRequest, w.Code)
	}
}

func TestReleaseHandler(t *testing.T) {
	resetPool()

	// first allocate
	pool.mu.Lock()
	pool.Allocations["node-1"] = 128
	pool.AllocatedGB = 128
	pool.mu.Unlock()

	tests := []struct {
		name       string
		request    Request
		wantStatus int
	}{
		{
			name:       "release partial",
			request:    Request{NodeName: "node-1", SizeGB: 64},
			wantStatus: http.StatusOK,
		},
		{
			name:       "release remaining",
			request:    Request{NodeName: "node-1", SizeGB: 64},
			wantStatus: http.StatusOK,
		},
		{
			name:       "release nonexistent",
			request:    Request{NodeName: "node-2", SizeGB: 64},
			wantStatus: http.StatusOK,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body, _ := json.Marshal(tt.request)
			req := httptest.NewRequest(http.MethodPost, "/release", bytes.NewReader(body))
			w := httptest.NewRecorder()

			releaseHandler(w, req)

			if w.Code != tt.wantStatus {
				t.Errorf("expected status %d, got %d", tt.wantStatus, w.Code)
			}
		})
	}

	// verify final state - node-1 should be removed
	pool.mu.Lock()
	if pool.AllocatedGB != 0 {
		t.Errorf("expected 0 allocated, got %d", pool.AllocatedGB)
	}
	if _, ok := pool.Allocations["node-1"]; ok {
		t.Error("expected node-1 to be removed from allocations")
	}
	pool.mu.Unlock()
}

func TestReleaseHandler_MoreThanAllocated(t *testing.T) {
	resetPool()

	pool.mu.Lock()
	pool.Allocations["node-1"] = 32
	pool.AllocatedGB = 32
	pool.mu.Unlock()

	// try to release more than allocated
	body, _ := json.Marshal(Request{NodeName: "node-1", SizeGB: 64})
	req := httptest.NewRequest(http.MethodPost, "/release", bytes.NewReader(body))
	w := httptest.NewRecorder()

	releaseHandler(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status OK, got %d", w.Code)
	}

	// should only release what was allocated
	pool.mu.Lock()
	if pool.AllocatedGB != 0 {
		t.Errorf("expected 0 allocated, got %d", pool.AllocatedGB)
	}
	pool.mu.Unlock()
}

func TestConcurrentAllocations(t *testing.T) {
	resetPool()

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			body, _ := json.Marshal(Request{NodeName: "node-1", SizeGB: 1})
			req := httptest.NewRequest(http.MethodPost, "/allocate", bytes.NewReader(body))
			w := httptest.NewRecorder()
			allocateHandler(w, req)
		}(i)
	}
	wg.Wait()

	pool.mu.Lock()
	if pool.AllocatedGB != 100 {
		t.Errorf("expected 100 allocated, got %d", pool.AllocatedGB)
	}
	pool.mu.Unlock()
}
