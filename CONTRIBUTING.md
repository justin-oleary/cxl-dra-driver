# Contributing to CXL DRA Driver

Thank you for your interest in contributing! This document outlines the development workflow and contribution guidelines.

## Development Setup

### Prerequisites

- Go 1.25+
- Docker (for container builds)
- kubectl and a Kubernetes cluster (for integration testing)
- golangci-lint and gosec (for linting)

### Clone and Build

```bash
git clone https://github.com/justin-oleary/cxl-dra-driver.git
cd cxl-dra-driver

# Install dependencies
go mod download

# Build binaries
make build

# Run tests
make test

# Run linters
make lint
```

### Running the Mock Switch

The mock switch simulates CXL hardware for local development:

```bash
# Build and run the mock switch
make build
./bin/cxl-mock-switch --addr :8080

# In another terminal, test the API
curl http://localhost:8080/health
curl -X POST http://localhost:8080/allocate -d '{"node":"test-node","sizeGB":64}'
curl -X POST http://localhost:8080/release -d '{"node":"test-node","sizeGB":64}'
```

### Running the Controller Locally

```bash
# Point to your kubeconfig and mock switch
./bin/cxl-controller \
  --kubeconfig ~/.kube/config \
  --cxl-endpoint http://localhost:8080
```

---

## Pull Request Process

### 1. Fork and Branch

```bash
# Fork via GitHub UI, then:
git clone https://github.com/YOUR_USERNAME/cxl-dra-driver.git
cd cxl-dra-driver
git checkout -b feature/my-feature
```

### 2. Make Changes

- Write clean, idiomatic Go code
- Add tests for new functionality
- Update documentation if needed

### 3. Run Checks Locally

```bash
# This runs lint + test
make verify
```

### 4. Commit

Use lowercase imperative commit messages:

```
fix null pointer in allocation handler
add retry logic for api conflicts
update docs for new flag
```

### 5. Push and Open PR

```bash
git push origin feature/my-feature
```

Open a PR against `main`. Fill out the PR template checklist.

---

## Code Standards

### Go Code

- Follow standard Go conventions
- Use `context.Context` for cancellation
- Handle all errors explicitly
- Avoid unnecessary abstractions

### Tests

Use table-driven tests:

```go
func TestAllocate(t *testing.T) {
    tests := []struct {
        name    string
        input   AllocateRequest
        wantErr bool
    }{
        {"valid request", AllocateRequest{Node: "n1", SizeGB: 64}, false},
        {"missing node", AllocateRequest{SizeGB: 64}, true},
    }
    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            err := Allocate(tt.input)
            if (err != nil) != tt.wantErr {
                t.Errorf("Allocate() error = %v, wantErr %v", err, tt.wantErr)
            }
        })
    }
}
```

### Kubernetes Resources

- Use kustomize for deployment customization
- Include resource requests and limits
- Follow Pod Security Standards
- Add liveness and readiness probes

---

## CI Requirements

All PRs must pass:

- `golangci-lint` — static analysis
- `gosec` — security scanning
- `go test -race` — unit tests with race detector
- Container image builds

---

## Security

Report security vulnerabilities privately via [GitHub Security Advisories](https://github.com/justin-oleary/cxl-dra-driver/security/advisories) rather than public issues.

---

## License

By contributing, you agree that your contributions will be licensed under the MIT License.
