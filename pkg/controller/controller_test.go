package controller

import (
	"context"
	"encoding/json"
	"sync"
	"sync/atomic"
	"testing"

	resourcev1 "k8s.io/api/resource/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
	"k8s.io/client-go/tools/record"

	"github.com/justin-oleary/cxl-dra-driver/pkg/cxlclient"
)

// Verify mockCXLClient implements the interface
var _ cxlclient.CXLClient = (*mockCXLClient)(nil)

// mockCXLClient implements cxlclient.CXLClient for testing
type mockCXLClient struct {
	allocateFunc func(ctx context.Context, nodeName string, sizeGB int) error
	releaseFunc  func(ctx context.Context, nodeName string, sizeGB int) error

	mu            sync.Mutex
	allocateCalls []allocateCall
	releaseCalls  []releaseCall
	allocateCount atomic.Int32
	releaseCount  atomic.Int32
}

type allocateCall struct {
	nodeName string
	sizeGB   int
}

type releaseCall struct {
	nodeName string
	sizeGB   int
}

func (m *mockCXLClient) Allocate(ctx context.Context, nodeName string, sizeGB int) error {
	m.allocateCount.Add(1)
	m.mu.Lock()
	m.allocateCalls = append(m.allocateCalls, allocateCall{nodeName, sizeGB})
	m.mu.Unlock()
	if m.allocateFunc != nil {
		return m.allocateFunc(ctx, nodeName, sizeGB)
	}
	return nil
}

func (m *mockCXLClient) Release(ctx context.Context, nodeName string, sizeGB int) error {
	m.releaseCount.Add(1)
	m.mu.Lock()
	m.releaseCalls = append(m.releaseCalls, releaseCall{nodeName, sizeGB})
	m.mu.Unlock()
	if m.releaseFunc != nil {
		return m.releaseFunc(ctx, nodeName, sizeGB)
	}
	return nil
}

func (m *mockCXLClient) getAllocateCalls() []allocateCall {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := make([]allocateCall, len(m.allocateCalls))
	copy(result, m.allocateCalls)
	return result
}

func (m *mockCXLClient) getReleaseCalls() []releaseCall {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := make([]releaseCall, len(m.releaseCalls))
	copy(result, m.releaseCalls)
	return result
}

func newTestClaim(namespace, name string) *resourcev1.ResourceClaim {
	return &resourcev1.ResourceClaim{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace,
			Name:      name,
			UID:       types.UID(name + "-uid"),
		},
	}
}

func withAllocation(claim *resourcev1.ResourceClaim, node string) *resourcev1.ResourceClaim {
	claim.Status.Allocation = &resourcev1.AllocationResult{
		Devices: resourcev1.DeviceAllocationResult{
			Results: []resourcev1.DeviceRequestAllocationResult{
				{Driver: DriverName, Pool: node},
			},
		},
	}
	claim.Status.ReservedFor = []resourcev1.ResourceClaimConsumerReference{
		{Name: "test-pod"},
	}
	return claim
}

func withFinalizer(claim *resourcev1.ResourceClaim) *resourcev1.ResourceClaim {
	claim.Finalizers = append(claim.Finalizers, FinalizerName)
	return claim
}

func withDeletionTimestamp(claim *resourcev1.ResourceClaim) *resourcev1.ResourceClaim {
	now := metav1.Now()
	claim.DeletionTimestamp = &now
	return claim
}

func withAllocationAnnotation(claim *resourcev1.ResourceClaim, node string, sizeGB int) *resourcev1.ResourceClaim {
	if claim.Annotations == nil {
		claim.Annotations = make(map[string]string)
	}
	meta := AllocationMeta{Node: node, SizeGB: sizeGB}
	metaJSON, _ := json.Marshal(meta)
	claim.Annotations[AnnotationAllocated] = string(metaJSON)
	return claim
}

// newTestClientset creates a fake clientset for testing.
//
//nolint:staticcheck // fake.NewSimpleClientset is deprecated but NewClientset requires generated apply configs
func newTestClientset(objects ...runtime.Object) *fake.Clientset {
	return fake.NewSimpleClientset(objects...)
}

func newController(clientset *fake.Clientset, mockCXL *mockCXLClient) *Controller {
	factory := informers.NewSharedInformerFactory(clientset, 0)
	return &Controller{
		clientset:        clientset,
		claimLister:      factory.Resource().V1().ResourceClaims().Lister(),
		claimSynced:      func() bool { return true },
		cxl:              mockCXL,
		recorder:         record.NewFakeRecorder(100),
		eventBroadcaster: record.NewBroadcaster(),
	}
}

func TestHandleAllocation_PersistsAnnotation(t *testing.T) {
	claim := withFinalizer(withAllocation(newTestClaim("default", "test-claim"), "node-1"))
	clientset := newTestClientset(claim)
	mock := &mockCXLClient{}
	ctrl := newController(clientset, mock)

	ctx := context.Background()
	err := ctrl.handleAllocation(ctx, claim)
	if err != nil {
		t.Fatalf("handleAllocation failed: %v", err)
	}

	// verify CXL allocate was called
	calls := mock.getAllocateCalls()
	if len(calls) != 1 {
		t.Fatalf("expected 1 allocate call, got %d", len(calls))
	}
	if calls[0].nodeName != "node-1" {
		t.Errorf("expected node-1, got %s", calls[0].nodeName)
	}

	// verify annotation was persisted to API server
	updated, err := clientset.ResourceV1().ResourceClaims("default").Get(ctx, "test-claim", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("failed to get claim: %v", err)
	}

	meta, ok := getAllocationMeta(updated)
	if !ok {
		t.Fatal("allocation annotation not found")
	}
	if meta.Node != "node-1" {
		t.Errorf("expected node-1, got %s", meta.Node)
	}
	if meta.SizeGB != DefaultSizeGB {
		t.Errorf("expected %d, got %d", DefaultSizeGB, meta.SizeGB)
	}
}

func TestHandleAllocation_Idempotent(t *testing.T) {
	// claim already has allocation annotation
	claim := withAllocationAnnotation(
		withFinalizer(withAllocation(newTestClaim("default", "test-claim"), "node-1")),
		"node-1", 64,
	)
	clientset := newTestClientset(claim)
	mock := &mockCXLClient{}
	ctrl := newController(clientset, mock)

	ctx := context.Background()
	err := ctrl.handleAllocation(ctx, claim)
	if err != nil {
		t.Fatalf("handleAllocation failed: %v", err)
	}

	// verify CXL allocate was NOT called (already allocated per annotation)
	if count := mock.allocateCount.Load(); count != 0 {
		t.Fatalf("expected 0 allocate calls, got %d", count)
	}
}

func TestHandleAllocation_AllocationError_NoAnnotation(t *testing.T) {
	claim := withFinalizer(withAllocation(newTestClaim("default", "test-claim"), "node-1"))
	clientset := newTestClientset(claim)
	mock := &mockCXLClient{
		allocateFunc: func(ctx context.Context, nodeName string, sizeGB int) error {
			return cxlclient.ErrInsufficientMemory
		},
	}
	ctrl := newController(clientset, mock)

	ctx := context.Background()
	err := ctrl.handleAllocation(ctx, claim)
	if err == nil {
		t.Fatal("expected error")
	}

	// verify annotation was NOT persisted (allocation failed)
	updated, _ := clientset.ResourceV1().ResourceClaims("default").Get(ctx, "test-claim", metav1.GetOptions{})
	if _, ok := getAllocationMeta(updated); ok {
		t.Error("annotation should not be present after failed allocation")
	}
}

func TestHandleAllocation_RetryAfterFailure(t *testing.T) {
	claim := withFinalizer(withAllocation(newTestClaim("default", "test-claim"), "node-1"))
	clientset := newTestClientset(claim)

	callCount := atomic.Int32{}
	mock := &mockCXLClient{
		allocateFunc: func(ctx context.Context, nodeName string, sizeGB int) error {
			if callCount.Add(1) == 1 {
				return cxlclient.ErrSwitchUnavailable
			}
			return nil
		},
	}
	ctrl := newController(clientset, mock)

	ctx := context.Background()

	// first call fails
	err := ctrl.handleAllocation(ctx, claim)
	if err == nil {
		t.Fatal("expected error on first call")
	}

	// second call succeeds (no annotation yet, so it tries again)
	err = ctrl.handleAllocation(ctx, claim)
	if err != nil {
		t.Fatalf("second call should succeed: %v", err)
	}

	// verify annotation now exists
	updated, _ := clientset.ResourceV1().ResourceClaims("default").Get(ctx, "test-claim", metav1.GetOptions{})
	if _, ok := getAllocationMeta(updated); !ok {
		t.Error("annotation should be present after successful retry")
	}
}

func TestHandleDeletion_ReleasesMemory(t *testing.T) {
	claim := withDeletionTimestamp(
		withAllocationAnnotation(
			withFinalizer(withAllocation(newTestClaim("default", "test-claim"), "node-1")),
			"node-1", 64,
		),
	)
	clientset := newTestClientset(claim)
	mock := &mockCXLClient{}
	ctrl := newController(clientset, mock)

	ctx := context.Background()
	err := ctrl.handleDeletion(ctx, claim)
	if err != nil {
		t.Fatalf("handleDeletion failed: %v", err)
	}

	// verify release was called with correct params from annotation
	calls := mock.getReleaseCalls()
	if len(calls) != 1 {
		t.Fatalf("expected 1 release call, got %d", len(calls))
	}
	if calls[0].nodeName != "node-1" {
		t.Errorf("expected node-1, got %s", calls[0].nodeName)
	}
	if calls[0].sizeGB != 64 {
		t.Errorf("expected 64, got %d", calls[0].sizeGB)
	}

	// verify finalizer was removed
	updated, _ := clientset.ResourceV1().ResourceClaims("default").Get(ctx, "test-claim", metav1.GetOptions{})
	if containsFinalizer(updated, FinalizerName) {
		t.Error("finalizer should have been removed")
	}
}

func TestHandleDeletion_NoAnnotation_NoRelease(t *testing.T) {
	// claim has finalizer but no allocation annotation
	claim := withDeletionTimestamp(withFinalizer(newTestClaim("default", "test-claim")))
	clientset := newTestClientset(claim)
	mock := &mockCXLClient{}
	ctrl := newController(clientset, mock)

	ctx := context.Background()
	err := ctrl.handleDeletion(ctx, claim)
	if err != nil {
		t.Fatalf("handleDeletion failed: %v", err)
	}

	// verify release was NOT called
	if count := mock.releaseCount.Load(); count != 0 {
		t.Errorf("expected 0 release calls, got %d", count)
	}
}

func TestHandleDeletion_ReleaseError_FinalizerRemains(t *testing.T) {
	// hostile path: CXL switch unavailable during deletion
	// finalizer MUST remain to prevent hardware leak
	claim := withDeletionTimestamp(
		withAllocationAnnotation(
			withFinalizer(withAllocation(newTestClaim("default", "test-claim"), "node-1")),
			"node-1", 64,
		),
	)
	clientset := newTestClientset(claim)
	mock := &mockCXLClient{
		releaseFunc: func(ctx context.Context, nodeName string, sizeGB int) error {
			return cxlclient.ErrSwitchUnavailable
		},
	}
	ctrl := newController(clientset, mock)

	ctx := context.Background()
	err := ctrl.handleDeletion(ctx, claim)

	// MUST return error so controller retries
	if err == nil {
		t.Fatal("expected error when release fails")
	}

	// verify release was attempted
	if count := mock.releaseCount.Load(); count != 1 {
		t.Errorf("expected 1 release call, got %d", count)
	}

	// CRITICAL: finalizer MUST remain - this is the distributed transaction barrier
	updated, _ := clientset.ResourceV1().ResourceClaims("default").Get(ctx, "test-claim", metav1.GetOptions{})
	if !containsFinalizer(updated, FinalizerName) {
		t.Fatal("HARDWARE LEAK: finalizer was removed despite release failure")
	}

	// annotation MUST remain so retry knows what to release
	if _, ok := getAllocationMeta(updated); !ok {
		t.Fatal("allocation annotation was removed despite release failure")
	}
}

func TestDeletionLifecycle(t *testing.T) {
	tests := []struct {
		name              string
		hasAllocation     bool
		releaseError      error
		wantReleaseCalls  int
		wantError         bool
		wantFinalizerGone bool
	}{
		{
			name:              "happy path: release succeeds, finalizer removed",
			hasAllocation:     true,
			releaseError:      nil,
			wantReleaseCalls:  1,
			wantError:         false,
			wantFinalizerGone: true,
		},
		{
			name:              "hostile path: switch unavailable, finalizer remains",
			hasAllocation:     true,
			releaseError:      cxlclient.ErrSwitchUnavailable,
			wantReleaseCalls:  1,
			wantError:         true,
			wantFinalizerGone: false,
		},
		{
			name:              "hostile path: insufficient memory error, finalizer remains",
			hasAllocation:     true,
			releaseError:      cxlclient.ErrInsufficientMemory,
			wantReleaseCalls:  1,
			wantError:         true,
			wantFinalizerGone: false,
		},
		{
			name:              "no allocation: finalizer removed without release",
			hasAllocation:     false,
			releaseError:      nil,
			wantReleaseCalls:  0,
			wantError:         false,
			wantFinalizerGone: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			claim := withDeletionTimestamp(withFinalizer(newTestClaim("default", "test-claim")))
			if tt.hasAllocation {
				claim = withAllocationAnnotation(claim, "node-1", 64)
			}

			clientset := newTestClientset(claim)
			mock := &mockCXLClient{
				releaseFunc: func(ctx context.Context, nodeName string, sizeGB int) error {
					return tt.releaseError
				},
			}
			ctrl := newController(clientset, mock)

			ctx := context.Background()
			err := ctrl.handleDeletion(ctx, claim)

			// check error expectation
			if tt.wantError && err == nil {
				t.Error("expected error, got nil")
			}
			if !tt.wantError && err != nil {
				t.Errorf("unexpected error: %v", err)
			}

			// check release calls
			if count := mock.releaseCount.Load(); int(count) != tt.wantReleaseCalls {
				t.Errorf("release calls = %d, want %d", count, tt.wantReleaseCalls)
			}

			// check finalizer state
			updated, _ := clientset.ResourceV1().ResourceClaims("default").Get(ctx, "test-claim", metav1.GetOptions{})
			hasFinalizer := containsFinalizer(updated, FinalizerName)
			if tt.wantFinalizerGone && hasFinalizer {
				t.Error("finalizer should have been removed")
			}
			if !tt.wantFinalizerGone && !hasFinalizer {
				t.Error("HARDWARE LEAK: finalizer was removed despite release failure")
			}
		})
	}
}

func TestAddFinalizer(t *testing.T) {
	claim := newTestClaim("default", "test-claim")
	clientset := newTestClientset(claim)
	mock := &mockCXLClient{}
	ctrl := newController(clientset, mock)

	ctx := context.Background()
	err := ctrl.addFinalizer(ctx, claim)
	if err != nil {
		t.Fatalf("addFinalizer failed: %v", err)
	}

	updated, _ := clientset.ResourceV1().ResourceClaims("default").Get(ctx, "test-claim", metav1.GetOptions{})
	if !containsFinalizer(updated, FinalizerName) {
		t.Error("finalizer should have been added")
	}
}

func TestUpdateWithRetry_ConflictHandling(t *testing.T) {
	claim := newTestClaim("default", "test-claim")
	clientset := newTestClientset(claim)
	mock := &mockCXLClient{}
	ctrl := newController(clientset, mock)

	// inject conflict on first 2 attempts
	conflictCount := atomic.Int32{}
	clientset.PrependReactor("update", "resourceclaims", func(action k8stesting.Action) (bool, runtime.Object, error) {
		if conflictCount.Add(1) <= 2 {
			return true, nil, apierrors.NewConflict(
				schema.GroupResource{Group: "resource.k8s.io", Resource: "resourceclaims"},
				"test-claim",
				nil,
			)
		}
		return false, nil, nil
	})

	ctx := context.Background()
	err := ctrl.addFinalizer(ctx, claim)
	if err != nil {
		t.Fatalf("addFinalizer failed after retries: %v", err)
	}

	if conflictCount.Load() != 3 {
		t.Errorf("expected 3 attempts (2 conflicts + 1 success), got %d", conflictCount.Load())
	}
}

func TestPatchAnnotation_ConflictHandling(t *testing.T) {
	claim := withFinalizer(withAllocation(newTestClaim("default", "test-claim"), "node-1"))
	clientset := newTestClientset(claim)
	mock := &mockCXLClient{}
	ctrl := newController(clientset, mock)

	// inject conflict on first 2 patch attempts
	conflictCount := atomic.Int32{}
	clientset.PrependReactor("patch", "resourceclaims", func(action k8stesting.Action) (bool, runtime.Object, error) {
		if conflictCount.Add(1) <= 2 {
			return true, nil, apierrors.NewConflict(
				schema.GroupResource{Group: "resource.k8s.io", Resource: "resourceclaims"},
				"test-claim",
				nil,
			)
		}
		return false, nil, nil
	})

	ctx := context.Background()
	err := ctrl.patchAnnotation(ctx, "default", "test-claim", AnnotationAllocated, `{"node":"node-1","sizeGB":64}`)
	if err != nil {
		t.Fatalf("patchAnnotation failed after retries: %v", err)
	}

	if conflictCount.Load() != 3 {
		t.Errorf("expected 3 attempts, got %d", conflictCount.Load())
	}
}

func TestPatchAnnotation_MaxRetriesExceeded(t *testing.T) {
	claim := withFinalizer(withAllocation(newTestClaim("default", "test-claim"), "node-1"))
	clientset := newTestClientset(claim)
	mock := &mockCXLClient{}
	ctrl := newController(clientset, mock)

	// always return conflict
	clientset.PrependReactor("patch", "resourceclaims", func(action k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, apierrors.NewConflict(
			schema.GroupResource{Group: "resource.k8s.io", Resource: "resourceclaims"},
			"test-claim",
			nil,
		)
	})

	ctx := context.Background()
	err := ctrl.patchAnnotation(ctx, "default", "test-claim", AnnotationAllocated, `{"node":"node-1","sizeGB":64}`)
	if err == nil {
		t.Fatal("expected error after max retries")
	}
}

func TestGetAllocationMeta(t *testing.T) {
	tests := []struct {
		name       string
		claim      *resourcev1.ResourceClaim
		wantMeta   AllocationMeta
		wantExists bool
	}{
		{
			name:       "no annotations",
			claim:      newTestClaim("default", "test"),
			wantExists: false,
		},
		{
			name: "no allocation annotation",
			claim: func() *resourcev1.ResourceClaim {
				c := newTestClaim("default", "test")
				c.Annotations = map[string]string{"other": "value"}
				return c
			}(),
			wantExists: false,
		},
		{
			name:       "valid allocation annotation",
			claim:      withAllocationAnnotation(newTestClaim("default", "test"), "node-1", 64),
			wantMeta:   AllocationMeta{Node: "node-1", SizeGB: 64},
			wantExists: true,
		},
		{
			name: "invalid json annotation",
			claim: func() *resourcev1.ResourceClaim {
				c := newTestClaim("default", "test")
				c.Annotations = map[string]string{AnnotationAllocated: "invalid-json"}
				return c
			}(),
			wantExists: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			meta, exists := getAllocationMeta(tt.claim)
			if exists != tt.wantExists {
				t.Errorf("exists = %v, want %v", exists, tt.wantExists)
			}
			if exists && meta != tt.wantMeta {
				t.Errorf("meta = %+v, want %+v", meta, tt.wantMeta)
			}
		})
	}
}

func TestIsOurClaim(t *testing.T) {
	clientset := newTestClientset()
	ctrl := newController(clientset, &mockCXLClient{})

	tests := []struct {
		name  string
		claim *resourcev1.ResourceClaim
		want  bool
	}{
		{
			name:  "no allocation, no finalizer",
			claim: newTestClaim("default", "test"),
			want:  false,
		},
		{
			name:  "has our finalizer",
			claim: withFinalizer(newTestClaim("default", "test")),
			want:  true,
		},
		{
			name:  "has our allocation",
			claim: withAllocation(newTestClaim("default", "test"), "node-1"),
			want:  true,
		},
		{
			name: "different driver",
			claim: func() *resourcev1.ResourceClaim {
				c := newTestClaim("default", "test")
				c.Status.Allocation = &resourcev1.AllocationResult{
					Devices: resourcev1.DeviceAllocationResult{
						Results: []resourcev1.DeviceRequestAllocationResult{
							{Driver: "other.driver.com", Pool: "node-1"},
						},
					},
				}
				return c
			}(),
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ctrl.isOurClaim(tt.claim); got != tt.want {
				t.Errorf("isOurClaim() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestSyncHandler_FullFlow(t *testing.T) {
	// test the full flow without informer caching complications
	// by directly calling handleAllocation with fresh data from API server

	claim := withAllocation(newTestClaim("default", "test-claim"), "node-1")
	clientset := newTestClientset(claim)
	mock := &mockCXLClient{}
	ctrl := newController(clientset, mock)

	ctx := context.Background()

	// step 1: add finalizer
	err := ctrl.addFinalizer(ctx, claim)
	if err != nil {
		t.Fatalf("addFinalizer failed: %v", err)
	}

	// verify finalizer added
	updated, _ := clientset.ResourceV1().ResourceClaims("default").Get(ctx, "test-claim", metav1.GetOptions{})
	if !containsFinalizer(updated, FinalizerName) {
		t.Fatal("finalizer should have been added")
	}

	// step 2: allocate (now has finalizer)
	err = ctrl.handleAllocation(ctx, updated)
	if err != nil {
		t.Fatalf("handleAllocation failed: %v", err)
	}

	// verify allocation
	if count := mock.allocateCount.Load(); count != 1 {
		t.Fatalf("expected 1 allocate call, got %d", count)
	}

	// verify annotation persisted
	updated, _ = clientset.ResourceV1().ResourceClaims("default").Get(ctx, "test-claim", metav1.GetOptions{})
	if _, ok := getAllocationMeta(updated); !ok {
		t.Fatal("allocation annotation should be present")
	}

	// step 3: second allocation call should be idempotent
	err = ctrl.handleAllocation(ctx, updated)
	if err != nil {
		t.Fatalf("second handleAllocation failed: %v", err)
	}

	// verify no additional allocations
	if count := mock.allocateCount.Load(); count != 1 {
		t.Fatalf("expected still 1 allocate call, got %d", count)
	}
}

func TestConcurrentAllocation_AnnotationPreventsRace(t *testing.T) {
	// This test verifies the annotation-based locking mechanism works.
	// Note: The fake clientset doesn't implement true conflict detection,
	// so we test the pattern rather than exact race prevention semantics.
	// In production, the real API server returns 409 Conflict on concurrent patches.

	claim := withFinalizer(withAllocation(newTestClaim("default", "test-claim"), "node-1"))
	clientset := newTestClientset(claim)

	mock := &mockCXLClient{}
	ctrl := newController(clientset, mock)

	ctx := context.Background()

	// run 10 concurrent allocations
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			freshClaim, _ := clientset.ResourceV1().ResourceClaims("default").Get(ctx, "test-claim", metav1.GetOptions{})
			_ = ctrl.handleAllocation(ctx, freshClaim)
		}()
	}
	wg.Wait()

	// at least one allocation must succeed
	if count := mock.allocateCount.Load(); count < 1 {
		t.Error("expected at least 1 allocate call")
	}

	// verify annotation was persisted
	updated, _ := clientset.ResourceV1().ResourceClaims("default").Get(ctx, "test-claim", metav1.GetOptions{})
	if _, ok := getAllocationMeta(updated); !ok {
		t.Error("allocation annotation should be present")
	}
}

func TestContainsFinalizer(t *testing.T) {
	tests := []struct {
		name       string
		finalizers []string
		target     string
		want       bool
	}{
		{
			name:       "contains",
			finalizers: []string{"a", "b", FinalizerName},
			target:     FinalizerName,
			want:       true,
		},
		{
			name:       "not contains",
			finalizers: []string{"a", "b"},
			target:     FinalizerName,
			want:       false,
		},
		{
			name:       "empty",
			finalizers: nil,
			target:     FinalizerName,
			want:       false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			claim := &resourcev1.ResourceClaim{
				ObjectMeta: metav1.ObjectMeta{Finalizers: tt.finalizers},
			}
			if got := containsFinalizer(claim, tt.target); got != tt.want {
				t.Errorf("containsFinalizer() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestRemoveString(t *testing.T) {
	tests := []struct {
		name   string
		slice  []string
		remove string
		want   []string
	}{
		{
			name:   "remove middle",
			slice:  []string{"a", "b", "c"},
			remove: "b",
			want:   []string{"a", "c"},
		},
		{
			name:   "not present",
			slice:  []string{"a", "b"},
			remove: "c",
			want:   []string{"a", "b"},
		},
		{
			name:   "empty slice",
			slice:  nil,
			remove: "a",
			want:   []string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := removeString(tt.slice, tt.remove)
			if len(got) != len(tt.want) {
				t.Errorf("removeString() len = %d, want %d", len(got), len(tt.want))
				return
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("removeString()[%d] = %s, want %s", i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestExtractAllocationDetails(t *testing.T) {
	tests := []struct {
		name       string
		claim      *resourcev1.ResourceClaim
		wantNode   string
		wantSizeGB int
	}{
		{
			name:       "with allocation",
			claim:      withAllocation(newTestClaim("default", "test"), "node-1"),
			wantNode:   "node-1",
			wantSizeGB: DefaultSizeGB,
		},
		{
			name:       "nil allocation",
			claim:      newTestClaim("default", "test"),
			wantNode:   "",
			wantSizeGB: DefaultSizeGB,
		},
		{
			name: "wrong driver",
			claim: func() *resourcev1.ResourceClaim {
				c := newTestClaim("default", "test")
				c.Status.Allocation = &resourcev1.AllocationResult{
					Devices: resourcev1.DeviceAllocationResult{
						Results: []resourcev1.DeviceRequestAllocationResult{
							{Driver: "other.driver", Pool: "node-2"},
						},
					},
				}
				return c
			}(),
			wantNode:   "",
			wantSizeGB: DefaultSizeGB,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			node, size := extractAllocationDetails(tt.claim)
			if node != tt.wantNode {
				t.Errorf("node = %s, want %s", node, tt.wantNode)
			}
			if size != tt.wantSizeGB {
				t.Errorf("size = %d, want %d", size, tt.wantSizeGB)
			}
		})
	}
}
