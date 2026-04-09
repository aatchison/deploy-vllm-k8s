#!/bin/bash
# Load test: fire concurrent requests at all three vLLM endpoints simultaneously.
# Measures time-to-first-token (TTFT), tokens/sec, and total latency per request.
#
# Usage:
#   bash loadtest-all.sh                                   # defaults
#   VLLM_ROUNDS=10 B31_ROUNDS=3 bash loadtest-all.sh       # custom counts
#
# The node IP is auto-detected from kubectl. Override with NODE_IP env var.
# Requires triple deployment: ./deploy.sh triple

set -uo pipefail

PROMPT="Build a complete Rust web application using Actix-web with: a REST API with CRUD endpoints for a todo list, a SQLite database layer using sqlx, JWT-based authentication middleware, full error handling with custom error types, and a comprehensive test suite covering unit tests and integration tests. Provide all source files including Cargo.toml, src/main.rs, src/auth.rs, src/db.rs, src/models.rs, src/handlers.rs, src/errors.rs, and tests/integration_test.rs with complete implementations."

# Auto-detect node IP from kubectl, or use NODE_IP env var
NODE_IP="${NODE_IP:-$(microk8s kubectl get nodes -o jsonpath='{.items[0].status.addresses[?(@.type=="InternalIP")].address}' 2>/dev/null)}"
if [ -z "$NODE_IP" ]; then
    echo "ERROR: Could not detect node IP. Set NODE_IP env var or check kubectl." >&2
    exit 1
fi

E2B_URL="http://${NODE_IP}:30801/v1/chat/completions"
E4B_URL="http://${NODE_IP}:30802/v1/chat/completions"
B31_URL="http://${NODE_IP}:30803/v1/chat/completions"

VLLM_ROUNDS=${VLLM_ROUNDS:-20}
B31_ROUNDS=${B31_ROUNDS:-5}
echo "==> Firing ${VLLM_ROUNDS}x vLLM (E2B/E4B) and ${B31_ROUNDS}x 31B simultaneously"
echo "    E2B [bg-digitalservices/Gemma-4-E2B-it-NVFP4]  -> ${E2B_URL}"
echo "    E4B [google/gemma-4-E4B-it (BF16)]             -> ${E4B_URL}"
echo "    31B [nvidia/Gemma-4-31B-IT-NVFP4]              -> ${B31_URL}"
echo ""

run_vllm() {
  local idx=$1 url=$2 model=$3 tag=$4 label=$5
  local tmpf="/tmp/stream_${tag}_${idx}.jsonl"
  local t0; t0=$(date +%s%3N)
  curl -sN "${url}" -H "Content-Type: application/json" \
    -d "{\"model\":\"${model}\",\"messages\":[{\"role\":\"user\",\"content\":\"${PROMPT}\"}],\"max_tokens\":4096,\"stream\":true}" \
    > "${tmpf}" &
  local cpid=$!
  local ttft=-1
  while kill -0 $cpid 2>/dev/null; do
    if grep -qm1 'choices' "${tmpf}" 2>/dev/null; then
      local t1; t1=$(date +%s%3N)
      ttft=$((t1 - t0))
      break
    fi
    sleep 0.05
  done
  wait $cpid
  local t2; t2=$(date +%s%3N)
  local elapsed_ms=$((t2 - t0))
  local toks chars tps
  toks=$(grep -c '^data: {' "${tmpf}" 2>/dev/null || echo 0)
  chars=$(grep '^data: {' "${tmpf}" | python3 -c "
import sys,json
c=0
for line in sys.stdin:
    try: c+=len(json.loads(line[6:])['choices'][0]['delta'].get('content',''))
    except: pass
print(c)
" 2>/dev/null || echo 0)
  [ "$elapsed_ms" -gt 0 ] && tps=$(echo "scale=1; ${toks}*1000/${elapsed_ms}" | bc 2>/dev/null) || tps=0
  printf "${label}[%d]: model=%-46s  ttft=%5dms  total=%6dms  tokens=%4d  tps=%5s tok/s  chars=%d\n" \
    "${idx}" "${model}" "${ttft}" "${elapsed_ms}" "${toks}" "${tps}" "${chars}"
}

pids=()
for i in $(seq 1 $VLLM_ROUNDS); do
  run_vllm $i "$E2B_URL" "bg-digitalservices/Gemma-4-E2B-it-NVFP4" "e2b" "E2B " &
  pids+=($!)
  run_vllm $i "$E4B_URL" "google/gemma-4-E4B-it" "e4b" "E4B " &
  pids+=($!)
done
for i in $(seq 1 $B31_ROUNDS); do
  run_vllm $i "$B31_URL" "nvidia/Gemma-4-31B-IT-NVFP4" "31b" "31B " &
  pids+=($!)
done

echo "Waiting for ${#pids[@]} requests..."
start=$SECONDS
for pid in "${pids[@]}"; do wait "$pid"; done
elapsed=$((SECONDS - start))
echo ""
echo "=== Done in ${elapsed}s ==="
