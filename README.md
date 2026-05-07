# deploy-vllm-k8s

A Kubernetes operator for running multiple vLLM model instances on NVIDIA MIG-partitioned GPUs. The operator manages four CRDs — `ModelPreset`/`VLLMInstance` for general serving and `LongContextPreset`/`LongContextInstance` for max-context-per-model serving — and automatically creates a `Deployment` and a `NodePort Service` for each instance.

## Hardware

| Component | Details |
|-----------|---------|
| CPU | AMD Ryzen Threadripper PRO 9975WX (32 cores) |
| RAM | 125 GB |
| GPU 0 | NVIDIA RTX PRO 6000 Blackwell — 97 GB VRAM, MIG-enabled |
| GPU 1 | NVIDIA RTX PRO 6000 Blackwell — 97 GB VRAM, MIG-enabled |
| Storage | NFS PVC for shared model weights |
| Kubernetes | microk8s (single-node) |

Each GPU has 4 compute slices that MIG carves into isolated partitions. Slices on a single GPU must sum to ≤4. Memory scales linearly: `4g` = 96 GB, `2g` = 48 GB, `1g` = 24 GB.

## Prerequisites

- microk8s with the `gpu` addon enabled
- NVIDIA GPU Operator with `mig-manager` (included in the microk8s `gpu` addon)
- A PersistentVolumeClaim (`vllm-models-pvc`) backed by NFS or similar shared storage
- A Secret named `hf-token` in the `vllm` namespace containing your HuggingFace token

## Quick Start

```bash
# 1. Namespace, HF token secret, and PVC
microk8s kubectl apply -f 00-base.yaml

# 2. MIG setup (run once after every reboot)
cd operator && make mig-setup
microk8s kubectl wait --for=condition=complete job/mig-setup -n kube-system --timeout=300s

# 3. Build the vLLM container image and load it into microk8s
./build.sh

# 4. Install CRDs and deploy the operator
cd operator && make install && make deploy

# 5. Apply presets
microk8s kubectl apply -f operator/config/samples/presets/

# 6. Deploy an instance (NodePort is auto-assigned if omitted)
microk8s kubectl apply -f operator/config/samples/instances/e2b.yaml
microk8s kubectl wait -n vllm vllminstance/e2b --for=condition=Ready --timeout=600s
```

Minimal `VLLMInstance` — no `nodePort` field needed; Kubernetes auto-assigns one:

```yaml
apiVersion: vllm.aatchison.io/v1alpha1
kind: VLLMInstance
metadata:
  name: e2b
  namespace: vllm
spec:
  presetRef: {name: gemma-4-e2b}
  pvcName: vllm-models-pvc
  hfToken: {name: hf-token, key: token}
```

Ready-made multi-model manifests live in `operator/config/samples/instances/` (`dual-moe.yaml`, `triple.yaml`, etc.).

## CRDs

### ModelPreset

A reusable vLLM configuration bundle. Captures the model ID, MIG resource type, quantization, context length, tensor parallelism, tool-calling settings, and probe timeouts. Presets are defined once and referenced by any number of instances.

```yaml
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
  shmSizeLimit: 8Gi
  progressDeadlineSeconds: 600
  livenessProbe: {initialDelaySeconds: 300, periodSeconds: 30, failureThreshold: 10}
  readinessProbe: {initialDelaySeconds: 60, periodSeconds: 10, failureThreshold: 30}
```

Seven presets ship in `operator/config/samples/presets/`, covering the full Gemma 4 range from E2B (1g.24gb) through 31B BF16 TP=2 (two 4g.96gb slices across both GPUs).

### VLLMInstance

One running model endpoint. References a `ModelPreset` by name and adds deployment-specific details. The operator creates a `Deployment` and a `NodePort Service`, then tracks readiness in `.status`.

```yaml
apiVersion: vllm.aatchison.io/v1alpha1
kind: VLLMInstance
metadata:
  name: e2b
  namespace: vllm
spec:
  presetRef: {name: gemma-4-e2b}
  pvcName: vllm-models-pvc
  hfToken: {name: hf-token, key: token}
  # nodePort: 30801   # optional — omit to let Kubernetes auto-assign
```

### LongContextPreset / LongContextInstance

Sibling CRDs tuned for **maximum context length per model**. Same wire shape
as `ModelPreset`/`VLLMInstance` but with two additional opinionated fields
that the operator emits as vLLM CLI flags:

- `kvCacheDtype` (required, enum `fp8`/`fp8_e5m2`/`fp8_e4m3`) → `--kv-cache-dtype`.
  FP8 KV cache roughly halves KV memory at long context, approximately doubling
  achievable `maxModelLen` on the same MIG slice.
- `enablePrefixCaching` (default `true`) → `--enable-prefix-caching`. RadixAttention-
  style automatic KV prefix reuse; pays off for agent workloads with shared system prompts.

Three long-context presets ship in `operator/config/samples/presets/`:

| Preset | MIG | Weights | KV | Target context |
|---|---|---|---|---|
| `gemma-4-31b-nvfp4-longctx` | 4g.96gb | NVFP4 | FP8 e5m2 | 256K (Gemma 4 native max) |
| `gemma-4-31b-bf16-longctx` | 4g.96gb | BF16 | FP8 e5m2 | 128K |
| `gemma-4-26b-moe-longctx` | 4g.96gb | BF16 | FP8 e5m2 | 128K (MoE native max) |

Use `LongContextPreset`/`LongContextInstance` when you want the longest serving window
the slice can hold. Use `ModelPreset`/`VLLMInstance` when you want the existing
default behavior unchanged. The two pairs are independent — you can run instances
of both types on the same cluster.

```yaml
apiVersion: vllm.aatchison.io/v1alpha1
kind: LongContextInstance
metadata:
  name: 31b-nvfp4-longctx
  namespace: vllm
spec:
  presetRef: {name: gemma-4-31b-nvfp4-longctx}
  pvcName: vllm-models-pvc
  hfToken: {name: hf-token, key: token}
```

## MIG Profile Combinations

The named profiles below are defined in `operator/config/mig-setup/configmap.yaml`. Apply a profile by labeling the node:

```bash
microk8s kubectl label node <node> nvidia.com/mig.config=<config-name> --overwrite
# After mig.config.state=success, restart the device plugin:
microk8s kubectl delete pod -n gpu-operator-resources -l app=nvidia-device-plugin-daemonset
```

| Config name | GPU 0 | GPU 1 | Total instances | Notes |
|---|---|---|---|---|
| `all-disabled` | MIG disabled | MIG disabled | 2 (whole GPUs) | Baseline; whole-GPU access |
| `custom-mig` | 1×4g.96gb | 1×4g.96gb | 2 | Default; large models or TP=2 |
| `all-2g.48gb` | 2×2g.48gb | 2×2g.48gb | 4 | Mid-size concurrent |
| `all-1g.24gb` | 4×1g.24gb | 4×1g.24gb | 8 | Max concurrency, small models |
| `mixed-2g-1g` | 1×2g.48gb + 2×1g.24gb | 1×2g.48gb + 2×1g.24gb | 6 | Mix of 31B NVFP4 + E2B/E4B |
| `asym-4g-2g` | 1×4g.96gb | 2×2g.48gb | 3 | BF16 big model + two mid-size |
| `asym-4g-1g` | 1×4g.96gb | 4×1g.24gb | 5 | BF16 big model + four small |
| `asym-4g-2g-1g` | 1×4g.96gb | 1×2g.48gb + 2×1g.24gb | 4 | Full spectrum |

### Preset-to-slice mapping

| Slice | VRAM | Presets |
|---|---|---|
| `1g.24gb` | 24 GB | `gemma-4-e2b-1g` |
| `2g.48gb` | 48 GB | `gemma-4-e2b`, `gemma-4-e4b`, `gemma-4-26b-a4b`, `gemma-4-31b-nvfp4` |
| `4g.96gb` | 96 GB | `gemma-4-31b-nvfp4-96`, `gemma-4-31b-bf16` |
| `4g.96gb` ×2 (TP=2) | 192 GB | `gemma-4-31b-bf16-tp2` (spans both GPUs) |

## Smoke Test

```bash
cd operator && make smoke-test INSTANCE=<name>
# e.g. make smoke-test INSTANCE=e2b
```

## Repository Layout

```
operator/              Kubernetes operator (CRDs, controller, MIG setup, samples)
build.sh               builds the custom vLLM container image
Dockerfile             extends vllm/vllm-openai:nightly with Gemma 4 support
00-base.yaml           namespace, HF token Secret, NFS PV/PVC
loadtest-all.sh        concurrent load test across vLLM endpoints
tooluse-demo.sh        function-calling demo across endpoints
BENCHMARKS.md          full benchmark report
legacy/                pre-operator deploy.sh workflow (reference only)
```

## Legacy Scripts

The old `deploy.sh`-based workflow is preserved in `legacy/` for reference. The operator is the current and recommended approach.

## License

This project is licensed under the [Apache License, Version 2.0](LICENSE).
Per-file copyright headers already declared Apache 2.0; the root `LICENSE` and
`NOTICE` files make the declaration explicit at the project level. See
[NOTICE](NOTICE) for AI-assisted-development attribution context.
