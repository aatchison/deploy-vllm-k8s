# Base image: vLLM nightly with OpenAI-compatible API server.
#
# Pinned to a digest, NOT the floating `:nightly` tag. `:nightly` is rebuilt
# daily; pinning makes the serving image reproducible-from-git and — critically
# — keeps the Anthropic system-hoist patch deterministic. apply_patch.py anchors
# on specific lines in vLLM's anthropic/protocol.py and fails the build loudly
# if they move; a floating base would let upstream drift silently shift those
# anchors. Bump this digest deliberately (and re-verify the patch) when adopting
# a newer base.
#
# This digest resolves to vLLM 0.21.1rc1.dev417+g22a58640b (commit 22a58640)
# and matches the digest currently running on the node.
FROM vllm/vllm-openai:nightly@sha256:4cebac8c03f2cd9f5fabe72ac7c2a0b3aaa8450ef8f0e47429425fd1bfb83d42

# --- Issue #103: passwd entry for the running uid ---
# Pods run as uid 1000 under the operator's runAsNonRoot security context
# (issue #37). A recent torch upgrade now calls getpass.getuser() at module
# import time (torch._dynamo.package), which falls back to
# pwd.getpwuid(getuid()) and raises KeyError when the uid has no /etc/passwd
# entry. The base image still ships no uid-1000 entry, so this remains
# required. The operator also sets TORCHINDUCTOR_CACHE_DIR + HOME as a second
# layer of defense.
RUN echo 'vllm:x:1000:1000::/home/vllm:/usr/sbin/nologin' >> /etc/passwd \
 && mkdir -p /home/vllm && chown 1000:1000 /home/vllm

# --- Anthropic /v1/messages: accept system-role entries in messages[] ---
# Claude Code talks to the Anthropic Messages API and emits the system prompt
# as a role="system" entry INSIDE messages[]. vLLM's native Anthropic endpoint
# validates messages[] against Literal["user","assistant"] and expects system
# as a top-level field, so it rejects this with HTTP 400 and Claude Code cannot
# complete a turn. This layer bakes a mode="before" model validator into
# AnthropicMessagesRequest that hoists role="system" entries into the top-level
# `system` field before validation. Idempotent; fails the build loudly if the
# upstream protocol changes. See patches/ for the logic + unit tests.
COPY patches/ /opt/patches/
# The base image ships python3 only (no `python` on PATH), so invoke python3.
RUN python3 /opt/patches/apply_patch.py

# --- cohere_melody: Cohere Command tool-call + reasoning parsers ---
# vLLM ships vllm/tool_parsers/cohere_command_tool_parser.py and
# vllm/reasoning/cohere_command_reasoning_parser.py, but BOTH do
# `from cohere_melody import PyFilter, PyFilterOptions` and the base image does
# not install it. Selecting --tool-call-parser cohere_command3/4 (or the
# matching --reasoning-parser) therefore CrashLoops the server with ImportError.
#
# Without this, Cohere-family models (e.g. CohereLabs/North-Mini-Code-1.0-w4a16)
# cannot emit tool calls at all, which disqualifies them as agentic workers --
# and, lacking the reasoning parser, they return raw chain-of-thought as visible
# content, which silently inflates output tokens and corrupts cost measurement.
#
# Pinned: the parsers bind to a native (Rust) extension, so an unpinned floating
# version could change filter behaviour under us. cp312/x86_64 manylinux wheels
# are published, matching this base image's interpreter.
RUN python3 -m pip install --no-cache-dir "cohere_melody==0.13.0" \
 && python3 -c "import cohere_melody; from cohere_melody import PyFilter, PyFilterOptions; print('cohere_melody OK')"
