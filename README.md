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

**Dual-MoE layout (current):**
```
GPU 0 (96 GB total)
+-- Full slice (96 GB)  -->  Gemma 4 26B MoE  — fast MoE model (BF16, 128K context)

GPU 1 (96 GB total)
+-- Full slice (96 GB)  -->  Gemma 4 31B NVFP4 — large capable model (128K context)
```

**Triple layout (2g.48gb on GPU 0):**
```
GPU 0 (96 GB total)
+-- Slice 1 (48 GB)  -->  Gemma 4 E2B  — fastest small model
+-- Slice 2 (48 GB)  -->  Gemma 4 E4B  — slightly larger

GPU 1 (96 GB total)
+-- Full slice (96 GB)  -->  Gemma 4 31B NVFP4
```

**TP=2 layout (single model, max context):**
```
GPU 0 + GPU 1 (192 GB combined)  -->  Gemma 4 31B BF16  — full 256K native context
```

The MIG layout is a single ConfigMap edit — apply it with `cd operator && make mig-setup`.

## The Models

[Gemma 4](https://blog.google/technology/developers/gemma-4/) is Google's latest open-weight model family, ranging from 2B to 27B+ parameters. We tested four sizes:

| Model | Size | Format | Speed | Notes |
|-------|------|--------|-------|-------|
| **E2B** | ~2B params | NVFP4 | 155 tok/s | Fastest — Blackwell-optimized 4-bit format |
| **E4B** | ~4B params | BF16 | 66 tok/s | Standard full-precision |
| **26B-A4B** | 26B MoE, 4B active | BF16 | 113 tok/s | MoE: fast decode despite 26B total params; 128K ctx |
| **31B** | 31B params | NVFP4 | 31 tok/s | 128K ctx on 4g.96gb |
| **31B BF16 TP=2** | 31B params | BF16 | 41 tok/s | Full 256K native context; spans both GPUs via TP=2 |

**NVFP4** deserves a callout: it's a quantization format native to Blackwell GPUs that uses dedicated tensor cores not available on older GPU generations. It delivers more than 2x the throughput of standard BF16 — effectively getting the performance of a much more expensive setup.

## The Software

- **[vLLM](https://github.com/vllm-project/vllm):** Serves the E2B and E4B models. vLLM's "continuous batching" means it processes all incoming requests simultaneously in one pass rather than queuing them — 50 users get responses in roughly the same time as 1 user.
- **[ollama](https://ollama.com/):** Used as a comparison baseline during benchmarking. ollama is simpler to set up but processes requests one at a time, which makes the performance difference dramatic under concurrent load. See [BENCHMARKS.md](BENCHMARKS.md) for the head-to-head results.
- **[MicroK8s](https://microk8s.io/):** Lightweight Kubernetes that manages the containers, GPU access, and networking.
- **NVIDIA GPU Operator:** Kubernetes extension that handles MIG configuration and makes GPU slices available to containers.

## Performance Highlights

With 50 simultaneous users sending a long coding prompt to the E2B model, vLLM delivered **5,350 tokens per second** in aggregate. The MoE 26B-A4B model delivers **113 tok/s single-user** despite having 26B total parameters, because only 4B parameters are active during inference — nearly matching E2B NVFP4 speed with far greater capability.

Running the MoE and 31B NVFP4 simultaneously on two isolated GPU slices produces **530 tok/s combined** with no interference between models. The 31B BF16 model spans both GPUs via tensor parallelism to serve the model's full native **256K context window**.

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
cd operator && make mig-setup
microk8s kubectl wait --for=condition=complete job/mig-setup -n kube-system --timeout=300s
```

### 4. Install the operator and deploy a model

```bash
cd operator && make install && make deploy
# Apply a preset + instance (e.g. E2B on port 30801):
microk8s kubectl apply -f operator/config/samples/presets/gemma-4-e2b.yaml
microk8s kubectl apply -f operator/config/samples/instances/e2b.yaml
microk8s kubectl wait -n vllm vllminstance/e2b --for=condition=Ready --timeout=600s
```

Multi-model layouts: apply the corresponding files from `operator/config/samples/instances/` (e.g. `dual-moe.yaml`, `triple.yaml`).

### 5. Test it

```bash
bash tooluse-demo.sh  # verify function calling works across all three vLLM endpoints
bash loadtest-all.sh  # concurrent load test across all three vLLM endpoints
```

### 6. Tear down

```bash
microk8s kubectl delete vllminstance --all -n vllm  # stop serving, keep model cache on NFS
microk8s kubectl delete namespace vllm              # remove everything
```

## Repository Layout

```
operator/              Kubernetes operator — the current way to deploy (see below)
build.sh               builds the custom vLLM container image (used by the operator)
Dockerfile             extends vllm/vllm-openai:nightly with Gemma 4 support
00-base.yaml           namespace, HF token secret, NFS PV/PVC, service
loadtest-all.sh        concurrent load test across vLLM endpoints
tooluse-demo.sh        function calling demo across endpoints
BENCHMARKS.md          full benchmark report with tables and data
legacy/                pre-operator shell scripts and static YAMLs (reference only)
```

## Kubernetes Operator

A full Kubernetes operator now manages vLLM deployments declaratively, replacing the manual shell scripts and static YAML manifests. The operator lives in `operator/` and introduces two CRDs in the `vllm.aatchison.io/v1alpha1` API group.

### CRDs

**`ModelPreset`** — a reusable vLLM configuration template. It captures the model ID, MIG resource type and count, quantization, context length, GPU memory utilization, tensor parallel size, tool-calling settings, and health probe timeouts. Seven presets ship in `operator/config/samples/presets/` covering the full Gemma 4 model range.

**`VLLMInstance`** — one running model endpoint. It references a `ModelPreset` by name and adds the deployment-specific details: which PVC holds the model cache, which Secret has the HuggingFace token, and which NodePort to expose. The operator creates a `Deployment` and a `NodePort Service` for each instance and tracks readiness in the instance's status.

### Minimal example

```yaml
# 1. Define (or reuse) a preset
apiVersion: vllm.aatchison.io/v1alpha1
kind: ModelPreset
metadata:
  name: gemma-4-e2b
  namespace: vllm
spec:
  modelID: bg-digitalservices/Gemma-4-E2B-it-NVFP4
  migResource: nvidia.com/mig-2g.48gb
  migResourceCount: 1
  quantization: nvfp4
  maxModelLen: 32768
  gpuMemoryUtilization: "0.90"
  tensorParallelSize: 1
  enableAutoToolChoice: true
  toolCallParser: gemma4
---
# 2. Instantiate it
apiVersion: vllm.aatchison.io/v1alpha1
kind: VLLMInstance
metadata:
  name: e2b
  namespace: vllm
spec:
  presetRef: {name: gemma-4-e2b}
  pvcName: vllm-models-pvc
  hfToken: {name: hf-token, key: token}
  nodePort: 30801
```

### Quick start

```bash
# 1. MIG setup (run once after boot)
cd operator && make mig-setup
microk8s kubectl wait --for=condition=complete job/mig-setup -n kube-system --timeout=300s

# 2. Base infrastructure (namespace, HF token, PV/PVC)
microk8s kubectl apply -f 00-base.yaml

# 3. Build the vLLM image and load it into microk8s
./build.sh

# 4. Install CRDs and deploy the operator
cd operator && make install && make deploy

# 5. Apply presets and deploy a model instance
make apply-samples
microk8s kubectl wait -n vllm vllminstance/e2b --for=condition=Ready --timeout=600s
```

Multi-model layouts (dual, triple, dual-moe) have ready-made instance manifests in `operator/config/samples/instances/`. See `operator/README.md` for full details and all `make` targets.

## MIG Profile Combinations

Each RTX PRO 6000 Blackwell has 4 compute slices that can be divided as `4g`, `2g`, or `1g`. Slices on a single GPU must sum to 4 or fewer. Memory scales with compute: `4g` = 96 GB, `2g` = 48 GB, `1g` = 24 GB.

The named profiles below are defined in `operator/config/mig-setup/configmap.yaml` under the `custom-mig-config` key in the `gpu-operator-resources` namespace.

| Config name | GPU 0 | GPU 1 | Total instances | Notes |
|---|---|---|---|---|
| `custom-mig` | 1×4g.96gb | 1×4g.96gb | 2 | Default; large models or TP=2 |
| `all-2g.48gb` | 2×2g.48gb | 2×2g.48gb | 4 | Mid-size concurrent |
| `all-1g.24gb` | 4×1g.24gb | 4×1g.24gb | 8 | Max concurrency, small models |
| `mixed-2g-1g` | 1×2g.48gb + 2×1g.24gb | 1×2g.48gb + 2×1g.24gb | 6 | 31B + E2B/E4B mix |
| `asym-4g-2g` | 1×4g.96gb | 2×2g.48gb | 3 | BF16 big + two 31B NVFP4 |
| `asym-4g-1g` | 1×4g.96gb | 4×1g.24gb | 5 | BF16 big + four small |
| `asym-4g-2g-1g` | 1×4g.96gb | 1×2g.48gb + 2×1g.24gb | 4 | Full spectrum |

### Switching profiles

```bash
# Label the node to trigger reconfiguration (run on the cluster node)
microk8s kubectl label node <node> nvidia.com/mig.config=<config-name> --overwrite

# Wait for mig.config.state=success, then restart the device plugin
microk8s kubectl delete pod -n gpu-operator-resources -l app=nvidia-device-plugin-daemonset
```

### Preset-to-slice mapping

| Slice size | VRAM | Presets |
|---|---|---|
| `1g.24gb` | 24 GB | `gemma-4-e2b-1g` |
| `2g.48gb` | 48 GB | `gemma-4-e2b`, `gemma-4-e4b`, `gemma-4-26b-a4b`, `gemma-4-31b-nvfp4` |
| `4g.96gb` | 96 GB | `gemma-4-31b-nvfp4-96`, `gemma-4-31b-bf16`, `gemma-4-31b-bf16-tp2` (TP=2 uses both GPUs' 4g slices) |
