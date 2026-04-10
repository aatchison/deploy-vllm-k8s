#!/bin/bash
# Configure MIG on both GPUs and verify allocatable resources.
#
# Both GPUs are managed by mig-manager via the custom-mig ConfigMap:
#   GPU 0: 1x mig-4g.96gb
#   GPU 1: 1x mig-4g.96gb
#
# Run this after first boot or if MIG config is lost.
# Safe to re-run — mig-manager skips GPUs already in the desired state.

set -euo pipefail

NAMESPACE=gpu-operator-resources
NODE=$(microk8s kubectl get nodes -o jsonpath='{.items[0].metadata.name}')
echo "  Node: $NODE"

echo ""
echo "==> Step 1: Apply custom-mig label to trigger mig-manager for both GPUs"
microk8s kubectl label node "$NODE" nvidia.com/mig.config=custom-mig --overwrite

echo ""
echo "==> Step 2: Wait for mig-manager to finish configuring both GPUs"
for i in $(seq 1 48); do
    STATE=$(microk8s kubectl get node "$NODE" \
        -o jsonpath='{.metadata.labels.nvidia\.com/mig\.config\.state}' 2>/dev/null || echo "")
    echo "  $(date +%H:%M:%S) mig.config.state=$STATE"
    [ "$STATE" = "success" ] && break
    sleep 5
done

echo ""
echo "==> Step 3: Restart device plugin so it discovers all MIG instances"
POD=$(microk8s kubectl -n $NAMESPACE get pod \
    -l app=nvidia-device-plugin-daemonset \
    -o jsonpath='{.items[0].metadata.name}')
microk8s kubectl -n $NAMESPACE delete pod "$POD"
echo "  Deleted $POD, waiting for replacement..."

for i in $(seq 1 24); do
    READY=$(microk8s kubectl -n $NAMESPACE get pod \
        -l app=nvidia-device-plugin-daemonset \
        -o jsonpath='{.items[0].status.containerStatuses[*].ready}' 2>/dev/null || echo "")
    echo "  $(date +%H:%M:%S) ready=$READY"
    [ "$READY" = "true true" ] && break
    sleep 5
done

echo ""
echo "==> Verification"
nvidia-smi -L
echo ""
microk8s kubectl get node "$NODE" -o json \
    | python3 -c "
import json, sys
node = json.load(sys.stdin)
alloc = node['status']['allocatable']
for k, v in sorted(alloc.items()):
    if 'nvidia' in k:
        print(f'  {k} = {v}')
"
echo ""
echo "Done. Expected:"
echo "  nvidia.com/mig-4g.96gb = 2   (GPU 0 + GPU 1)"
