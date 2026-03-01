# CXL DRA Driver

[![CI](https://github.com/justin-oleary/cxl-dra-driver/actions/workflows/ci.yaml/badge.svg)](https://github.com/justin-oleary/cxl-dra-driver/actions/workflows/ci.yaml)
[![Release](https://img.shields.io/github/v/release/justin-oleary/cxl-dra-driver)](https://github.com/justin-oleary/cxl-dra-driver/releases)
[![Go Report Card](https://goreportcard.com/badge/github.com/justin-oleary/cxl-dra-driver)](https://goreportcard.com/report/github.com/justin-oleary/cxl-dra-driver)
[![Go Version](https://img.shields.io/badge/go-1.25+-blue)](https://go.dev/)
[![License](https://img.shields.io/badge/license-MIT-green)](LICENSE)

Kubernetes Dynamic Resource Allocation (DRA) driver for CXL pooled memory orchestration.

## Contents

- [Overview](#overview)
- [Architecture](#architecture)
- [Requirements](#requirements)
- [Quick Start](#quick-start)
- [Usage](#usage)
- [Configuration](#configuration)
- [Development](#development)
- [Contributing](#contributing)

## Overview

This driver enables Kubernetes workloads to request CXL (Compute Express Link) pooled memory through the DRA framework. When a pod requests CXL memory via a ResourceClaim, the driver:

1. Allocates memory from the CXL switch pool
2. Prepares the memory for container use via the kubelet plugin interface
3. Releases memory back to the pool when the pod terminates

### Allocation and Release Flow

```mermaid
sequenceDiagram
    participant User
    participant K8s API as Kubernetes API
    participant Scheduler
    participant Controller as DRA Controller
    participant CXL as CXL Switch
    participant Kubelet
    participant Plugin as Node Plugin

    Note over User,Plugin: Allocation Flow
    User->>K8s API: Create Pod + ResourceClaim
    K8s API->>Controller: Watch: new ResourceClaim
    Controller->>K8s API: PATCH annotation (claim lock)
    Controller->>CXL: POST /allocate (node, sizeGB)
    CXL-->>Controller: 200 OK
    Controller->>K8s API: Add finalizer
    Scheduler->>K8s API: Bind Pod to Node
    Kubelet->>Plugin: NodePrepareResources()
    Plugin-->>Kubelet: CDI device specs
    Kubelet->>Kubelet: Start container with CXL memory

    Note over User,Plugin: Finalizer-Driven Release
    User->>K8s API: Delete Pod
    K8s API->>K8s API: Set deletionTimestamp
    K8s API->>Controller: Watch: claim terminating
    Controller->>CXL: POST /release (node, sizeGB)
    CXL-->>Controller: 200 OK
    Controller->>K8s API: Remove finalizer
    K8s API->>K8s API: Object deleted
```

The controller uses an **annotation-based distributed lock** to prevent double allocation across replicas, and a **finalizer** to guarantee hardware release before API object deletion. See [docs/architecture.md](docs/architecture.md) for the full systems engineering analysis.

## Architecture

```
┌─────────────────────────────────────────────────────────────────┐
│                     Kubernetes Cluster                          │
│  ┌──────────────────┐           ┌───────────────────────────┐  │
│  │  DRA Controller  │           │  Node Plugin (DaemonSet)  │  │
│  │   (Deployment)   │           │                           │  │
│  ├──────────────────┤           ├───────────────────────────┤  │
│  │ • Watch claims   │           │ • kubelet gRPC interface  │  │
│  │ • Annotation lock│           │ • NodePrepareResources    │  │
│  │ • Finalizer mgmt │           │ • NodeUnprepareResources  │  │
│  └────────┬─────────┘           └───────────────────────────┘  │
│           │                                                     │
└───────────┼─────────────────────────────────────────────────────┘
            │ HTTP
            ▼
┌─────────────────────┐
│  CXL Memory Switch  │
│     (External)      │
├─────────────────────┤
│ • /allocate         │
│ • /release          │
│ • Pool management   │
└─────────────────────┘
```

| Component | Description |
|-----------|-------------|
| Controller | Watches ResourceClaims, coordinates with CXL switch via annotation-based locking |
| Node Plugin | gRPC server implementing kubelet DRA plugin interface |
| Mock Switch | Development/testing CXL hardware simulator |

## Requirements

- Kubernetes v1.35+ with `DynamicResourceAllocation` feature gate enabled
- Go 1.25+ (for development)

## Quick Start

```bash
# deploy to cluster
make deploy

# verify pods are running
kubectl -n cxl-system get pods
```


## Development

```bash
# install dependencies
go mod download

# run tests with coverage
make test

# run linters
make lint

# run fuzz tests
make fuzz

# build binaries
make build

# build container images
make docker-build
```

See `make help` for all available targets.

## Usage

Request CXL memory in a pod:

```yaml
apiVersion: v1
kind: Pod
metadata:
  name: my-app
spec:
  containers:
    - name: app
      image: myapp:latest
      resources:
        claims:
          - name: cxl-memory
  resourceClaims:
    - name: cxl-memory
      resourceClaimTemplateName: cxl-memory-claim-template
```

See [deploy/examples/](deploy/examples/) for complete examples.

## Configuration

Controller flags:
- `--cxl-endpoint` - CXL switch API URL (default: `http://localhost:8080`)
- `--kubeconfig` - Path to kubeconfig (uses in-cluster config if empty)

Node plugin flags:
- `--node-name` - Kubernetes node name (required)

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md) for development guidelines.

## License

MIT - see [LICENSE](LICENSE)
