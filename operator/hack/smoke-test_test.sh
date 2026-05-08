#!/usr/bin/env bash
# Regression test for smoke-test.sh kind-detection (issue #45).
#
# Exercises the lookup-fallback branch by injecting a fake `kubectl` that
# returns success only for `longcontextinstance/<name>`. Asserts that
# smoke-test.sh selects the LongContextInstance instead of failing on a
# missing VLLMInstance. We stop the script before the curl step (no live
# endpoint needed) by having the fake kubectl emit a recognizable URL and
# then short-circuiting on the curl exit.
#
# Usage: bash operator/hack/smoke-test_test.sh
# Exits 0 on success.

set -euo pipefail

THIS_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SMOKE="$THIS_DIR/smoke-test.sh"
TMPDIR="$(mktemp -d)"
trap 'rm -rf "$TMPDIR"' EXIT

# ── Test 1: VLLMInstance exists → script selects it. ─────────────────────────
cat >"$TMPDIR/kubectl_vllm" <<'EOF'
#!/usr/bin/env bash
# Fake kubectl: VLLMInstance exists, returns endpoint http://example/v1.
case "$*" in
    "get vllminstance e2b -n vllm")
        echo "vllminstance.vllm.aatchison.io/e2b"; exit 0 ;;
    "get vllminstance e2b -n vllm -o jsonpath={.status.endpoint}")
        printf "%s" "http://127.0.0.1:1/v1"; exit 0 ;;
    "get longcontextinstance"*)
        echo "Error from server (NotFound)" >&2; exit 1 ;;
esac
echo "unexpected fake-kubectl call: $*" >&2; exit 99
EOF
chmod +x "$TMPDIR/kubectl_vllm"

OUT="$("$SMOKE" e2b vllm "$TMPDIR/kubectl_vllm" 2>&1 || true)"
if ! grep -q "Fetching endpoint for Vllminstance" <<<"$OUT"; then
    echo "FAIL test 1: did not select VLLMInstance branch" >&2
    echo "$OUT" >&2
    exit 1
fi
echo "PASS test 1: VLLMInstance found and selected"

# ── Test 2: VLLMInstance missing, LongContextInstance present → fallback. ────
cat >"$TMPDIR/kubectl_longctx" <<'EOF'
#!/usr/bin/env bash
case "$*" in
    "get vllminstance "*)
        echo "Error from server (NotFound)" >&2; exit 1 ;;
    "get longcontextinstance 31b-longctx -n vllm")
        echo "longcontextinstance.vllm.aatchison.io/31b-longctx"; exit 0 ;;
    "get longcontextinstance 31b-longctx -n vllm -o jsonpath={.status.endpoint}")
        printf "%s" "http://127.0.0.1:1/v1"; exit 0 ;;
esac
echo "unexpected fake-kubectl call: $*" >&2; exit 99
EOF
chmod +x "$TMPDIR/kubectl_longctx"

OUT="$("$SMOKE" 31b-longctx vllm "$TMPDIR/kubectl_longctx" 2>&1 || true)"
if ! grep -q "Fetching endpoint for Longcontextinstance" <<<"$OUT"; then
    echo "FAIL test 2: did not fall back to LongContextInstance branch" >&2
    echo "$OUT" >&2
    exit 1
fi
echo "PASS test 2: LongContextInstance fallback worked"

# ── Test 3: neither kind exists → fail with clear error. ─────────────────────
cat >"$TMPDIR/kubectl_none" <<'EOF'
#!/usr/bin/env bash
echo "Error from server (NotFound)" >&2
exit 1
EOF
chmod +x "$TMPDIR/kubectl_none"

set +e
OUT="$("$SMOKE" missing vllm "$TMPDIR/kubectl_none" 2>&1)"
RC=$?
set -e
if [[ $RC -eq 0 ]]; then
    echo "FAIL test 3: expected non-zero exit when neither kind exists" >&2
    echo "$OUT" >&2
    exit 1
fi
if ! grep -q "no VLLMInstance or LongContextInstance" <<<"$OUT"; then
    echo "FAIL test 3: error message did not mention both kinds" >&2
    echo "$OUT" >&2
    exit 1
fi
echo "PASS test 3: missing-instance error mentions both kinds"

echo "ALL PASS"
