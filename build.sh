#!/bin/bash
set -euo pipefail

# Build the custom vLLM image and import it into microk8s containerd.
#
# Why a custom image?
# -------------------
# Stock vllm/vllm-openai images (including :nightly) ship with a pinned
# version of HuggingFace `transformers` that does not support the Gemma 4
# model architecture. Running Gemma 4 against the stock image produces:
#
#   ValueError: model type `gemma4` ... Transformers does not recognize
#   this architecture.
#
# The Dockerfile extends vllm/vllm-openai:nightly and upgrades transformers
# to >= 4.51.0, which added Gemma 4 support.
#
# Why microk8s ctr import instead of a registry?
# -----------------------------------------------
# The microk8s built-in registry addon requires sudo to enable. Instead,
# we build with Docker (already available) and pipe the image directly into
# microk8s's containerd via `microk8s ctr images import`. This makes the
# image available to pods with imagePullPolicy: Never without needing a
# registry or pushing anywhere.
#
# Note: The first build pulls the nightly base (~9.5GB). Subsequent builds
# are fast since Docker caches the base layer.

IMAGE_NAME="vllm-gemma4:local"
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"

echo "==> Building $IMAGE_NAME"
docker build -t "$IMAGE_NAME" "$SCRIPT_DIR"

echo "==> Importing into microk8s containerd"
docker save "$IMAGE_NAME" | sudo microk8s ctr images import -

echo "==> Done. Image available as docker.io/library/$IMAGE_NAME"
echo "    Deploy with: ./deploy.sh <model-size>"
