# CXL DRA Driver

Kubernetes Dynamic Resource Allocation (DRA) driver for CXL pooled memory orchestration.

## Overview

This driver enables Kubernetes workloads to request CXL (Compute Express Link) pooled memory through the DRA framework. When a pod requests CXL memory via a ResourceClaim, the driver:

1. Allocates memory from the CXL switch pool
2. Prepares the memory for container use via the kubelet plugin interface
3. Releases memory back to the pool when the pod terminates

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

# run tests
make test

# run linters
make lint

# build binaries
make build

# build container images
make docker-build
```

See `make help` for all available targets.

## Architecture

The driver consists of three components:

| Component | Description |
|-----------|-------------|
| Controller | Watches ResourceClaims, calls CXL switch API to allocate/release memory |
| Node Plugin | gRPC server implementing the kubelet DRA plugin interface |
| Mock Switch | Mock CXL hardware switch for development/testing |

See [docs/architecture.md](docs/architecture.md) for detailed design documentation.

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

## Deployment

The driver uses Kustomize for deployment:

```bash
# deploy with default settings
kubectl apply -k deploy/kubernetes

# customize image tags
cd deploy/kubernetes
kustomize edit set image ghcr.io/justin-oleary/cxl-dra-controller:v1.0.0
kubectl apply -k .
```

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md) for development guidelines.

## License

MIT - see [LICENSE](LICENSE)
