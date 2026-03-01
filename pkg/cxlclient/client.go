// Package cxlclient provides an HTTP client for the CXL memory switch.
package cxlclient

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"
)

var (
	ErrInsufficientMemory = errors.New("cxl: insufficient memory in pool")
	ErrInvalidRequest     = errors.New("cxl: invalid request")
	ErrSwitchUnavailable  = errors.New("cxl: switch unavailable")
)

type request struct {
	NodeName string `json:"node_name"`
	SizeGB   int    `json:"size_gb"`
}

// CXLClient defines the interface for CXL switch operations.
type CXLClient interface {
	Allocate(ctx context.Context, nodeName string, sizeGB int) error
	Release(ctx context.Context, nodeName string, sizeGB int) error
}

// Client implements CXLClient via HTTP.
type Client struct {
	endpoint   string
	httpClient *http.Client
}

var _ CXLClient = (*Client)(nil)

func New(endpoint string) *Client {
	return &Client{
		endpoint: endpoint,
		httpClient: &http.Client{
			Timeout: 5 * time.Second,
		},
	}
}

func (c *Client) Allocate(ctx context.Context, nodeName string, sizeGB int) error {
	return c.do(ctx, "/allocate", nodeName, sizeGB)
}

func (c *Client) Release(ctx context.Context, nodeName string, sizeGB int) error {
	return c.do(ctx, "/release", nodeName, sizeGB)
}

func (c *Client) do(ctx context.Context, path, nodeName string, sizeGB int) error {
	body, err := json.Marshal(request{NodeName: nodeName, SizeGB: sizeGB})
	if err != nil {
		return fmt.Errorf("cxl: marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint+path, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("cxl: create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrSwitchUnavailable, err)
	}
	defer func() {
		// Drain body to enable connection reuse
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
	}()

	switch resp.StatusCode {
	case http.StatusOK:
		return nil
	case http.StatusBadRequest:
		return ErrInvalidRequest
	case http.StatusInsufficientStorage:
		return ErrInsufficientMemory
	default:
		return fmt.Errorf("cxl: unexpected status %d", resp.StatusCode)
	}
}
