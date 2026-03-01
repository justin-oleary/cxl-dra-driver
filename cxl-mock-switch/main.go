package main

import (
	"encoding/json"
	"log"
	"net/http"
	"sync"
)

type Pool struct {
	mu          sync.Mutex
	TotalGB     int            `json:"total_gb"`
	AllocatedGB int            `json:"allocated_gb"`
	Allocations map[string]int `json:"allocations"`
}

var pool = &Pool{
	TotalGB:     1024,
	AllocatedGB: 0,
	Allocations: make(map[string]int),
}

type Request struct {
	NodeName string `json:"node_name"`
	SizeGB   int    `json:"size_gb"`
}

func main() {
	http.HandleFunc("/status", statusHandler)
	http.HandleFunc("/allocate", allocateHandler)
	http.HandleFunc("/release", releaseHandler)

	log.Println("Mock CXL Switch listening on :8080")
	log.Fatal(http.ListenAndServe(":8080", nil))
}

func statusHandler(w http.ResponseWriter, r *http.Request) {
	pool.mu.Lock()
	defer pool.mu.Unlock()

	json.NewEncoder(w).Encode(map[string]interface{}{
		"total_gb":     pool.TotalGB,
		"allocated_gb": pool.AllocatedGB,
		"available_gb": pool.TotalGB - pool.AllocatedGB,
		"allocations":  pool.Allocations,
	})
}

func allocateHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req Request
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}

	pool.mu.Lock()
	defer pool.mu.Unlock()

	available := pool.TotalGB - pool.AllocatedGB
	if req.SizeGB > available {
		http.Error(w, "insufficient memory", http.StatusInsufficientStorage)
		return
	}

	pool.Allocations[req.NodeName] += req.SizeGB
	pool.AllocatedGB += req.SizeGB

	log.Printf("ALLOCATED %dGB to node %s (total allocated: %dGB)",
		req.SizeGB, req.NodeName, pool.AllocatedGB)

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"status": "allocated"})
}

func releaseHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req Request
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}

	pool.mu.Lock()
	defer pool.mu.Unlock()

	current := pool.Allocations[req.NodeName]
	release := req.SizeGB
	if release > current {
		release = current
	}

	pool.Allocations[req.NodeName] -= release
	pool.AllocatedGB -= release
	if pool.Allocations[req.NodeName] <= 0 {
		delete(pool.Allocations, req.NodeName)
	}

	log.Printf("RELEASED %dGB from node %s (total allocated: %dGB)",
		release, req.NodeName, pool.AllocatedGB)

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"status": "released"})
}
