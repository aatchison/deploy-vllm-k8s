#!/bin/bash
# Configure MIG on both GPUs and verify allocatable resources.
#
# GPU 0: 2x mig-2g.48gb  (managed by mig-manager via custom-mig ConfigMap)
# GPU 1: 1x mig-4g.96gb  (enabled manually; also in custom-mig ConfigMap)
#
# Run this after first boot or if MIG config is lost.
# Safe to re-run — mig-manager skips GPUs already in the desired state.

set -euo pipefail

NAMESPACE=gpu-operator-resources
NODE=$(microk8s kubectl get nodes -o jsonpath='{.items[0].metadata.name}')
echo "  Node: $NODE"

echo ""
echo "==> Step 1: Enable MIG mode on GPU 1 (if not already)"
sudo nvidia-smi -i 1 -mig 1 || true

echo ""
echo "==> Step 2: Apply custom-mig label to trigger mig-manager for GPU 0"
microk8s kubectl label node "$NODE" nvidia.com/mig.config=custom-mig --overwrite

echo ""
echo "==> Step 3: Wait for mig-manager to finish configuring GPU 0"
for i in $(seq 1 24); do
    STATE=$(microk8s kubectl get node "$NODE" \
        -o jsonpath='{.metadata.labels.nvidia\.com/mig\.config\.state}' 2>/dev/null || echo "")
    echo "  $(date +%H:%M:%S) mig.config.state=$STATE"
    [ "$STATE" = "success" ] && break
    sleep 5
done

echo ""
echo "==> Step 4: Create 4g.96gb MIG instance on GPU 1 (if not present)"
EXISTING=$(sudo nvidia-smi mig -lgi 2>/dev/null | grep "4g.96gb" || true)
if [ -z "$EXISTING" ]; then
    sudo nvidia-smi mig -cgi 4g.96gb -i 1
    sudo nvidia-smi mig -cci -gi 0 -i 1
    echo "  Created 4g.96gb instance on GPU 1"
else
    echo "  4g.96gb instance already present, skipping"
fi

echo ""
echo "==> Step 5: Restart device plugin so it discovers all MIG instances"
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
echo "  nvidia.com/mig-2g.48gb = 2   (GPU 0)"
echo "  nvidia.com/mig-4g.96gb = 1   (GPU 1)"
