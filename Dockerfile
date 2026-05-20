# Base image: vLLM nightly with OpenAI-compatible API server
# We use nightly (not :latest) because :latest lags behind and lacks
# support for newer model architectures.
FROM vllm/vllm-openai:nightly

# --- Why this layer is needed ---
# Gemma 4 (released April 2025) uses a new architecture type ("gemma4")
# that is only recognized by transformers >= 4.51.0.
#
# The nightly vLLM image ships with an older pinned version of transformers
# that predates Gemma 4, causing this error at startup:
#
#   ValueError: The checkpoint you are trying to load has model type `gemma4`
#   but Transformers does not recognize this architecture.
#
# Upgrading transformers here bakes the fix into the image so no runtime
# patching is needed.
#
# bitsandbytes is also added here — it is not included in the base image
# and is required for INT8 quantization used by the larger models (26B-A4B, 31B).
RUN pip install --quiet --upgrade "transformers>=4.51.0" bitsandbytes

# --- Issue #103: passwd entry for the running uid ---
# Pods run as uid 1000 under the operator's runAsNonRoot security context
# (issue #37). A recent torch upgrade now calls getpass.getuser() at module
# import time (torch._dynamo.package), which falls back to
# pwd.getpwuid(getuid()) and raises KeyError when the uid has no /etc/passwd
# entry. Adding the entry here fixes the image for any caller. The
# operator also sets TORCHINDUCTOR_CACHE_DIR + HOME as a second layer
# of defense.
RUN echo 'vllm:x:1000:1000::/home/vllm:/usr/sbin/nologin' >> /etc/passwd \
 && mkdir -p /home/vllm && chown 1000:1000 /home/vllm
