// Package controller implements the DRA controller for CXL memory allocation.
package controller

import (
	"context"
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
)

type Controller struct {
	clientset   kubernetes.Interface
	claimLister resourcelisters.ResourceClaimLister
	claimSynced cache.InformerSynced
	workqueue   workqueue.TypedRateLimitingInterface[string]
	cxl         *cxlclient.Client
	recorder    record.EventRecorder
}

func New(clientset kubernetes.Interface, factory informers.SharedInformerFactory, cxl *cxlclient.Client) *Controller {
	claimInformer := factory.Resource().V1().ResourceClaims()

	eventBroadcaster := record.NewBroadcaster()
	eventBroadcaster.StartRecordingToSink(&corev1EventSink{clientset: clientset})
	recorder := eventBroadcaster.NewRecorder(nil, corev1.EventSource{Component: "cxl-dra-controller"})

	c := &Controller{
		clientset:   clientset,
		claimLister: claimInformer.Lister(),
		claimSynced: claimInformer.Informer().HasSynced,
		workqueue:   workqueue.NewTypedRateLimitingQueue(workqueue.DefaultTypedControllerRateLimiter[string]()),
		cxl:         cxl,
		recorder:    recorder,
	}

	claimInformer.Informer().AddEventHandler(cache.ResourceEventHandlerFuncs{
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

func (c *Controller) Run(ctx context.Context, workers int) error {
	defer c.workqueue.ShutDown()

	klog.InfoS("starting cxl-dra-controller")

	klog.InfoS("waiting for informer caches to sync")
	if !cache.WaitForCacheSync(ctx.Done(), c.claimSynced) {
		return fmt.Errorf("failed to wait for caches to sync")
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

	nodeName, sizeGB := extractAllocationDetails(claim)
	if nodeName != "" {
		if err := c.cxl.Release(ctx, nodeName, sizeGB); err != nil {
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
	if c.isAllocatedByUs(claim) {
		return nil
	}

	if claim.Status.Allocation == nil || len(claim.Status.ReservedFor) == 0 {
		return nil
	}

	nodeName := extractNodeFromAllocation(claim)
	if nodeName == "" {
		klog.V(4).InfoS("no node assignment yet", "claim", klog.KObj(claim))
		return nil
	}

	sizeGB := DefaultSizeGB

	if err := c.cxl.Allocate(ctx, nodeName, sizeGB); err != nil {
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

func (c *Controller) addFinalizer(ctx context.Context, claim *resourcev1.ResourceClaim) error {
	claimCopy := claim.DeepCopy()
	claimCopy.Finalizers = append(claimCopy.Finalizers, FinalizerName)
	_, err := c.clientset.ResourceV1().ResourceClaims(claim.Namespace).
		Update(ctx, claimCopy, metav1.UpdateOptions{})
	return err
}

func (c *Controller) removeFinalizer(ctx context.Context, claim *resourcev1.ResourceClaim) error {
	claimCopy := claim.DeepCopy()
	claimCopy.Finalizers = removeString(claimCopy.Finalizers, FinalizerName)
	_, err := c.clientset.ResourceV1().ResourceClaims(claim.Namespace).
		Update(ctx, claimCopy, metav1.UpdateOptions{})
	return err
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

func (c *Controller) isAllocatedByUs(claim *resourcev1.ResourceClaim) bool {
	// check if we've already recorded allocation via annotation
	if claim.Annotations == nil {
		return false
	}
	_, ok := claim.Annotations["cxl.example.com/allocated"]
	return ok
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

func extractNodeFromAllocation(claim *resourcev1.ResourceClaim) string {
	if claim.Status.Allocation == nil {
		return ""
	}
	for _, result := range claim.Status.Allocation.Devices.Results {
		if result.Driver == DriverName {
			return result.Pool
		}
	}
	return ""
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
