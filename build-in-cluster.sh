#!/usr/bin/env bash
set -euo pipefail

cd "$(dirname "$0")"

# Registry accessible via NodePort from both BuildKit (cluster internal) and kubelet (localhost)
REGISTRY_INTERNAL="registry.registry.svc.cluster.local:5000"
REGISTRY_NODEPORT="localhost:30500"

echo "=== CXL DRA Driver In-Cluster Build ==="

# check for buildctl
if ! command -v buildctl &> /dev/null; then
    echo "Installing buildctl..."
    if [[ "$(uname)" == "Darwin" ]]; then
        brew install buildkit
    else
        echo "Please install buildctl: https://github.com/moby/buildkit/releases"
        exit 1
    fi
fi

# deploy registry if not exists or update to NodePort
echo ""
echo "=== Deploying in-cluster registry (NodePort 30500) ==="
kubectl apply -f deploy/buildkit/registry.yaml
kubectl -n registry wait --for=condition=available deployment/registry --timeout=60s

# deploy/update buildkit
echo ""
echo "=== Deploying BuildKit ==="
kubectl apply -f deploy/buildkit/buildkit.yaml

# wait for buildkit with retry
for i in {1..3}; do
    if kubectl -n buildkit wait --for=condition=available deployment/buildkitd --timeout=60s 2>/dev/null; then
        break
    fi
    echo "Retrying buildkit deployment..."
    kubectl -n buildkit rollout restart deployment/buildkitd
    sleep 5
done

# cleanup existing port-forwards
pkill -f "port-forward.*svc/buildkitd" 2>/dev/null || true
sleep 1

echo ""
echo "=== Starting port-forward to BuildKit ==="
kubectl -n buildkit port-forward svc/buildkitd 1234:1234 &
PF_BUILD=$!

cleanup() {
    echo "Stopping port-forward..."
    kill $PF_BUILD 2>/dev/null || true
}
trap cleanup EXIT

sleep 3

export BUILDKIT_HOST=tcp://localhost:1234

echo ""
echo "=== Building cxl-dra-controller ==="
buildctl build \
    --frontend dockerfile.v0 \
    --local context=. \
    --local dockerfile=. \
    --opt filename=Dockerfile.controller \
    --output type=image,name=${REGISTRY_INTERNAL}/cxl-dra-controller:latest,push=true

echo ""
echo "=== Building cxl-node-plugin ==="
buildctl build \
    --frontend dockerfile.v0 \
    --local context=. \
    --local dockerfile=. \
    --opt filename=Dockerfile.node-plugin \
    --output type=image,name=${REGISTRY_INTERNAL}/cxl-node-plugin:latest,push=true

echo ""
echo "=== Building cxl-mock-switch ==="
buildctl build \
    --frontend dockerfile.v0 \
    --local context=. \
    --local dockerfile=. \
    --opt filename=Dockerfile.mock-switch \
    --output type=image,name=${REGISTRY_INTERNAL}/cxl-mock-switch:latest,push=true

echo ""
echo "=== Images pushed to in-cluster registry ==="
echo "  BuildKit pushes to: ${REGISTRY_INTERNAL}"
echo "  Kubelet pulls from: ${REGISTRY_NODEPORT}"
echo ""
echo "=== Build Complete ==="
echo ""
echo "Deploy with:"
echo "  kubectl apply -f deploy/kubernetes/"
