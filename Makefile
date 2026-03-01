.PHONY: all build test fuzz lint docker-build deploy clean help

# Build configuration
BINARY_DIR := bin
CONTROLLER := $(BINARY_DIR)/cxl-controller
NODE_PLUGIN := $(BINARY_DIR)/cxl-node-plugin
MOCK_SWITCH := $(BINARY_DIR)/cxl-mock-switch

# Container configuration
REGISTRY ?= ghcr.io/justin-oleary
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
CONTROLLER_IMAGE := $(REGISTRY)/cxl-dra-controller:$(VERSION)
NODE_PLUGIN_IMAGE := $(REGISTRY)/cxl-node-plugin:$(VERSION)
MOCK_SWITCH_IMAGE := $(REGISTRY)/cxl-mock-switch:$(VERSION)

# Go configuration
GOFLAGS := -trimpath
LDFLAGS := -s -w -X main.version=$(VERSION)

all: lint test build

## Build targets
build: $(CONTROLLER) $(NODE_PLUGIN) $(MOCK_SWITCH)

$(BINARY_DIR):
	mkdir -p $(BINARY_DIR)

$(CONTROLLER): $(BINARY_DIR)
	CGO_ENABLED=0 go build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o $@ ./cmd/controller

$(NODE_PLUGIN): $(BINARY_DIR)
	CGO_ENABLED=0 go build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o $@ ./cmd/node-plugin

$(MOCK_SWITCH): $(BINARY_DIR)
	CGO_ENABLED=0 go build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o $@ ./cxl-mock-switch

## Test targets
test:
	go test -race -v ./...

fuzz:
	go test -fuzz=FuzzGetAllocationMeta -fuzztime=30s ./pkg/controller/...
	go test -fuzz=FuzzAllocationMetaMarshal -fuzztime=30s ./pkg/controller/...

## Lint targets
lint:
	@which golangci-lint > /dev/null || (echo "golangci-lint not found, install: go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest" && exit 1)
	@which gosec > /dev/null || (echo "gosec not found, install: go install github.com/securego/gosec/v2/cmd/gosec@latest" && exit 1)
	golangci-lint run ./...
	gosec -quiet ./...

## Container targets
docker-build:
	docker build -t $(CONTROLLER_IMAGE) -f Dockerfile.controller .
	docker build -t $(NODE_PLUGIN_IMAGE) -f Dockerfile.node-plugin .
	docker build -t $(MOCK_SWITCH_IMAGE) -f Dockerfile.mock-switch .

docker-push: docker-build
	docker push $(CONTROLLER_IMAGE)
	docker push $(NODE_PLUGIN_IMAGE)
	docker push $(MOCK_SWITCH_IMAGE)

## Deploy targets
deploy:
	kubectl apply -k deploy/kubernetes

undeploy:
	kubectl delete -k deploy/kubernetes --ignore-not-found

## Utility targets
clean:
	rm -rf $(BINARY_DIR)
	rm -rf pkg/controller/testdata/fuzz

fmt:
	go fmt ./...
	goimports -w .

verify: lint test
	@echo "All checks passed"

help:
	@echo "CXL DRA Driver - Makefile Targets"
	@echo ""
	@echo "Build:"
	@echo "  build        Build all binaries"
	@echo "  docker-build Build container images"
	@echo "  docker-push  Build and push container images"
	@echo ""
	@echo "Test:"
	@echo "  test         Run unit tests with race detector"
	@echo "  fuzz         Run fuzz tests (30s each)"
	@echo "  lint         Run linters (golangci-lint, gosec)"
	@echo "  verify       Run all checks (lint + test)"
	@echo ""
	@echo "Deploy:"
	@echo "  deploy       Apply manifests via kustomize"
	@echo "  undeploy     Remove manifests"
	@echo ""
	@echo "Utility:"
	@echo "  clean        Remove build artifacts"
	@echo "  fmt          Format code"
	@echo "  help         Show this help"
	@echo ""
	@echo "Variables:"
	@echo "  REGISTRY     Container registry (default: ghcr.io/justin-oleary)"
	@echo "  VERSION      Image tag (default: git describe)"
