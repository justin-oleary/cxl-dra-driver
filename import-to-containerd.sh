#!/usr/bin/env bash
set -euo pipefail

cd "$(dirname "$0")"

echo "=== Import Images to K3s/containerd ==="

# save images from docker
echo "Saving images from Docker..."
docker save cxl-dra-controller:latest cxl-node-plugin:latest -o /tmp/cxl-images.tar

# get all nodes
nodes=$(kubectl get nodes -o jsonpath='{.items[*].metadata.name}')

for node in $nodes; do
    echo ""
    echo "=== Importing to node: $node ==="

    # copy tarball to node (assumes ssh access or use kubectl cp with a debug pod)
    # for K3s, images go to k3s containerd

    # Option 1: If you have SSH access
    # scp /tmp/cxl-images.tar $node:/tmp/
    # ssh $node "sudo ctr -n k8s.io images import /tmp/cxl-images.tar"

    # Option 2: Use a privileged pod to import
    kubectl run import-$node \
        --image=alpine \
        --restart=Never \
        --overrides='{
            "spec": {
                "nodeName": "'$node'",
                "hostPID": true,
                "containers": [{
                    "name": "import",
                    "image": "alpine",
                    "command": ["sleep", "300"],
                    "securityContext": {"privileged": true},
                    "volumeMounts": [{
                        "name": "containerd",
                        "mountPath": "/run/containerd"
                    }]
                }],
                "volumes": [{
                    "name": "containerd",
                    "hostPath": {"path": "/run/containerd"}
                }]
            }
        }' \
        --rm -it -- sh -c '
            apk add --no-cache curl
            # download ctr from buildkit or use host ctr
            echo "Images would be imported here with ctr"
        ' || true
done

echo ""
echo "For manual import on each node, run:"
echo "  sudo ctr -n k8s.io images import /tmp/cxl-images.tar"

rm -f /tmp/cxl-images.tar
