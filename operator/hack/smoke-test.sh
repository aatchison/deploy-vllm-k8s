#!/usr/bin/env bash
# Usage: smoke-test.sh <instance-name> [namespace] [kubectl-binary]
#
# Looks up <instance-name> as a VLLMInstance first, then falls back to
# LongContextInstance. Both CRDs expose the same .status.endpoint field, so the
# script is kind-agnostic past the lookup. Exits 0 on success, non-zero on any
# failure.
set -euo pipefail

INSTANCE="${1:?Usage: $0 <instance-name> [namespace] [kubectl]}"
NAMESPACE="${2:-vllm}"
# Split KUBECTL into an array so "microk8s kubectl" works as well as plain "kubectl".
read -ra KUBECTL <<< "${3:-kubectl}"

# ── 1. Resolve endpoint from VLLMInstance or LongContextInstance status ──────
# Try VLLMInstance first; fall back to LongContextInstance. Both CRDs expose
# the same `.status.endpoint` field, so the rest of the script is kind-agnostic.
KIND=""
ENDPOINT=""
for kind in vllminstance longcontextinstance; do
    if "${KUBECTL[@]}" get "$kind" "$INSTANCE" -n "$NAMESPACE" >/dev/null 2>&1; then
        KIND="$kind"
        echo "==> Fetching endpoint for ${kind^} '$INSTANCE' in namespace '$NAMESPACE'..."
        ENDPOINT=$("${KUBECTL[@]}" get "$kind" "$INSTANCE" -n "$NAMESPACE" \
            -o jsonpath='{.status.endpoint}' 2>/dev/null)
        break
    fi
done

if [[ -z "$KIND" ]]; then
    echo "ERROR: no VLLMInstance or LongContextInstance named '$INSTANCE' in namespace '$NAMESPACE'" >&2
    exit 1
fi

if [[ -z "$ENDPOINT" ]]; then
    echo "ERROR: status.endpoint is empty on $KIND/$INSTANCE — instance not Ready yet?" >&2
    "${KUBECTL[@]}" get "$KIND" "$INSTANCE" -n "$NAMESPACE" \
        -o jsonpath='{range .status.conditions[*]}{.type}={.status}: {.message}{"\n"}{end}' 2>/dev/null || true
    exit 1
fi
echo "    endpoint: $ENDPOINT"

# ── 2. Models list ────────────────────────────────────────────────────────────
echo "==> GET $ENDPOINT/models"
MODELS=$(curl -sf --max-time 10 "$ENDPOINT/models")
MODEL_ID=$(echo "$MODELS" | grep -o '"id":"[^"]*"' | head -1 | cut -d'"' -f4)
if [[ -z "$MODEL_ID" ]]; then
    echo "ERROR: /models returned no model IDs" >&2
    echo "$MODELS" >&2
    exit 1
fi
echo "    model: $MODEL_ID"

# ── 3. Minimal chat completion ────────────────────────────────────────────────
echo "==> POST $ENDPOINT/chat/completions (max_tokens=5)"
RESPONSE=$(curl -sf --max-time 30 \
    -H "Content-Type: application/json" \
    -d "{\"model\":\"$MODEL_ID\",\"messages\":[{\"role\":\"user\",\"content\":\"Say hi\"}],\"max_tokens\":5}" \
    "$ENDPOINT/chat/completions")

CONTENT=$(echo "$RESPONSE" | grep -o '"content":"[^"]*"' | head -1 | cut -d'"' -f4)
if [[ -z "$CONTENT" ]]; then
    echo "ERROR: chat completion returned no content" >&2
    echo "$RESPONSE" >&2
    exit 1
fi
echo "    response: $CONTENT"
echo "==> PASS"
