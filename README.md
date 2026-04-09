# Running Google's Gemma 4 AI Models on a Home Kubernetes Cluster

This repo contains everything needed to deploy and benchmark Google's Gemma 4 family of language models on a single-node Kubernetes cluster with NVIDIA GPUs — no cloud required.

## What is this?

We took two NVIDIA RTX PRO 6000 Blackwell GPUs (each with 96 GB of VRAM) and ran three Gemma 4 models simultaneously, serving them as OpenAI-compatible API endpoints that any application can talk to. We load-tested them at up to 50 simultaneous requests, measured performance, and verified tool/function calling — all on a single machine sitting on a home network.

## The Hardware

- **CPU:** AMD Ryzen Threadripper PRO 9975WX (32 cores)
- **RAM:** 125 GB
- **GPU 0:** NVIDIA RTX PRO 6000 Blackwell — 96 GB VRAM, 600W TDP
- **GPU 1:** NVIDIA RTX PRO 6000 Blackwell — 96 GB VRAM, 600W TDP
- **Storage:** NFS server for shared model weights (see below)

## Why NFS?

Large language models can be tens of gigabytes. Downloading them every time a pod restarts — or keeping separate copies for each deployment — wastes time and disk space. Instead, we point an NFS export at all of our pods through a Kubernetes PersistentVolume. The HuggingFace Hub library caches downloaded model weights on this shared volume, so:

- Models are downloaded **once** and reused by every deployment.
- Pod restarts, redeployments, and scaling events don't re-download anything.
- Multiple models (E2B, E4B, 31B) share the same cache directory without duplication.

Any NFS server on your network will work. Set the IP and export path in `00-base.yaml`.

## The Key Trick: MIG (Multi-Instance GPU)

Rather than dedicating an entire GPU to one model, NVIDIA's MIG technology lets you carve a GPU into isolated slices — each slice gets its own dedicated memory and compute, completely walled off from the others.

We split the hardware like this:

```
GPU 0 (96 GB total)
+-- Slice 1 (48 GB)  -->  Gemma 4 E2B  — a fast, small model
+-- Slice 2 (48 GB)  -->  Gemma 4 E4B  — a slightly larger model

GPU 1 (96 GB total)
+-- Full slice (96 GB)  -->  Gemma 4 31B  — a large, capable model via ollama
```

Three models, two GPUs, all running simultaneously without interfering with each other.

## The Models

[Gemma 4](https://blog.google/technology/developers/gemma-4/) is Google's latest open-weight model family, ranging from 2B to 27B+ parameters. We tested four sizes:

| Model | Size | Format | Speed | Notes |
|-------|------|--------|-------|-------|
| **E2B** | ~2B params | NVFP4 | 155 tok/s | Fastest — Blackwell-optimized 4-bit format |
| **E4B** | ~4B params | BF16 | 66 tok/s | Standard full-precision |
| **31B** | 31B params | GGUF (ollama) | 62 tok/s | Largest, most capable |
| **26B-A4B** | 26B MoE | FP8 | broken | Community checkpoint incompatible with vLLM |

**NVFP4** deserves a callout: it's a quantization format native to Blackwell GPUs that uses dedicated tensor cores not available on older GPU generations. It delivers more than 2x the throughput of standard BF16 — effectively getting the performance of a much more expensive setup.

## The Software

- **[vLLM](https://github.com/vllm-project/vllm):** Serves the E2B and E4B models. vLLM's "continuous batching" means it processes all incoming requests simultaneously in one pass rather than queuing them — 50 users get responses in roughly the same time as 1 user.
- **[ollama](https://ollama.com/):** Serves the 31B model. Simpler setup, but processes requests one at a time.
- **[MicroK8s](https://microk8s.io/):** Lightweight Kubernetes that manages the containers, GPU access, and networking.
- **NVIDIA GPU Operator:** Kubernetes extension that handles MIG configuration and makes GPU slices available to containers.

## Performance Highlights

With 50 simultaneous users sending a long coding prompt to the E2B model, vLLM delivered **5,350 tokens per second** in aggregate — all 50 responses generating in parallel, completing in 38 seconds total. The equivalent scenario on ollama would take over 50 minutes.

Both GPUs ran near their 600W thermal design power for sustained periods during load testing, reaching up to 93 C, with **no thermal throttling** in any test.

See [BENCHMARKS.md](BENCHMARKS.md) for full results, graphs, and a detailed breakdown of every test.

## Tool / Function Calling

All three endpoints support OpenAI-style [function calling](https://platform.openai.com/docs/guides/function-calling) — where the model can decide to call a function you've defined (like looking up weather, querying a database, etc.) rather than just generating text. No extra configuration needed on the application side; just pass a `tools` array in the request like you would to OpenAI's API.

## Using This Repo

### Prerequisites

- A Linux machine with one or more NVIDIA MIG-capable GPUs
- [MicroK8s](https://microk8s.io/) with the `gpu` addon enabled
- NVIDIA GPU Operator installed (comes with the MicroK8s GPU addon)
- An NFS server exporting a directory for model storage
- A [HuggingFace](https://huggingface.co/) account with access to Gemma 4 models

### 1. Configure

Two things need to be set in `00-base.yaml` before deploying:

```yaml
# Your HuggingFace token (get one at https://huggingface.co/settings/tokens)
stringData:
  token: "YOUR_HUGGINGFACE_TOKEN_HERE"

# Your NFS server
nfs:
  server: "YOUR_NFS_SERVER_IP"
  path: "/your/nfs/export/path"
```

### 2. Build the vLLM container image

```bash
./build.sh
```

This builds a custom vLLM image with Gemma 4 support and imports it into MicroK8s.

### 3. Set up MIG (after every reboot)

```bash
./deploy.sh setup
```

### 4. Deploy a model

```bash
./deploy.sh E2B       # small fast model on port 30800
./deploy.sh 31B       # large model on port 30800
./deploy.sh dual      # E2B + E4B simultaneously on ports 30801/30802
```

### 5. Test it

```bash
./deploy.sh test      # quick smoke test
bash tooluse-demo.sh  # verify function calling works
bash loadtest-all.sh  # full load test against all endpoints
```

### 6. Tear down

```bash
./deploy.sh undeploy  # stop serving, keep model cache on NFS
./deploy.sh destroy   # remove everything (model cache on NFS is untouched)
```

## Repository Layout

```
deploy.sh              main entry point for all operations
setup-mig.sh           restores GPU partitioning after reboot
build.sh               builds the custom vLLM container image
Dockerfile             extends vllm/vllm-openai:nightly with Gemma 4 support
00-base.yaml           namespace, HF token secret, NFS PV/PVC, service
deploy-dual.yaml       run E2B + E4B simultaneously
deploy-gemma4-*.yaml   per-model deployment configs
loadtest-all.sh        concurrent load test (vLLM + ollama)
tooluse-demo.sh        function calling demo across all endpoints
BENCHMARKS.md          full benchmark report with tables and data
```
