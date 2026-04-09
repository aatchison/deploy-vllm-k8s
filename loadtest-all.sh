#!/bin/bash
PROMPT="Build a complete Rust web application using Actix-web with: a REST API with CRUD endpoints for a todo list, a SQLite database layer using sqlx, JWT-based authentication middleware, full error handling with custom error types, and a comprehensive test suite covering unit tests and integration tests. Provide all source files including Cargo.toml, src/main.rs, src/auth.rs, src/db.rs, src/models.rs, src/handlers.rs, src/errors.rs, and tests/integration_test.rs with complete implementations."
NODE_IP="192.168.122.78"
E2B_URL="http://${NODE_IP}:30801/v1/chat/completions"
E4B_URL="http://${NODE_IP}:30802/v1/chat/completions"
OLLAMA_URL="http://localhost:31434/api/generate"

VLLM_ROUNDS=${VLLM_ROUNDS:-20}
OLLAMA_ROUNDS=${OLLAMA_ROUNDS:-5}
echo "==> Firing ${VLLM_ROUNDS}x vLLM (E2B/E4B) and ${OLLAMA_ROUNDS}x ollama simultaneously"
echo "    E2B    [bg-digitalservices/Gemma-4-E2B-it-NVFP4]  -> ${E2B_URL}"
echo "    E4B    [google/gemma-4-E4B-it (BF16)]             -> ${E4B_URL}"
echo "    ollama [gemma4:31b]                                -> ${OLLAMA_URL}"
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

run_ollama() {
  local idx=$1
  local tmpf="/tmp/stream_ollama_${idx}.jsonl"
  local t0; t0=$(date +%s%3N)
  curl -sN "${OLLAMA_URL}" -H "Content-Type: application/json" \
    -d "{\"model\":\"gemma4:31b\",\"prompt\":\"${PROMPT}\",\"stream\":true,\"options\":{\"num_predict\":4096}}" \
    > "${tmpf}" &
  local cpid=$!
  local ttft=-1
  while kill -0 $cpid 2>/dev/null; do
    if [ -s "${tmpf}" ]; then
      local t1; t1=$(date +%s%3N)
      ttft=$((t1 - t0))
      break
    fi
    sleep 0.05
  done
  wait $cpid
  local t2; t2=$(date +%s%3N)
  local elapsed_ms=$((t2 - t0))
  # pass ttft and elapsed as args to python
  python3 - "$idx" "$ttft" "$elapsed_ms" <<'PYEOF'
import json, sys
idx, ttft_ms, elapsed_ms = sys.argv[1], int(sys.argv[2]), int(sys.argv[3])
tmpf = f"/tmp/stream_ollama_{idx}.jsonl"
lines = [l.strip() for l in open(tmpf) if l.strip()]
toks = 0; chars = 0; tps = 0.0
for l in lines:
    try:
        d = json.loads(l)
        chars += len(d.get('response', ''))
        if not d.get('done'):
            toks += 1
        if d.get('done'):
            ec = d.get('eval_count', 0)
            ed = d.get('eval_duration', 0)
            if ed > 0:
                tps = ec / (ed / 1e9)
    except:
        pass
print(f"ollama[{idx}]: model=gemma4:31b                                      ttft={ttft_ms:>5}ms  total={elapsed_ms:>6}ms  tokens={toks:>4}  tps={tps:>5.1f} tok/s  chars={chars}")
PYEOF
}

pids=()
for i in $(seq 1 $VLLM_ROUNDS); do
  run_vllm $i "$E2B_URL" "bg-digitalservices/Gemma-4-E2B-it-NVFP4" "e2b" "E2B " &
  pids+=($!)
  run_vllm $i "$E4B_URL" "google/gemma-4-E4B-it" "e4b" "E4B " &
  pids+=($!)
done
for i in $(seq 1 $OLLAMA_ROUNDS); do
  run_ollama $i &
  pids+=($!)
done

echo "Waiting for ${#pids[@]} requests..."
start=$SECONDS
for pid in "${pids[@]}"; do wait "$pid"; done
elapsed=$((SECONDS - start))
echo ""
echo "=== Done in ${elapsed}s ==="
