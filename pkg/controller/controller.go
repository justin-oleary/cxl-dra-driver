// Package controller implements the DRA controller for CXL memory allocation.
package controller

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	resourcev1 "k8s.io/api/resource/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	resourcelisters "k8s.io/client-go/listers/resource/v1"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/record"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/klog/v2"

	"github.com/justin-oleary/cxl-dra-driver/pkg/cxlclient"
)

const (
	DriverName    = "cxl.example.com"
	FinalizerName = "cxl.example.com/finalizer"
	DefaultSizeGB = 64

	// AnnotationAllocated marks a claim as having CXL memory allocated on hardware.
	// Value contains allocation metadata (node, size).
	AnnotationAllocated = "cxl.example.com/allocated"

	// maxRetries for optimistic concurrency conflicts
	maxRetries = 5
)

// AllocationMeta stores metadata about the CXL allocation persisted in annotation.
type AllocationMeta struct {
	Node   string `json:"node"`
	SizeGB int    `json:"sizeGB"`
}

type Controller struct {
	clientset        kubernetes.Interface
	claimLister      resourcelisters.ResourceClaimLister
	claimSynced      cache.InformerSynced
	workqueue        workqueue.TypedRateLimitingInterface[string]
	cxl              cxlclient.CXLClient
	recorder         record.EventRecorder
	eventBroadcaster record.EventBroadcaster
}

func New(clientset kubernetes.Interface, factory informers.SharedInformerFactory, cxl cxlclient.CXLClient) *Controller {
	claimInformer := factory.Resource().V1().ResourceClaims()

	eventBroadcaster := record.NewBroadcaster()
	eventBroadcaster.StartRecordingToSink(&corev1EventSink{clientset: clientset})
	recorder := eventBroadcaster.NewRecorder(nil, corev1.EventSource{Component: "cxl-dra-controller"})

	c := &Controller{
		clientset:        clientset,
		claimLister:      claimInformer.Lister(),
		claimSynced:      claimInformer.Informer().HasSynced,
		workqueue:        workqueue.NewTypedRateLimitingQueue(workqueue.DefaultTypedControllerRateLimiter[string]()),
		cxl:              cxl,
		recorder:         recorder,
		eventBroadcaster: eventBroadcaster,
	}

	_, _ = claimInformer.Informer().AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			claim := obj.(*resourcev1.ResourceClaim)
			if c.isOurClaim(claim) {
				c.enqueue(claim)
			}
		},
		UpdateFunc: func(old, new interface{}) {
			claim := new.(*resourcev1.ResourceClaim)
			if c.isOurClaim(claim) {
				c.enqueue(claim)
			}
		},
		DeleteFunc: func(obj interface{}) {
			claim, ok := obj.(*resourcev1.ResourceClaim)
			if !ok {
				tombstone, ok := obj.(cache.DeletedFinalStateUnknown)
				if !ok {
					return
				}
				claim, ok = tombstone.Obj.(*resourcev1.ResourceClaim)
				if !ok {
					return
				}
			}
			if c.isOurClaim(claim) {
				c.enqueue(claim)
			}
		},
	})

	return c
}

func (c *Controller) Run(ctx context.Context, workers int, onReady func()) error {
	defer c.workqueue.ShutDown()
	defer c.eventBroadcaster.Shutdown()

	klog.InfoS("starting cxl-dra-controller")

	klog.InfoS("waiting for informer caches to sync")
	if !cache.WaitForCacheSync(ctx.Done(), c.claimSynced) {
		return fmt.Errorf("failed to wait for caches to sync")
	}

	if onReady != nil {
		onReady()
	}

	klog.InfoS("starting workers", "count", workers)
	for i := 0; i < workers; i++ {
		go wait.UntilWithContext(ctx, c.runWorker, time.Second)
	}

	<-ctx.Done()
	klog.InfoS("shutting down workers")
	return nil
}

func (c *Controller) runWorker(ctx context.Context) {
	for c.processNextWorkItem(ctx) {
	}
}

func (c *Controller) processNextWorkItem(ctx context.Context) bool {
	key, shutdown := c.workqueue.Get()
	if shutdown {
		return false
	}
	defer c.workqueue.Done(key)

	err := c.syncHandler(ctx, key)
	if err == nil {
		c.workqueue.Forget(key)
		return true
	}

	klog.ErrorS(err, "error syncing resource claim", "key", key)
	c.workqueue.AddRateLimited(key)
	return true
}

func (c *Controller) syncHandler(ctx context.Context, key string) error {
	namespace, name, err := cache.SplitMetaNamespaceKey(key)
	if err != nil {
		return fmt.Errorf("invalid key %q: %w", key, err)
	}

	claim, err := c.claimLister.ResourceClaims(namespace).Get(name)
	if err != nil {
		if apierrors.IsNotFound(err) {
			klog.V(4).InfoS("resource claim deleted", "key", key)
			return nil
		}
		return err
	}

	// handle deletion
	if !claim.DeletionTimestamp.IsZero() {
		return c.handleDeletion(ctx, claim)
	}

	// ensure finalizer exists
	if !containsFinalizer(claim, FinalizerName) {
		return c.addFinalizer(ctx, claim)
	}

	// handle allocation
	return c.handleAllocation(ctx, claim)
}

func (c *Controller) handleDeletion(ctx context.Context, claim *resourcev1.ResourceClaim) error {
	if !containsFinalizer(claim, FinalizerName) {
		return nil
	}

	// check if we have CXL memory allocated (persisted in annotation)
	meta, hasAllocation := getAllocationMeta(claim)
	if hasAllocation {
		if err := c.cxl.Release(ctx, meta.Node, meta.SizeGB); err != nil {
			c.recorder.Eventf(claim, corev1.EventTypeWarning, "ReleaseFailed",
				"Failed to release CXL memory: %v", err)
			return err
		}
		c.recorder.Event(claim, corev1.EventTypeNormal, "Released",
			"CXL memory released successfully")
	}

	return c.removeFinalizer(ctx, claim)
}

func (c *Controller) handleAllocation(ctx context.Context, claim *resourcev1.ResourceClaim) error {
	if claim.Status.Allocation == nil || len(claim.Status.ReservedFor) == 0 {
		return nil
	}

	// quick check on passed claim (may be stale but avoids API call in common case)
	if _, hasAllocation := getAllocationMeta(claim); hasAllocation {
		return nil
	}

	nodeName, sizeGB := extractAllocationDetails(claim)
	if nodeName == "" {
		klog.V(4).InfoS("no node assignment yet", "claim", klog.KObj(claim))
		return nil
	}

	// try to claim the allocation by setting annotation first (optimistic lock)
	// this prevents concurrent workers from both allocating
	claimed, err := c.tryClaimAllocation(ctx, claim.Namespace, claim.Name, nodeName, sizeGB)
	if err != nil {
		return err
	}
	if !claimed {
		// another worker already claimed it
		return nil
	}

	// we won the race, now allocate on CXL hardware
	if err := c.cxl.Allocate(ctx, nodeName, sizeGB); err != nil {
		// allocation failed, remove our claim so retry can attempt again
		_ = c.removeAllocationAnnotation(ctx, claim.Namespace, claim.Name)
		if errors.Is(err, cxlclient.ErrInsufficientMemory) {
			c.recorder.Eventf(claim, corev1.EventTypeWarning, "AllocationFailed",
				"Insufficient CXL memory for %dGB request", sizeGB)
		}
		return err
	}

	c.recorder.Eventf(claim, corev1.EventTypeNormal, "Allocated",
		"Allocated %dGB CXL memory on node %s", sizeGB, nodeName)

	return nil
}

// tryClaimAllocation atomically tries to claim an allocation by setting the annotation.
// Returns true if this worker won the race, false if another worker already claimed it.
func (c *Controller) tryClaimAllocation(ctx context.Context, namespace, name, node string, sizeGB int) (bool, error) {
	meta := AllocationMeta{Node: node, SizeGB: sizeGB}
	metaJSON, err := json.Marshal(meta)
	if err != nil {
		return false, fmt.Errorf("marshal allocation meta: %w", err)
	}

	// use conditional patch - only set if annotation doesn't exist
	// we achieve this by first checking if annotation exists, then patching
	claim, err := c.clientset.ResourceV1().ResourceClaims(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			return false, nil
		}
		return false, err
	}

	// check if already claimed
	if _, exists := getAllocationMeta(claim); exists {
		return false, nil
	}

	// try to patch - if conflict, someone else won
	err = c.patchAnnotation(ctx, namespace, name, AnnotationAllocated, string(metaJSON))
	if err != nil {
		if apierrors.IsConflict(err) {
			// another worker may have set it
			return false, nil
		}
		return false, err
	}

	return true, nil
}

// removeAllocationAnnotation removes the allocation annotation (used on allocation failure)
func (c *Controller) removeAllocationAnnotation(ctx context.Context, namespace, name string) error {
	patch := []byte(`{"metadata":{"annotations":{"` + AnnotationAllocated + `":null}}}`)
	_, err := c.clientset.ResourceV1().ResourceClaims(namespace).
		Patch(ctx, name, types.MergePatchType, patch, metav1.PatchOptions{})
	return err
}

// patchAnnotation sets an annotation using JSON merge patch with conflict retry.
func (c *Controller) patchAnnotation(ctx context.Context, namespace, name, key, value string) error {
	patch := map[string]interface{}{
		"metadata": map[string]interface{}{
			"annotations": map[string]string{
				key: value,
			},
		},
	}
	patchBytes, err := json.Marshal(patch)
	if err != nil {
		return fmt.Errorf("marshal patch: %w", err)
	}

	var lastErr error
	for i := 0; i < maxRetries; i++ {
		_, err := c.clientset.ResourceV1().ResourceClaims(namespace).
			Patch(ctx, name, types.MergePatchType, patchBytes, metav1.PatchOptions{})
		if err == nil {
			return nil
		}
		if apierrors.IsConflict(err) {
			klog.V(4).InfoS("conflict patching annotation, retrying", "attempt", i+1, "name", name)
			lastErr = err
			continue
		}
		return err
	}
	return fmt.Errorf("max retries exceeded: %w", lastErr)
}

func (c *Controller) addFinalizer(ctx context.Context, claim *resourcev1.ResourceClaim) error {
	return c.updateWithRetry(ctx, claim, func(c *resourcev1.ResourceClaim) {
		c.Finalizers = append(c.Finalizers, FinalizerName)
	})
}

func (c *Controller) removeFinalizer(ctx context.Context, claim *resourcev1.ResourceClaim) error {
	return c.updateWithRetry(ctx, claim, func(c *resourcev1.ResourceClaim) {
		c.Finalizers = removeString(c.Finalizers, FinalizerName)
		// also remove the allocation annotation if present
		if c.Annotations != nil {
			delete(c.Annotations, AnnotationAllocated)
		}
	})
}

// updateWithRetry performs an update with optimistic concurrency retry on conflict.
func (c *Controller) updateWithRetry(ctx context.Context, claim *resourcev1.ResourceClaim, mutate func(*resourcev1.ResourceClaim)) error {
	var lastErr error
	for i := 0; i < maxRetries; i++ {
		// on retry, fetch fresh copy
		current := claim
		if i > 0 {
			var err error
			current, err = c.clientset.ResourceV1().ResourceClaims(claim.Namespace).Get(ctx, claim.Name, metav1.GetOptions{})
			if err != nil {
				return err
			}
		}

		claimCopy := current.DeepCopy()
		mutate(claimCopy)

		_, err := c.clientset.ResourceV1().ResourceClaims(claim.Namespace).
			Update(ctx, claimCopy, metav1.UpdateOptions{})
		if err == nil {
			return nil
		}
		if apierrors.IsConflict(err) {
			klog.V(4).InfoS("conflict updating claim, retrying", "attempt", i+1, "claim", klog.KObj(claim))
			lastErr = err
			continue
		}
		return err
	}
	return fmt.Errorf("max retries exceeded: %w", lastErr)
}

func (c *Controller) enqueue(claim *resourcev1.ResourceClaim) {
	key, err := cache.MetaNamespaceKeyFunc(claim)
	if err != nil {
		klog.ErrorS(err, "failed to get key for claim", "claim", klog.KObj(claim))
		return
	}
	c.workqueue.Add(key)
}

func (c *Controller) isOurClaim(claim *resourcev1.ResourceClaim) bool {
	// also consider claims with our finalizer (for deletion handling)
	if containsFinalizer(claim, FinalizerName) {
		return true
	}
	if claim.Status.Allocation == nil {
		return false
	}
	for _, result := range claim.Status.Allocation.Devices.Results {
		if result.Driver == DriverName {
			return true
		}
	}
	return false
}

// getAllocationMeta retrieves allocation metadata from the claim annotation.
func getAllocationMeta(claim *resourcev1.ResourceClaim) (AllocationMeta, bool) {
	if claim.Annotations == nil {
		return AllocationMeta{}, false
	}
	metaJSON, ok := claim.Annotations[AnnotationAllocated]
	if !ok {
		return AllocationMeta{}, false
	}
	var meta AllocationMeta
	if err := json.Unmarshal([]byte(metaJSON), &meta); err != nil {
		return AllocationMeta{}, false
	}
	return meta, true
}

func containsFinalizer(claim *resourcev1.ResourceClaim, finalizer string) bool {
	for _, f := range claim.Finalizers {
		if f == finalizer {
			return true
		}
	}
	return false
}

func removeString(slice []string, s string) []string {
	result := make([]string, 0, len(slice))
	for _, item := range slice {
		if item != s {
			result = append(result, item)
		}
	}
	return result
}

func extractAllocationDetails(claim *resourcev1.ResourceClaim) (nodeName string, sizeGB int) {
	sizeGB = DefaultSizeGB
	if claim.Status.Allocation == nil {
		return "", sizeGB
	}
	for _, result := range claim.Status.Allocation.Devices.Results {
		if result.Driver == DriverName {
			// node is stored in the pool field in DRA v1
			nodeName = result.Pool
			break
		}
	}
	return nodeName, sizeGB
}

// corev1EventSink implements EventSink for recording events
type corev1EventSink struct {
	clientset kubernetes.Interface
}

func (s *corev1EventSink) Create(event *corev1.Event) (*corev1.Event, error) {
	return s.clientset.CoreV1().Events(event.Namespace).Create(context.Background(), event, metav1.CreateOptions{})
}

func (s *corev1EventSink) Update(event *corev1.Event) (*corev1.Event, error) {
	return s.clientset.CoreV1().Events(event.Namespace).Update(context.Background(), event, metav1.UpdateOptions{})
}

func (s *corev1EventSink) Patch(oldEvent *corev1.Event, data []byte) (*corev1.Event, error) {
	return s.clientset.CoreV1().Events(oldEvent.Namespace).Patch(context.Background(), oldEvent.Name, types.StrategicMergePatchType, data, metav1.PatchOptions{})
}
