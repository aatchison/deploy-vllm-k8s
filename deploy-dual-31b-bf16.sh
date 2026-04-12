#!/bin/bash
# Deploy Gemma 4 31B BF16 on both mig-4g.96gb slices simultaneously.
# Requires MIG layout: both GPUs as 1x 4g.96gb (run deploy.sh setup first if needed).
#
#   31B-A: http://<node>:30801/v1  (google/gemma-4-31B-it, BF16, 65K ctx)
#   31B-B: http://<node>:30802/v1  (google/gemma-4-31B-it, BF16, 65K ctx)
#
# Usage: ./deploy-dual-31b-bf16.sh [undeploy]
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"

if [[ "${1:-}" == "undeploy" ]]; then
    echo "==> Scaling down dual-31b-bf16 deployments"
    microk8s kubectl scale deployment/vllm-31b-a deployment/vllm-31b-b -n vllm --replicas=0
    echo "==> Done. Redeploy with: $0"
    exit 0
fi

echo "==> Applying base infrastructure"
microk8s kubectl apply -f "$SCRIPT_DIR/00-base.yaml"

echo "==> Tearing down any conflicting deployments and services"
microk8s kubectl delete deployment vllm vllm-moe vllm-31b -n vllm --ignore-not-found
microk8s kubectl delete svc vllm-moe-svc vllm-31b-svc vllm-31b-a-svc vllm-31b-b-svc -n vllm --ignore-not-found

echo "==> Deploying 31B BF16 on both 4g.96gb slices"
microk8s kubectl apply -f "$SCRIPT_DIR/deploy-dual-31b.yaml"

echo "==> Waiting for 31B-A rollout (up to 30 min)"
microk8s kubectl rollout status deployment/vllm-31b-a -n vllm --timeout=1800s

echo "==> Waiting for 31B-B rollout (up to 30 min)"
microk8s kubectl rollout status deployment/vllm-31b-b -n vllm --timeout=1800s

NODE_IP="$(microk8s kubectl get nodes -o jsonpath='{.items[0].status.addresses[?(@.type=="InternalIP")].address}')"
echo ""
echo "============================================"
echo "  Both slices ready"
echo "  31B-A : http://${NODE_IP}:30801/v1  (google/gemma-4-31B-it BF16)"
echo "  31B-B : http://${NODE_IP}:30802/v1  (google/gemma-4-31B-it BF16)"
echo "============================================"

echo ""
echo "  Smoke test 31B-A:"
curl -sf "http://${NODE_IP}:30801/v1/chat/completions" \
    -H "Content-Type: application/json" \
    -d '{"model":"gemma-4-31b-bf16","messages":[{"role":"user","content":"Reply with one word: ready"}],"max_tokens":10}' \
    | python3 -c 'import json,sys; print("  31B-A response:", json.load(sys.stdin)["choices"][0]["message"]["content"].strip())'

echo "  Smoke test 31B-B:"
curl -sf "http://${NODE_IP}:30802/v1/chat/completions" \
    -H "Content-Type: application/json" \
    -d '{"model":"gemma-4-31b-bf16","messages":[{"role":"user","content":"Reply with one word: ready"}],"max_tokens":10}' \
    | python3 -c 'import json,sys; print("  31B-B response:", json.load(sys.stdin)["choices"][0]["message"]["content"].strip())'
