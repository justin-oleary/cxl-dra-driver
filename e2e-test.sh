#!/usr/bin/env bash
set -euo pipefail

cd "$(dirname "$0")"

echo "=== CXL DRA Driver E2E Test ==="

cleanup() {
    echo ""
    echo "=== Cleanup ==="
    if [[ -n "${PF_BUILD:-}" ]]; then
        kill "$PF_BUILD" 2>/dev/null || true
    fi
    kubectl delete -f deploy/test-workload.yaml --ignore-not-found 2>/dev/null || true
}
trap cleanup EXIT

# step 1: build images with in-cluster buildkit
echo ""
echo "=== Step 1: Building images with in-cluster BuildKit ==="

./build-in-cluster.sh

# step 2: apply policy exception for DRA driver
echo ""
echo "=== Step 2: Applying Kyverno PolicyException ==="
kubectl apply -f deploy/kubernetes/policy-exception.yaml || {
    echo "Warning: PolicyException failed (Kyverno may not be installed)"
}

# step 3: deploy mock switch (in-cluster)
echo ""
echo "=== Step 3: Deploying Mock CXL Switch (in-cluster) ==="
kubectl apply -f deploy/kubernetes/rbac.yaml
kubectl apply -f deploy/kubernetes/mock-switch.yaml
kubectl -n cxl-system wait --for=condition=available deployment/cxl-mock-switch --timeout=60s
echo "Mock switch deployed"

# step 4: deploy DRA driver
echo ""
echo "=== Step 4: Deploying DRA Driver ==="
kubectl apply -f deploy/kubernetes/deviceclass.yaml
kubectl apply -f deploy/kubernetes/controller.yaml
kubectl apply -f deploy/kubernetes/daemonset.yaml
kubectl apply -f deploy/kubernetes/resourceslice.yaml

# step 5: wait for pods
echo ""
echo "=== Step 5: Waiting for DRA pods ==="
echo "Waiting for controller..."
kubectl -n cxl-system wait --for=condition=available deployment/cxl-dra-controller --timeout=60s || {
    echo "Controller deployment failed:"
    kubectl -n cxl-system describe deployment cxl-dra-controller
    kubectl -n cxl-system get pods -l app=cxl-dra-controller -o wide
    kubectl -n cxl-system logs -l app=cxl-dra-controller --tail=50 2>/dev/null || true
    exit 1
}

echo "Waiting for node plugin daemonset..."
kubectl -n cxl-system rollout status daemonset/cxl-node-plugin --timeout=60s || {
    echo "DaemonSet rollout failed:"
    kubectl -n cxl-system describe daemonset cxl-node-plugin
    kubectl -n cxl-system get pods -l app=cxl-node-plugin -o wide
    exit 1
}
echo "DRA driver pods ready"

# step 6: deploy test workload
echo ""
echo "=== Step 6: Deploying test workload ==="
kubectl apply -f deploy/test-workload.yaml

# step 7: wait for test pod
echo ""
echo "=== Step 7: Waiting for test pod (up to 60s) ==="
for i in {1..60}; do
    phase=$(kubectl -n cxl-system get pod cxl-test-pod -o jsonpath='{.status.phase}' 2>/dev/null || echo "Pending")
    echo "  Attempt $i: Pod phase = $phase"
    if [[ "$phase" == "Running" ]]; then
        echo "Test pod is Running!"
        break
    fi
    if [[ "$phase" == "Failed" ]]; then
        echo "ERROR: Test pod failed"
        kubectl -n cxl-system describe pod cxl-test-pod
        exit 1
    fi
    sleep 1
done

final_phase=$(kubectl -n cxl-system get pod cxl-test-pod -o jsonpath='{.status.phase}' 2>/dev/null || echo "Unknown")
if [[ "$final_phase" != "Running" ]]; then
    echo "ERROR: Test pod did not reach Running state"
    kubectl -n cxl-system describe pod cxl-test-pod
    kubectl -n cxl-system get resourceclaims -o wide
    exit 1
fi

# step 8: dump logs
echo ""
echo "=== Step 8: Mock CXL Switch Logs ==="
kubectl -n cxl-system logs -l app=cxl-mock-switch --tail=30

echo ""
echo "=== Step 8: Controller Logs ==="
kubectl -n cxl-system logs -l app=cxl-dra-controller --tail=30

echo ""
echo "=== Step 8: Node Plugin Logs ==="
kubectl -n cxl-system logs -l app=cxl-node-plugin --tail=30

echo ""
echo "=== E2E TEST PASSED ==="
echo "CXL memory allocation flow verified end-to-end"
