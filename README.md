# cxl-dra-driver

Kubernetes Dynamic Resource Allocation (DRA) driver for CXL pooled memory orchestration. Targets Kubernetes v1.35 with the stable `resource.k8s.io/v1` API.

## Overview

This driver enables Kubernetes workloads to request CXL (Compute Express Link) pooled memory through the DRA framework. When a pod requests CXL memory via a ResourceClaim, the driver:

1. Allocates memory from the CXL switch pool
2. Prepares the memory for container use via the kubelet plugin interface
3. Releases memory back to the pool when the pod terminates

## Components

| Component | Description |
|-----------|-------------|
| `cmd/controller` | Watches ResourceClaims, calls CXL switch API to allocate/release memory |
| `cmd/node-plugin` | gRPC server implementing the kubelet DRA plugin interface |
| `cxl-mock-switch` | Mock CXL hardware switch for development/testing |

## Requirements

- Kubernetes v1.35+ with `DynamicResourceAllocation` feature gate enabled
- BuildKit (for in-cluster builds)
- Go 1.25+ (for local development)

## Quick Start

```bash
# build images and deploy
./build-in-cluster.sh
kubectl apply -f deploy/kubernetes/

# run e2e test
./e2e-test.sh
```

## Project Structure

```
├── cmd/
│   ├── controller/main.go      # DRA controller entrypoint
│   └── node-plugin/main.go     # Node plugin entrypoint
├── pkg/
│   ├── controller/             # Informer-based reconciliation
│   ├── cxlclient/              # HTTP client for CXL switch
│   └── nodeplugin/             # gRPC plugin implementation
├── cxl-mock-switch/            # Mock hardware for testing
├── deploy/
│   ├── kubernetes/             # RBAC, deployments, device class
│   ├── buildkit/               # In-cluster build infrastructure
│   └── test-workload.yaml      # Sample pod with CXL claim
└── Dockerfile.*                # Multi-stage container builds
```

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

## Configuration

The controller accepts:
- `--cxl-endpoint` - CXL switch API URL (default: `http://localhost:8080`)
- `--kubeconfig` - Path to kubeconfig (uses in-cluster config if empty)

The node plugin accepts:
- `--node-name` - Kubernetes node name (required)

## Development

```bash
# run mock switch locally
go run ./cxl-mock-switch

# build and test
go build ./...
go test ./...
```

## License

MIT
