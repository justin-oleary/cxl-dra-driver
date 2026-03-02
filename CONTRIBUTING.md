# Contributing to CXL DRA Driver

Thank you for your interest in contributing. This document outlines the process and standards for contributions.

## Development Setup

```bash
# clone the repository
git clone https://github.com/justin-oleary/cxl-dra-driver.git
cd cxl-dra-driver

# install dependencies
go mod download

# run tests
make test

# run linters
make lint
```

## Pull Request Process

1. **Fork and branch**: Create a feature branch from `main`.

2. **Write tests**: All new features must include table-driven tests. Bug fixes should include a regression test that fails without the fix.

3. **Run checks locally**:
   ```bash
   make verify  # runs lint + test
   ```

4. **Commit messages**: Use lowercase imperative form. Keep it short.
   ```
   fix null pointer in allocation handler
   add retry logic for api server conflicts
   ```

5. **Open PR**: Target the `main` branch. The CI pipeline must pass before merge.

## Code Standards

### Go Code

- Follow standard Go conventions and idioms
- Use `context.Context` for cancellation and timeouts
- Handle all errors explicitly
- Avoid unnecessary abstractions

### Tests

- Use table-driven tests for multiple input scenarios
- Include both happy path and error cases
- Use `t.Parallel()` where safe
- Mock external dependencies (CXL client, Kubernetes API)

Example:
```go
func TestFoo(t *testing.T) {
    tests := []struct {
        name    string
        input   string
        want    string
        wantErr bool
    }{
        {"valid input", "foo", "bar", false},
        {"empty input", "", "", true},
    }
    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            got, err := Foo(tt.input)
            if (err != nil) != tt.wantErr {
                t.Errorf("Foo() error = %v, wantErr %v", err, tt.wantErr)
            }
            if got != tt.want {
                t.Errorf("Foo() = %v, want %v", got, tt.want)
            }
        })
    }
}
```

### Kubernetes Resources

- Use kustomize for deployment customization
- Include resource requests and limits
- Follow Pod Security Standards (restricted where possible)
- Add health probes to long-running containers

## CI Requirements

All PRs must pass:

- `golangci-lint` - static analysis
- `gosec` - security scanning
- `go test -race` - unit tests with race detector
- Container image builds

## Security

Report security vulnerabilities privately via GitHub Security Advisories rather than public issues.

## License

By contributing, you agree that your contributions will be licensed under the MIT License.
