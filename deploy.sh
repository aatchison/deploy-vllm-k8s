#!/bin/bash
set -euo pipefail

# Deploy or undeploy Gemma 4 models on vLLM / microk8s.
# Usage:
#   ./deploy.sh <model-size>          - deploy a model
#   ./deploy.sh undeploy              - scale down (release GPU, keep infra)
#   ./deploy.sh destroy               - delete everything including namespace/PV

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"

usage() {
    echo "Usage: $0 <command>"
    echo ""
    echo "Commands:"
    echo "  setup    - Configure MIG on both GPUs (run after first boot or reboot)"
    echo "  E2B      - Deploy Gemma 4 E2B  (~2B params,  NVFP4, ~4GB  weights)"
    echo "  E4B      - Deploy Gemma 4 E4B  (~4B params,  BF16,  ~8GB  weights)"
    echo "  26B-A4B  - Deploy Gemma 4 26B  (MoE, FP8 — see note in yaml, currently broken)"
    echo "  31B      - Deploy Gemma 4 31B  (31B params,  NVFP4, ~31GB weights)"
    echo "  dual     - Deploy E2B + E4B simultaneously on the two mig-2g.48gb slices"
    echo "             E2B: NodePort 30801  |  E4B: NodePort 30802"
    echo "  test     - Run a smoke test against the currently deployed model (single)"
    echo "  undeploy - Scale down to 0 replicas (releases GPU, model cache kept)"
    echo "  destroy  - Delete all vLLM resources including namespace and PV"
    exit 1
}

get_endpoint() {
    local node_ip
    node_ip="$(microk8s kubectl get nodes -o jsonpath='{.items[0].status.addresses[?(@.type=="InternalIP")].address}')"
    local node_port
    node_port="$(microk8s kubectl get svc vllm-svc -n vllm -o jsonpath='{.spec.ports[0].nodePort}' 2>/dev/null || echo 30800)"
    echo "http://${node_ip}:${node_port}/v1"
}

get_model_id() {
    microk8s kubectl get deployment vllm -n vllm \
        -o jsonpath='{.spec.template.spec.containers[0].args}' \
        | python3 -c 'import json,sys; args=json.load(sys.stdin); print(args[args.index("--model")+1])'
}

CMD="${1:-}"
[[ -z "$CMD" ]] && usage

if [[ "$CMD" == "setup" ]]; then
    exec bash "$SCRIPT_DIR/setup-mig.sh"
fi

if [[ "$CMD" == "test" ]]; then
    ENDPOINT="$(get_endpoint)"
    MODEL_ID="$(get_model_id)"

    echo "  Endpoint : ${ENDPOINT}"
    echo "  Model    : ${MODEL_ID}"
    echo ""
    echo "==> Running smoke test..."
    RESPONSE="$(curl -sf "${ENDPOINT}/chat/completions" \
        -H "Content-Type: application/json" \
        -d "{\"model\":\"${MODEL_ID}\",\"messages\":[{\"role\":\"user\",\"content\":\"Reply with one word: ready\"}],\"max_tokens\":10}")"

    if [[ $? -eq 0 ]]; then
        CONTENT="$(echo "$RESPONSE" | python3 -c 'import json,sys; print(json.load(sys.stdin)["choices"][0]["message"]["content"].strip())')"
        echo "  Response : ${CONTENT}"
        echo "  Status   : OK"
    else
        echo "  Status   : FAILED"
        echo "  Health   : $(curl -so /dev/null -w '%{http_code}' "${ENDPOINT%/v1}/health" 2>/dev/null || echo 'unreachable')"
        echo "  Retry    : curl ${ENDPOINT}/health"
        exit 1
    fi
    exit 0
fi

if [[ "$CMD" == "dual" ]]; then
    echo "==> Applying base infrastructure"
    microk8s kubectl apply -f "$SCRIPT_DIR/00-base.yaml"

    echo "==> Tearing down single-model deployment if present"
    microk8s kubectl delete deployment vllm -n vllm --ignore-not-found

    echo "==> Deploying E2B + E4B simultaneously"
    microk8s kubectl apply -f "$SCRIPT_DIR/deploy-dual.yaml"

    echo "==> Waiting for E2B rollout"
    microk8s kubectl rollout status deployment/vllm-e2b -n vllm --timeout=600s

    echo "==> Waiting for E4B rollout"
    microk8s kubectl rollout status deployment/vllm-e4b -n vllm --timeout=600s

    NODE_IP="$(microk8s kubectl get nodes -o jsonpath='{.items[0].status.addresses[?(@.type=="InternalIP")].address}')"
    echo ""
    echo "============================================"
    echo "  Both models ready"
    echo "  E2B : http://${NODE_IP}:30801/v1  (bg-digitalservices/Gemma-4-E2B-it-NVFP4)"
    echo "  E4B : http://${NODE_IP}:30802/v1  (google/gemma-4-E4B-it)"
    echo "============================================"
    echo ""
    echo "  Smoke test E2B:"
    curl -sf "http://${NODE_IP}:30801/v1/chat/completions" \
        -H "Content-Type: application/json" \
        -d '{"model":"bg-digitalservices/Gemma-4-E2B-it-NVFP4","messages":[{"role":"user","content":"Reply with one word: ready"}],"max_tokens":10}' \
        | python3 -c 'import json,sys; print("  E2B response:", json.load(sys.stdin)["choices"][0]["message"]["content"].strip())'
    echo "  Smoke test E4B:"
    curl -sf "http://${NODE_IP}:30802/v1/chat/completions" \
        -H "Content-Type: application/json" \
        -d '{"model":"google/gemma-4-E4B-it","messages":[{"role":"user","content":"Reply with one word: ready"}],"max_tokens":10}' \
        | python3 -c 'import json,sys; print("  E4B response:", json.load(sys.stdin)["choices"][0]["message"]["content"].strip())'
    exit 0
fi

if [[ "$CMD" == "undeploy" ]]; then
    echo "==> Scaling down vLLM deployment (GPU released, NFS cache preserved)"
    microk8s kubectl scale deployment/vllm -n vllm --replicas=0
    echo "==> Done. Redeploy with: $0 <model-size>"
    exit 0
fi

if [[ "$CMD" == "destroy" ]]; then
    echo "==> Deleting all vLLM resources"
    microk8s kubectl delete namespace vllm --ignore-not-found
    microk8s kubectl delete pv vllm-nfs-pv --ignore-not-found
    echo "==> Done. NFS model cache at 10.0.0.61:/exports/vllm-models is untouched."
    exit 0
fi

# Deploy a model
DEPLOY_FILE="$SCRIPT_DIR/deploy-gemma4-${CMD}.yaml"
if [[ ! -f "$DEPLOY_FILE" ]]; then
    echo "Error: $DEPLOY_FILE not found"
    echo "Available: $(ls "$SCRIPT_DIR"/deploy-gemma4-*.yaml 2>/dev/null | sed 's/.*deploy-gemma4-//;s/\.yaml//' | tr '\n' ' ')"
    exit 1
fi

echo "==> Applying base infrastructure"
microk8s kubectl apply -f "$SCRIPT_DIR/00-base.yaml"

echo "==> Deploying Gemma 4 $CMD"
microk8s kubectl apply -f "$DEPLOY_FILE"

echo "==> Waiting for rollout"
microk8s kubectl rollout status deployment/vllm -n vllm --timeout=1800s

ENDPOINT="$(get_endpoint)"
MODEL_ID="$(get_model_id)"

echo ""
echo "============================================"
echo "  vLLM is ready"
echo "  Endpoint : ${ENDPOINT}"
echo "  Model    : ${MODEL_ID}"
echo "============================================"
echo ""
echo "  Run a smoke test with: $0 test"
