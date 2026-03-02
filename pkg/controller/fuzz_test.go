package controller

import (
	"encoding/json"
	"testing"

	resourcev1 "k8s.io/api/resource/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// FuzzGetAllocationMeta tests the JSON parsing in getAllocationMeta
// against malformed, malicious, and edge-case inputs.
func FuzzGetAllocationMeta(f *testing.F) {
	// seed corpus with valid and edge-case inputs
	f.Add(`{"node":"worker-1","sizeGB":64}`)
	f.Add(`{"node":"","sizeGB":0}`)
	f.Add(`{}`)
	f.Add(`{"node":"a"}`)
	f.Add(`{"sizeGB":128}`)
	f.Add(`null`)
	f.Add(`""`)
	f.Add(`[]`)
	f.Add(`{"node":null,"sizeGB":null}`)
	f.Add(`{"node":"x","sizeGB":-1}`)
	f.Add(`{"node":"x","sizeGB":9999999999999}`)
	f.Add(`{"extra":"field","node":"y","sizeGB":32}`)
	f.Add(`{`)
	f.Add(`}`)
	f.Add(`{"node":"` + string(make([]byte, 10000)) + `","sizeGB":1}`)

	f.Fuzz(func(t *testing.T, data string) {
		claim := &resourcev1.ResourceClaim{
			ObjectMeta: metav1.ObjectMeta{
				Annotations: map[string]string{
					AnnotationAllocated: data,
				},
			},
		}

		// should never panic regardless of input
		meta, ok := getAllocationMeta(claim)

		// if parsing succeeded, verify invariants
		if ok {
			// re-marshal should produce valid JSON
			_, err := json.Marshal(meta)
			if err != nil {
				t.Errorf("valid parse produced unmarshalable result: %v", err)
			}
		}
	})
}

// FuzzAllocationMetaMarshal tests roundtrip marshal/unmarshal
func FuzzAllocationMetaMarshal(f *testing.F) {
	f.Add("node-1", 64)
	f.Add("", 0)
	f.Add("very-long-node-name-that-exceeds-normal-limits", 999999)
	f.Add("node/with/slashes", -1)
	f.Add("node with spaces", 1)

	f.Fuzz(func(t *testing.T, node string, sizeGB int) {
		original := AllocationMeta{Node: node, SizeGB: sizeGB}

		// marshal should never panic
		data, err := json.Marshal(original)
		if err != nil {
			t.Fatalf("marshal failed: %v", err)
		}

		// unmarshal should never panic
		var recovered AllocationMeta
		if err := json.Unmarshal(data, &recovered); err != nil {
			t.Fatalf("unmarshal failed: %v", err)
		}

		// Note: JSON encoding may replace invalid UTF-8 with replacement characters.
		// This is expected behavior. We only verify sizeGB which is always exact.
		if recovered.SizeGB != original.SizeGB {
			t.Errorf("sizeGB mismatch: got %d, want %d", recovered.SizeGB, original.SizeGB)
		}
	})
}
