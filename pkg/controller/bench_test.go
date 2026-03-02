package controller

import (
	"context"
	"testing"

	resourcev1 "k8s.io/api/resource/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

func BenchmarkGetAllocationMeta(b *testing.B) {
	claim := &resourcev1.ResourceClaim{
		ObjectMeta: metav1.ObjectMeta{
			Annotations: map[string]string{
				AnnotationAllocated: `{"node":"worker-1","sizeGB":64}`,
			},
		},
	}

	b.ResetTimer()
	b.ReportAllocs()
	for b.Loop() {
		getAllocationMeta(claim)
	}
}

func BenchmarkContainsFinalizer(b *testing.B) {
	claim := &resourcev1.ResourceClaim{
		ObjectMeta: metav1.ObjectMeta{
			Finalizers: []string{"other-finalizer", FinalizerName, "another-finalizer"},
		},
	}

	b.ResetTimer()
	b.ReportAllocs()
	for b.Loop() {
		containsFinalizer(claim, FinalizerName)
	}
}

func BenchmarkExtractAllocationDetails(b *testing.B) {
	claim := &resourcev1.ResourceClaim{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "default",
			Name:      "test-claim",
		},
		Status: resourcev1.ResourceClaimStatus{
			Allocation: &resourcev1.AllocationResult{
				Devices: resourcev1.DeviceAllocationResult{
					Results: []resourcev1.DeviceRequestAllocationResult{
						{Driver: DriverName, Pool: "worker-1"},
					},
				},
			},
		},
	}

	b.ResetTimer()
	b.ReportAllocs()
	for b.Loop() {
		extractAllocationDetails(claim)
	}
}

func BenchmarkIsOurClaim(b *testing.B) {
	claim := &resourcev1.ResourceClaim{
		ObjectMeta: metav1.ObjectMeta{
			Namespace:  "default",
			Name:       "test-claim",
			Finalizers: []string{FinalizerName},
		},
		Status: resourcev1.ResourceClaimStatus{
			Allocation: &resourcev1.AllocationResult{
				Devices: resourcev1.DeviceAllocationResult{
					Results: []resourcev1.DeviceRequestAllocationResult{
						{Driver: DriverName, Pool: "worker-1"},
					},
				},
			},
		},
	}

	clientset := newTestClientset(claim)
	ctrl := newController(clientset, &mockCXLClient{})

	b.ResetTimer()
	b.ReportAllocs()
	for b.Loop() {
		ctrl.isOurClaim(claim)
	}
}

func BenchmarkHandleAllocation_AlreadyAllocated(b *testing.B) {
	claim := &resourcev1.ResourceClaim{
		ObjectMeta: metav1.ObjectMeta{
			Namespace:  "default",
			Name:       "test-claim",
			UID:        types.UID("test-uid"),
			Finalizers: []string{FinalizerName},
			Annotations: map[string]string{
				AnnotationAllocated: `{"node":"worker-1","sizeGB":64}`,
			},
		},
		Status: resourcev1.ResourceClaimStatus{
			Allocation: &resourcev1.AllocationResult{
				Devices: resourcev1.DeviceAllocationResult{
					Results: []resourcev1.DeviceRequestAllocationResult{
						{Driver: DriverName, Pool: "worker-1"},
					},
				},
			},
			ReservedFor: []resourcev1.ResourceClaimConsumerReference{
				{Name: "test-pod"},
			},
		},
	}

	clientset := newTestClientset(claim)
	ctrl := newController(clientset, &mockCXLClient{})
	ctx := context.Background()

	b.ResetTimer()
	b.ReportAllocs()
	for b.Loop() {
		_ = ctrl.handleAllocation(ctx, claim)
	}
}
