#!/usr/bin/env bash
# benchmark.sh — run the Rust-app prompt against all live VLLMInstances
# and report TTFT, total latency, tokens, and tok/s per instance.
#
# Usage:
#   bash hack/benchmark.sh [namespace] [kubectl] [rounds]
#
# Examples:
#   bash hack/benchmark.sh                                  # defaults
#   bash hack/benchmark.sh vllm "microk8s kubectl" 3       # 3 rounds each
set -uo pipefail

NAMESPACE="${1:-vllm}"
KUBECTL="${2:-kubectl}"
ROUNDS="${3:-5}"
read -ra KCL <<< "$KUBECTL"

PROMPT="Build a complete Rust web application using Actix-web with: a REST API with CRUD endpoints for a todo list, a SQLite database layer using sqlx, JWT-based authentication middleware, full error handling with custom error types, and a comprehensive test suite covering unit tests and integration tests. Provide all source files including Cargo.toml, src/main.rs, src/auth.rs, src/db.rs, src/models.rs, src/handlers.rs, src/errors.rs, and tests/integration_test.rs with complete implementations."

# ── Discover all Ready instances ─────────────────────────────────────────────
NAMES=()
ENDPOINTS=()
MODELS=()

while IFS=$'\t' read -r name endpoint; do
    [[ -z "$endpoint" ]] && continue
    model=$(curl -sf --max-time 5 "${endpoint}/models" \
        | grep -o '"id":"[^"]*"' | head -1 | cut -d'"' -f4)
    [[ -z "$model" ]] && continue
    NAMES+=("$name")
    ENDPOINTS+=("$endpoint")
    MODELS+=("$model")
done < <("${KCL[@]}" get vllminstance -n "$NAMESPACE" \
    -o jsonpath='{range .items[*]}{.metadata.name}{"\t"}{.status.endpoint}{"\n"}{end}' 2>/dev/null)

if [[ ${#NAMES[@]} -eq 0 ]]; then
    echo "ERROR: no Ready VLLMInstances with endpoints found in namespace '$NAMESPACE'" >&2
    exit 1
fi

echo "============================================================"
echo " Benchmark: Rust app prompt  |  ${ROUNDS} rounds/instance"
echo " MIG config: $(${KCL[@]} get node -o jsonpath='{.items[0].metadata.labels.nvidia\.com/mig\.config}' 2>/dev/null)"
echo " Instances  : ${#NAMES[@]}"
for i in "${!NAMES[@]}"; do
    printf "   %-16s  %s\n" "${NAMES[$i]}" "${MODELS[$i]}"
done
echo "============================================================"
echo ""

# ── Per-request benchmark ─────────────────────────────────────────────────────
run_once() {
    local label="$1" url="$2" model="$3" idx="$4"
    local tmpf; tmpf=$(mktemp /tmp/bench_XXXXXX.jsonl)
    local t0; t0=$(date +%s%3N)
    curl -sN "${url}/chat/completions" \
        -H "Content-Type: application/json" \
        -d "{\"model\":\"${model}\",\"messages\":[{\"role\":\"user\",\"content\":\"${PROMPT}\"}],\"max_tokens\":4096,\"stream\":true}" \
        > "$tmpf" &
    local cpid=$!
    local ttft=-1
    while kill -0 $cpid 2>/dev/null; do
        if grep -qm1 '"choices"' "$tmpf" 2>/dev/null; then
            ttft=$(( $(date +%s%3N) - t0 ))
            break
        fi
        sleep 0.05
    done
    wait $cpid
    local elapsed=$(( $(date +%s%3N) - t0 ))
    local toks; toks=$(grep -c '^data: {' "$tmpf" 2>/dev/null || echo 0)
    local tps=0
    [[ $elapsed -gt 0 ]] && tps=$(awk "BEGIN{printf \"%.1f\", ${toks}*1000/${elapsed}}")
    rm -f "$tmpf"
    printf "  %-14s [%d]  ttft=%5dms  total=%6dms  tokens=%4d  tps=%5s tok/s\n" \
        "$label" "$idx" "$ttft" "$elapsed" "$toks" "$tps"
}

# ── Fire all rounds concurrently ──────────────────────────────────────────────
pids=()
start=$SECONDS
for i in $(seq 1 "$ROUNDS"); do
    for j in "${!NAMES[@]}"; do
        run_once "${NAMES[$j]}" "${ENDPOINTS[$j]}" "${MODELS[$j]}" "$i" &
        pids+=($!)
    done
done

total_reqs=${#pids[@]}
echo "Firing ${total_reqs} concurrent requests (${ROUNDS} rounds × ${#NAMES[@]} instances)..."
echo ""
for pid in "${pids[@]}"; do wait "$pid"; done
elapsed=$(( SECONDS - start ))

echo ""
echo "============================================================"
echo " Total wall time: ${elapsed}s  |  Requests: ${total_reqs}"
echo "============================================================"
