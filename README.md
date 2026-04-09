# Running Google's Gemma 4 AI Models on a Home Kubernetes Cluster

This repo contains everything needed to deploy and benchmark Google's Gemma 4 family of language models on a single-node Kubernetes cluster using consumer workstation GPUs — no cloud required.

## What is this?

The goal was to take two NVIDIA RTX PRO 6000 Blackwell GPUs (each with 96 GB of VRAM) and run as many Gemma 4 models as possible simultaneously, serving them as OpenAI-compatible API endpoints that any app can talk to.

The result: three models running at the same time across two GPUs, all accessible via standard HTTP APIs, with tool/function calling support, load tested at up to 50 simultaneous requests.

## The Hardware

Everything runs on a single machine sitting on a home network:

- **CPU:** AMD Ryzen Threadripper PRO 9975WX (32 cores)
- **RAM:** 125 GB
- **GPU 0:** NVIDIA RTX PRO 6000 Blackwell — 96 GB VRAM, 600W TDP
- **GPU 1:** NVIDIA RTX PRO 6000 Blackwell — 96 GB VRAM, 600W TDP
- **Storage:** Models live on a separate NFS server so they're downloaded once and shared across everything

## The Key Trick: MIG (Multi-Instance GPU)

Rather than dedicating an entire GPU to one model, NVIDIA's MIG technology lets you carve a GPU into isolated slices — each slice gets its own dedicated memory and compute, completely isolated from the others.

We split the hardware like this:

```
GPU 0 (96 GB total)
├── Slice 1 (48 GB)  →  Gemma 4 E2B  — a fast, small model
└── Slice 2 (48 GB)  →  Gemma 4 E4B  — a slightly larger model

GPU 1 (96 GB total)
└── One big slice (96 GB)  →  Gemma 4 31B  — a large, capable model via ollama
```

Three models, two GPUs, all running simultaneously without interfering with each other.

## The Models

[Gemma 4](https://blog.google/technology/developers/gemma-4/) is Google's latest open-weight model family, ranging from 2B to 31B parameters. We tested four sizes:

| Model | Size | Format | Speed | Notes |
|-------|------|--------|-------|-------|
| **E2B** | ~2B params | NVFP4 | ⚡ 155 tok/s | Fastest — Blackwell-optimized 4-bit format |
| **E4B** | ~4B params | BF16 | 66 tok/s | Standard full-precision |
| **31B** | 31B params | GGUF (ollama) | 62 tok/s | Largest, most capable |
| **26B-A4B** | 26B MoE | FP8 | ❌ broken | Community checkpoint incompatible with vLLM |

**NVFP4** deserves a callout: it's a quantization format native to Blackwell GPUs that uses dedicated tensor cores not available on older GPU generations. It delivers more than 2× the throughput of standard BF16 — effectively getting the performance of a much more expensive setup.

## The Software

- **[vLLM](https://github.com/vllm-project/vllm):** Serves the E2B and E4B models. vLLM's "continuous batching" means it processes all incoming requests simultaneously in one pass rather than queuing them — 50 users get responses in roughly the same time as 1 user.
- **[ollama](https://ollama.com/):** Serves the 31B model. Simpler setup, but processes requests one at a time.
- **[MicroK8s](https://microk8s.io/):** Lightweight Kubernetes that manages the containers, GPU access, and networking.
- **NVIDIA GPU Operator:** Kubernetes extension that handles MIG configuration and makes GPU slices available to containers.

## Performance Highlights

With 50 simultaneous users sending a long coding prompt to the E2B model, vLLM delivered **5,350 tokens per second** in aggregate — all 50 responses generating in parallel, completing in 38 seconds total. The equivalent scenario with ollama would take over 50 minutes.

Both GPUs ran near their 600W thermal design power for sustained periods during load testing, reaching up to 93°C, with **no thermal throttling** in any test.

See [BENCHMARKS.md](BENCHMARKS.md) for full results, graphs, and a detailed breakdown of every test.

## Tool / Function Calling

All three endpoints support OpenAI-style [function calling](https://platform.openai.com/docs/guides/function-calling) — where the model can decide to call a function you've defined (like looking up weather, querying a database, etc.) rather than just generating text. No extra configuration needed on the application side; just pass a `tools` array in the request like you would to OpenAI's API.

## Using This Repo

> **Prerequisites:** MicroK8s with the GPU addon, NVIDIA GPU Operator, and a HuggingFace account with access to the Gemma 4 model family.

**1. Add your HuggingFace token**

Edit `00-base.yaml` and replace `YOUR_HUGGINGFACE_TOKEN_HERE` with your actual token.

**2. After every reboot, restore MIG config**

```bash
./deploy.sh setup
```

**3. Deploy a model**

```bash
./deploy.sh E2B       # small fast model on port 30800
./deploy.sh 31B       # large model on port 30800
./deploy.sh dual      # E2B + E4B simultaneously on ports 30801/30802
```

**4. Test it**

```bash
./deploy.sh test      # quick smoke test
bash tooluse-demo.sh  # verify function calling works
bash loadtest-all.sh  # full load test against all three endpoints
```

**5. Tear down**

```bash
./deploy.sh undeploy  # stop serving, keep model cache
./deploy.sh destroy   # remove everything (models stay on NFS)
```

## Repository Layout

```
deploy.sh          — main entry point for all operations
setup-mig.sh       — restores GPU partitioning after reboot
00-base.yaml       — namespace, secret, NFS storage
deploy-dual.yaml   — run E2B + E4B simultaneously
deploy-gemma4-*.yaml — per-model deployment configs
loadtest-all.sh    — concurrent load test (vLLM + ollama)
tooluse-demo.sh    — function calling demo across all endpoints
BENCHMARKS.md      — full benchmark report with graphs and data
```
