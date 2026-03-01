# CXL DRA Driver Architecture

This document describes the systems engineering principles behind the CXL DRA Driver.

## The Two-Body Problem

CXL pooled memory introduces a fundamental distributed systems challenge: **two sources of truth**.

```
┌─────────────────────┐     ┌─────────────────────┐
│   Kubernetes API    │     │   CXL Hardware      │
│      Server         │     │     Switch          │
├─────────────────────┤     ├─────────────────────┤
│ ResourceClaim: foo  │     │ Node: worker-1      │
│ Status: allocated   │     │ Allocated: 64GB     │
│ Node: worker-1      │     │                     │
└─────────────────────┘     └─────────────────────┘
```

The K8s API server believes memory is allocated. The CXL switch has physically allocated memory. But what happens when these states diverge?

| K8s State | Hardware State | Problem |
|-----------|----------------|---------|
| Allocated | Allocated | Consistent (OK) |
| Allocated | Not Allocated | Pod fails at runtime |
| Not Allocated | Allocated | **Hardware leak** |
| Not Allocated | Not Allocated | Consistent (OK) |

The **hardware leak** case is catastrophic: the CXL memory is permanently consumed but invisible to Kubernetes. The pool drains until nothing can allocate.

## Solution: Annotation-Based Distributed Lock

The controller uses the Kubernetes API server as the coordination layer. Before making any CXL hardware call, it claims ownership via an annotation:

```
metadata:
  annotations:
    cxl.example.com/allocated: '{"node":"worker-1","sizeGB":64}'
```

### Allocation Flow

```
┌──────────────┐     ┌──────────────┐     ┌──────────────┐
│  Controller  │     │   K8s API    │     │  CXL Switch  │
└──────┬───────┘     └──────┬───────┘     └──────┬───────┘
       │                    │                    │
       │ 1. GET claim       │                    │
       │───────────────────>│                    │
       │                    │                    │
       │ 2. PATCH annotation│                    │
       │───────────────────>│                    │
       │    (optimistic)    │                    │
       │                    │                    │
       │ 3. 200 OK          │                    │
       │<───────────────────│                    │
       │    (won the race)  │                    │
       │                    │                    │
       │ 4. POST /allocate  │                    │
       │────────────────────────────────────────>│
       │                    │                    │
       │ 5. 200 OK          │                    │
       │<────────────────────────────────────────│
       │                    │                    │
```

### Optimistic Concurrency

Multiple controller replicas may attempt allocation simultaneously. The annotation patch uses Kubernetes optimistic concurrency:

1. Controller A reads claim (no annotation)
2. Controller B reads claim (no annotation)
3. Controller A patches annotation → **succeeds**
4. Controller B patches annotation → **409 Conflict**
5. Controller B re-reads, sees annotation, skips allocation

This prevents double-allocation without distributed locks.

### Failure Recovery

If the CXL hardware call fails after the annotation is set:

```go
if err := c.cxl.Allocate(ctx, nodeName, sizeGB); err != nil {
    // remove annotation so retry can attempt again
    _ = c.removeAllocationAnnotation(ctx, claim.Namespace, claim.Name)
    return err
}
```

The annotation is removed, allowing the next reconciliation to retry cleanly.

## The Finalizer Pattern

Kubernetes deletes objects immediately unless a **finalizer** blocks deletion. Without protection:

```
User: kubectl delete resourceclaim/foo
K8s:  Object deleted
CXL:  Memory still allocated
      → Hardware leak
```

The controller injects `cxl.example.com/finalizer` on every claim:

```yaml
metadata:
  finalizers:
    - cxl.example.com/finalizer
```

### Deletion Flow

```
┌──────────────┐     ┌──────────────┐     ┌──────────────┐
│  Controller  │     │   K8s API    │     │  CXL Switch  │
└──────┬───────┘     └──────┬───────┘     └──────┬───────┘
       │                    │                    │
       │ 1. Watch: deletion │                    │
       │    timestamp set   │                    │
       │<───────────────────│                    │
       │                    │                    │
       │ 2. Read annotation │                    │
       │    {node, sizeGB}  │                    │
       │                    │                    │
       │ 3. POST /release   │                    │
       │────────────────────────────────────────>│
       │                    │                    │
       │ 4. 200 OK          │                    │
       │<────────────────────────────────────────│
       │                    │                    │
       │ 5. PATCH: remove   │                    │
       │    finalizer       │                    │
       │───────────────────>│                    │
       │                    │                    │
       │ 6. Object deleted  │                    │
       │<───────────────────│                    │
       │                    │                    │
```

### Graceful Degradation

If the CXL switch is unavailable during deletion:

```go
if err := c.cxl.Release(ctx, meta.Node, meta.SizeGB); err != nil {
    // do NOT remove finalizer
    // claim stays in Terminating until hardware is proven clean
    return err
}
```

The claim remains stuck in `Terminating` state. The controller backs off and retries with exponential backoff. The invariant holds: **the finalizer is only removed after hardware is released**.

## Component Architecture

```
┌─────────────────────────────────────────────────────────────┐
│                     Kubernetes Cluster                       │
│                                                              │
│  ┌──────────────────┐     ┌──────────────────────────────┐  │
│  │   Controller     │     │      Node Plugin (DaemonSet) │  │
│  │   (Deployment)   │     │                              │  │
│  ├──────────────────┤     ├──────────────────────────────┤  │
│  │ - Watch claims   │     │ - Register with kubelet      │  │
│  │ - Add finalizers │     │ - NodePrepareResources       │  │
│  │ - Allocate CXL   │     │ - NodeUnprepareResources     │  │
│  │ - Release CXL    │     │                              │  │
│  └────────┬─────────┘     └──────────────────────────────┘  │
│           │                                                  │
└───────────┼──────────────────────────────────────────────────┘
            │
            │ HTTP POST /allocate, /release
            ▼
┌─────────────────────┐
│   CXL Memory Switch │
│   (External)        │
├─────────────────────┤
│ - Pool management   │
│ - Physical attach   │
└─────────────────────┘
```

## Key Design Decisions

### Why annotations instead of status?

ResourceClaim status is managed by the scheduler. Writing to status would conflict with scheduler updates and require careful field management. Annotations are under controller ownership and avoid coordination issues.

### Why optimistic locking instead of leader election?

Leader election adds latency and a single point of failure. Optimistic locking allows multiple replicas to serve claims, with the API server acting as arbiter. Conflicts are rare in practice and handled gracefully.

### Why not cache allocation state in memory?

Controller restarts would lose state, creating potential hardware leaks. The API server is the durable coordination layer. Every reconciliation reads fresh state.

## Invariants

These properties must hold at all times:

1. **No double allocation**: Only one CXL allocation per claim, enforced by annotation patch.

2. **No hardware leaks**: Finalizer is removed only after successful CXL release.

3. **Crash recovery**: All state is reconstructed from API server on startup.

4. **Idempotency**: Repeated reconciliation of the same claim is safe.
