# vLLM Kubernetes Operator

## Context

Replace the existing shell scripts and static YAML manifests in this repo with a Kubernetes operator. The operator makes vLLM deployments declarative and eliminates manual `deploy.sh` usage.

**Scope for v1:** Parity with existing `deploy.sh` — seven distinct single-model variants (E2B, E4B, 26B-A4B, 31B-NVFP4, 31B-96, 31B-bf16, 31B-bf16-tp2), TP=2 across two MIG slices, and multi-model layouts (dual/dual-moe/triple) expressed as multiple VLLMInstance CRs. *Note: the existing `deploy-gemma4-27B.yaml` is a filename misnomer — its args are identical to `31B-bf16` on the 4g.96gb slice, so it collapses into one preset.*

**Out of scope for v1:** image build (`build.sh` stays), non-MIG GPUs, multi-node scheduling, conversion webhooks.

**Prerequisite (one-time, outside the operator):** MIG slice setup via a `config/mig-setup/job.yaml` Job (translated from `setup-mig.sh`), and a PV+PVC YAML applied manually. The operator assumes the NVIDIA GPU Operator is installed and `nvidia-mig-manager` owns the `nvidia.com/mig.config` label. This is a cooperating operator, not a competing one.

---

## Architecture

Two CRDs in group `vllm.aatchison.io/v1alpha1`, both namespaced:

```
ModelPreset (namespaced) ──► VLLMInstance (namespaced)
                                 owns: Deployment + Service
```

- **ModelPreset** — reusable bundle of vLLM args (modelID, MIG resource, context, quantization, probe timings, progressDeadlineSeconds). No controller; consumed by VLLMInstance.
- **VLLMInstance** — resolves preset, composes Deployment + Service, references an existing PVC by name.

Dropped from earlier iterations: `MIGPool` (replaced by one-shot Job), `ModelStorage` (replaced by direct PVC reference).

Framework: **kubebuilder / controller-runtime** (Go 1.22, k8s.io v0.29.x).

---

## Directory Structure

```
operator/
├── Dockerfile
├── Makefile
├── go.mod
├── main.go
├── api/v1alpha1/
│   ├── groupversion_info.go
│   ├── modelpreset_types.go
│   ├── vllminstance_types.go
│   └── zz_generated.deepcopy.go       # generated
├── controllers/
│   └── vllminstance_controller.go
├── internal/
│   └── vllm/
│       ├── deployment.go              # buildDeployment()
│       ├── service.go                 # buildService()
│       └── merge.go                   # resolve preset + overrides
├── config/
│   ├── crd/                           # generated
│   ├── rbac/                          # generated
│   ├── manager/manager.yaml
│   ├── default/kustomization.yaml
│   ├── mig-setup/                     # replaces setup-mig.sh
│   │   ├── serviceaccount.yaml
│   │   ├── clusterrole.yaml
│   │   ├── clusterrolebinding.yaml
│   │   ├── configmap.yaml
│   │   └── job.yaml
│   ├── storage/                       # sample PV/PVC
│   │   ├── pv-nfs.yaml
│   │   └── pvc-local.yaml
│   └── samples/
│       ├── presets/                   # 7 ModelPreset samples
│       └── instances/                 # 7 VLLMInstance samples + dual/triple layouts
└── hack/boilerplate.go.txt
```

Also at the repo root: delete `vllm-svc` and related PV/PVC from `00-base.yaml` (operator owns these now; only `namespace` + `hf-token` Secret stay).

---

## CRD Specs

### ModelPreset (no controller)

```go
type ModelPresetSpec struct {
    ModelID              string       `json:"modelID"`
    Image                string       `json:"image,omitempty"`             // default "docker.io/library/vllm-gemma4:local"
    ImagePullPolicy      string       `json:"imagePullPolicy,omitempty"`   // default "Never"
    MIGResource          string       `json:"migResource"`                 // "nvidia.com/mig-2g.48gb" | "nvidia.com/mig-4g.96gb"
    MIGResourceCount     int32        `json:"migResourceCount"`            // 1 or 2 (TP=2)
    Quantization         string       `json:"quantization,omitempty"`
    DType                string       `json:"dtype,omitempty"`
    MaxModelLen          int32        `json:"maxModelLen"`
    GPUMemoryUtilization string       `json:"gpuMemoryUtilization"`        // kept as string; vLLM accepts "0.95"
    TensorParallelSize   int32        `json:"tensorParallelSize"`
    EnableAutoToolChoice bool         `json:"enableAutoToolChoice"`
    ToolCallParser       string       `json:"toolCallParser,omitempty"`
    SHMSizeLimit         string       `json:"shmSizeLimit"`                // "8Gi" | "16Gi" | "32Gi"
    ProgressDeadlineSeconds int32     `json:"progressDeadlineSeconds"`     // 600 (small), 1800 (big), 2400 (tp2)
    LivenessProbe        ProbeConfig  `json:"livenessProbe"`
    ReadinessProbe       ProbeConfig  `json:"readinessProbe"`
}

type ProbeConfig struct {
    InitialDelaySeconds int32 `json:"initialDelaySeconds"`
    PeriodSeconds       int32 `json:"periodSeconds"`
    FailureThreshold    int32 `json:"failureThreshold"`
}
```

**Seven presets ship in `config/samples/presets/`**, each with exact probe values, progressDeadlineSeconds, and default NodePorts taken from the current YAMLs:

| Preset | Liveness initDelay | Readiness (init/period/failThresh) | progressDeadline | Default NodePort |
|---|---|---|---|---|
| gemma-4-e2b | 300 | 60/10/30 | 600 | 30801 |
| gemma-4-e4b | 480 | 90/10/30 | 600 | 30802 |
| gemma-4-26b-a4b | 1200 | 180/15/40 | 1800 | 30801 |
| gemma-4-31b-nvfp4 | 1200 | 180/15/40 | 1800 | 30800 |
| gemma-4-31b-nvfp4-96 | 1200 | 180/15/40 | 1800 | 30800 |
| gemma-4-31b-bf16 | 1200 | 180/15/40 | 1800 | 30803 |
| gemma-4-31b-bf16-tp2 | 1800 | 300/15/60 | 2400 | 30800 |

Liveness periodSeconds=30 and failureThreshold=10 are constant — hardcode in builder. NodePort is required on VLLMInstance (no default in preset) — the table above reflects what each sample instance YAML should set, based on `deploy-dual/dual-moe/triple.yaml` assignments. Single-model usage previously shared NodePort 30800 via `vllm-svc`; users picking any port in range 30000-32767 works as long as it's unique per concurrent instance.

### VLLMInstance

```go
type VLLMInstanceSpec struct {
    // Preset to inherit from. Must exist in the same namespace. Optional.
    PresetRef *PresetReference `json:"presetRef,omitempty"`

    // Overrides: any non-nil field replaces the preset value.
    // Mirrors ModelPresetSpec field-for-field; grouped to keep the top level clean.
    Overrides *ModelConfigOverrides `json:"overrides,omitempty"`

    // Storage: reference an existing PVC. The operator does not create PVCs.
    PVCName string `json:"pvcName"`

    // HFToken: SecretKeySelector so users can pick the key name.
    HFToken corev1.SecretKeySelector `json:"hfToken"`

    // NodePort for the Service (30000-32767).
    NodePort int32 `json:"nodePort"`

    // Replicas. 0 allows "scale down" (matches deploy.sh undeploy).
    // Default 1. Must be 0 or 1 (multi-replica on one MIG slice is invalid).
    Replicas *int32 `json:"replicas,omitempty"`
}

type PresetReference struct {
    Name string `json:"name"`
}

// Same shape as ModelPresetSpec but every field is a pointer — non-nil means override.
// Probe overrides REPLACE the whole struct (no partial-probe merging) — document in CRD description.
type ModelConfigOverrides struct {
    ModelID              *string      `json:"modelID,omitempty"`
    Image                *string      `json:"image,omitempty"`
    // ... one entry per ModelPresetSpec field
    LivenessProbe        *ProbeConfig `json:"livenessProbe,omitempty"`  // whole-struct replace
    ReadinessProbe       *ProbeConfig `json:"readinessProbe,omitempty"` // whole-struct replace
}

type VLLMInstanceStatus struct {
    // Conditions: PresetResolved, StorageReady, Progressing, DeploymentAvailable, Ready.
    // Progressing mirrors the owned Deployment's Progressing condition for alerting parity.
    Conditions []metav1.Condition `json:"conditions,omitempty" patchStrategy:"merge" patchMergeKey:"type"`

    DeploymentName string `json:"deploymentName,omitempty"`
    ServiceName    string `json:"serviceName,omitempty"`
    ReadyReplicas  int32  `json:"readyReplicas,omitempty"`
    Endpoint       string `json:"endpoint,omitempty"`           // http://<nodeIP>:<nodePort>/v1; nodeIP = InternalIP of any Ready node hosting a Ready pod (looked up via EndpointSlice)
    // sha256 hex of canonical JSON (json.Marshal on the EffectiveConfig struct; no maps allowed in EffectiveConfig to keep ordering deterministic). Debugging aid only.
    ResolvedConfigHash string `json:"resolvedConfigHash,omitempty"`

    // Must be written = instance.Generation on every status update so `kubectl wait` never latches onto stale conditions.
    ObservedGeneration int64 `json:"observedGeneration,omitempty"`
}
```

No `Phase` string enum — use Conditions (API convention). A `Ready` printer column derives from `Conditions[type=Ready].status`.

**Resolution order (highest wins):** `Overrides.<Field>` → `ModelPreset.<Field>` → controller constant default.

**Validation (CEL `+kubebuilder:validation:XValidation`, attached at the `spec` struct level):**
- Preset OR required overrides: `has(self.presetRef) || (has(self.overrides) && has(self.overrides.modelID) && has(self.overrides.migResource) && has(self.overrides.maxModelLen))`
- TP consistency: `!has(self.overrides) || !has(self.overrides.tensorParallelSize) || self.overrides.tensorParallelSize <= 1 || self.overrides.migResourceCount == self.overrides.tensorParallelSize`
- NodePort range: `self.nodePort >= 30000 && self.nodePort <= 32767`
- PVC immutability (guards `oldSelf` nil on CREATE): `!has(oldSelf) || self.pvcName == oldSelf.pvcName`
- Replicas cap: `!has(self.replicas) || self.replicas <= 1`

Immutability rules require `+kubebuilder:validation:XValidation:rule="...",message="...",reason="FieldImmutable"` and rely on `optionalOldSelf=true` to permit CREATE. Place on the `spec` struct, not individual fields.

Typical VLLMInstance CR:
```yaml
apiVersion: vllm.aatchison.io/v1alpha1
kind: VLLMInstance
metadata: {name: e2b, namespace: vllm}
spec:
  presetRef: {name: gemma-4-e2b}
  pvcName: vllm-models-pvc
  hfToken: {name: hf-token, key: token}
  nodePort: 30801
```

---

## Controller Logic

### VLLMInstanceReconciler

Watches:
- `VLLMInstance` (primary)
- `appsv1.Deployment` + `corev1.Service` via ownerRef (`.Owns(...)`)
- `corev1.Node` via `.Watches(...)` — so endpoint IP updates when nodes come/go
- `ModelPreset` via `handler.EnqueueRequestsFromMapFunc` backed by a field indexer on `spec.presetRef.name`, registered in `SetupWithManager` with `mgr.GetFieldIndexer().IndexField(&VLLMInstance{}, "spec.presetRef.name", ...)`. The map function **filters by same-namespace** (`obj.GetNamespace() == preset.GetNamespace()`) before listing. Preset edits re-reconcile every referencing instance in that namespace.

Reconcile steps:
1. Fetch VLLMInstance; return if deleted. No finalizer — Deployment/Service are cascade-deleted via ownerRef.
2. **Resolve config:** if `presetRef` set, `Get` the ModelPreset in same namespace → NotFound sets `PresetResolved=False`, returns `ctrl.Result{RequeueAfter: 15*time.Second}, nil` (not an error, try again). Else require `overrides` fields.
3. **Merge:** `overrides` → `preset` → constant defaults (Image, ImagePullPolicy). Produce `EffectiveConfig` struct (no maps, deterministic field order). Compute `sha256` of `json.Marshal(effectiveConfig)` for `status.resolvedConfigHash`.
4. **Storage probe:** `Get` PVC by `spec.pvcName`; if missing set `StorageReady=False`, `RequeueAfter: 15s`. (The operator does not create PVCs.)
5. **Apply Deployment** via Server-Side Apply: `c.Patch(ctx, obj, client.Apply, client.FieldOwner("vllm-operator"), client.ForceOwnership)`. Construct the object with `TypeMeta` populated and **no `ResourceVersion`**. Only fields the operator owns are managed — external patches (sidecar injectors, user annotations) do not cause a fight. Spec is built in `internal/vllm/deployment.go` from EffectiveConfig.
6. **Apply Service** (NodePort) via SSA. Service name: `<instance-name>-svc`.
7. **Status:**
   - Read owned Deployment. Trust `readyReplicas` only when `dep.Status.ObservedGeneration >= dep.Generation`.
   - Mirror Deployment's `Available` condition into `DeploymentAvailable`, and its `Progressing` condition into `Progressing`.
   - Set `Ready=True` when `DeploymentAvailable=True` and `ReadyReplicas >= desiredReplicas`.
   - Populate `endpoint` from an InternalIP of any Node that currently hosts a Ready pod for this instance (look up via EndpointSlice for `<name>-svc`; fall back to first Ready node if list is empty).
   - **Always write** `status.ObservedGeneration = instance.Generation` at the end of every status update.

**Error handling convention:** Return `(ctrl.Result{}, err)` for API-server / transient errors (workqueue backoff). Use `RequeueAfter` only for "not an error, check back later" (missing PVC, unresolved preset). Never both.

### Deployment construction — parity checklist

Built in `internal/vllm/deployment.go`:

- `spec.replicas`: `spec.Replicas` if set, else 1
- `spec.progressDeadlineSeconds`: from preset — **always emit explicitly** (source YAMLs sometimes omit it and rely on k8s default=600, which is wrong for big models)
- `spec.strategy.type`: `Recreate`
- `spec.selector.matchLabels`: `{app: <name>}`
- Pod labels: `{app: <name>, model: <sanitize(modelID)>}` (parity with current YAMLs)
- Container name: `vllm`
- Ports: `[{name: http, containerPort: 8000, protocol: TCP}]`
- Args built in order: `--model`, `--dtype` (if set), `--quantization` (if set), `--port 8000`, `--max-model-len`, `--gpu-memory-utilization`, `--tensor-parallel-size` (if >1), `--enable-auto-tool-choice` (if true), `--tool-call-parser` (if set)
- Env: `HF_TOKEN` from `spec.hfToken` SecretKeySelector, `HF_HOME=/models/huggingface` (constant)
- Resources.limits: `{<migResource>: "<migResourceCount>"}`, no requests
- Tolerations: `[{key: <migResource>, operator: Exists, effect: NoSchedule}]`
- VolumeMounts: `/models` from PVC, `/dev/shm` from emptyDir
- Volumes: PVC `spec.pvcName`, emptyDir `{medium: Memory, sizeLimit: <shmSizeLimit>}`
- Probes: build from preset ProbeConfig; liveness `periodSeconds=30, failureThreshold=10` constants
- OwnerReference: VLLMInstance

Service: `type: NodePort`, port 8000 → targetPort 8000 (name `http`), `nodePort: spec.nodePort`, selector `{app: <name>}`. No `sessionAffinity`.

---

## RBAC

Two independent ClusterRoles — operator and mig-setup. Keeping them separate means the operator never holds `nodes/patch` or `pods/delete`.

**Operator ClusterRole** (ModelPreset + VLLMInstance are both namespaced, but the operator watches all namespaces — single instance serves everyone):

- `vllm.aatchison.io/modelpresets`: get, list, watch
- `vllm.aatchison.io/vllminstances`: get, list, watch, update, patch
- `vllm.aatchison.io/vllminstances/status`: get, update, patch
- `apps/deployments`: get, list, watch, create, update, patch, delete
- `"" /services`: get, list, watch, create, update, patch, delete
- `"" /persistentvolumeclaims`: get, list, watch (existence check only; does not create)
- `"" /nodes`: get, list, watch (for status.endpoint)
- `discovery.k8s.io/endpointslices`: get, list, watch (resolve Ready-pod node for endpoint)
- `"" /events`: create, patch

**MIG-setup ClusterRole** (used only by the one-shot Job, separate SA `vllm-mig-setup`):

- `"" /nodes`: get, list, patch
- `"" /pods`: get, list, delete — scoped via namespace `gpu-operator-resources` in the binding
- `"" /configmaps`: get — scoped to `kube-system` in the binding

---

## MIG Setup (out-of-band Job)

`config/mig-setup/` ships a bundle (five manifests, all in `kube-system`):

1. **`serviceaccount.yaml`** — SA `vllm-mig-setup`
2. **`clusterrole.yaml`** — permissions: `nodes: get,list,patch`; `pods: get,list,delete` in `gpu-operator-resources`; `configmaps: get` in own namespace
3. **`clusterrolebinding.yaml`** — binds the SA to the ClusterRole
4. **`configmap.yaml`** — sample with keys `nodeName` (auto-discovery default: first node with label `nvidia.com/mig.capable=true`), `configName` (e.g. `all-2g.48gb` or custom), `devicePluginLabelSelector` (default `app=nvidia-device-plugin-daemonset`), `timeoutSeconds` (default `240`)
5. **`job.yaml`** — image `bitnami/kubectl:1.29` (explicit `imagePullPolicy: IfNotPresent` — not `Never`, since microk8s will pull it once and cache; the operator image uses `Never` because it's locally built, but kubectl isn't). Runs a shell script that:
   - Reads the ConfigMap via `kubectl get cm` (SA has `configmaps: get`)
   - Pre-checks `nvidia.com/mig.capable=true` label on target node; fails fast with clear message if not present
   - Pre-checks `nvidia.com/gpu.deploy.mig-manager=true`; fails fast if GPU Operator isn't running mig-manager
   - Patches `nvidia.com/mig.config=<configName>` label on the node
   - Polls `nvidia.com/mig.config.state` every 5s until `success` or `timeoutSeconds` expires
   - On success: deletes device-plugin pods matching the selector so they re-register with new MIG slices
   - On failure: runs `kubectl describe node <nodeName>` and prints the mig-manager-related annotations/labels before exiting non-zero (so `kubectl logs job/mig-setup` has enough to diagnose)

**Idempotency:** `make mig-setup` does `kubectl delete job mig-setup -n kube-system --ignore-not-found` before apply (Job spec.template is immutable, so plain `apply` rejects re-runs). The ConfigMap + RBAC are plain-apply-safe.

**Failure mode:** Job fails → `kubectl logs job/mig-setup -n kube-system` shows the describe output. Users can then fix the ConfigMap (wrong configName, wrong node) and `make mig-setup` again.

---

## Operator Dockerfile + Makefile

Multi-stage: `golang:1.22` builder → `gcr.io/distroless/static:nonroot` runtime. Binary runs as UID 65532. `imagePullPolicy: Never` in manager.yaml, loaded into microk8s via `docker save | microk8s ctr image import`.

Makefile targets: `generate`, `manifests`, `build`, `test`, `install` (CRDs), `image-load`, `deploy`, `undeploy`, `mig-setup`, `apply-samples`.

---

## Implementation Order

1. `go.mod` + `groupversion_info.go`
2. `modelpreset_types.go` + `vllminstance_types.go` with all CEL validations
3. `make generate` → `zz_generated.deepcopy.go`
4. `make manifests` → CRDs + RBAC
5. `internal/vllm/{deployment,service,merge}.go` — pure functions, unit-testable
6. `vllminstance_controller.go` with SSA + field indexer + conditions
7. `main.go`
8. Seven ModelPreset YAMLs in `config/samples/presets/`
9. Seven VLLMInstance YAMLs in `config/samples/instances/` (plus dual/dual-moe/triple bundles as multi-CR files; CR names `e2b`, `e4b`, `moe`, `31b` keep pod labels matching the multi-deploy YAML `app:` selectors)
10. `config/mig-setup/{serviceaccount,clusterrole,clusterrolebinding,configmap,job}.yaml` and `config/storage/{pv-nfs.yaml,pvc-local.yaml}`
11. Trim `00-base.yaml` to namespace + Secret only
12. `make install` + `make deploy`
13. End-to-end verification

---

## Verification

```bash
# 1. One-time MIG setup (replaces setup-mig.sh) — `make mig-setup` handles the delete-then-apply
cd operator && make mig-setup
microk8s kubectl wait --for=condition=complete job/mig-setup -n kube-system --timeout=300s

# 2. Namespace + secret (hf-token)
microk8s kubectl apply -f 00-base.yaml

# 3. Storage — pick one
microk8s kubectl apply -f operator/config/storage/pv-nfs.yaml
microk8s kubectl apply -f operator/config/storage/pvc-local.yaml   # or this instead

# 4. Install operator + all presets
cd operator && make install && make deploy
microk8s kubectl apply -f config/samples/presets/

# 5. Single-instance parity: E2B
microk8s kubectl apply -f config/samples/instances/e2b.yaml
microk8s kubectl wait -n vllm vllminstance/e2b --for=condition=Ready --timeout=600s
ENDPOINT=$(microk8s kubectl get -n vllm vllminstance/e2b -o jsonpath='{.status.endpoint}')
curl "$ENDPOINT/models"

# 6. Multi-model parity: reproduce `deploy.sh triple`
microk8s kubectl apply -f config/samples/instances/e2b.yaml \
                      -f config/samples/instances/e4b.yaml \
                      -f config/samples/instances/31b.yaml
for n in e2b e4b 31b; do
  microk8s kubectl wait -n vllm vllminstance/$n --for=condition=Ready --timeout=1800s
done
./loadtest-all.sh   # existing script — NodePorts unchanged

# 7. Scale-down parity (replaces `deploy.sh undeploy`)
microk8s kubectl patch -n vllm vllminstance/e2b --type=merge -p '{"spec":{"replicas":0}}'

# 8. TP=2 parity
microk8s kubectl apply -f config/samples/instances/31b-bf16-tp2.yaml
microk8s kubectl wait -n vllm vllminstance/31b-bf16-tp2 --for=condition=Ready --timeout=2400s
```

---

## Key Hazards

- **NVIDIA GPU Operator coexistence:** The operator assumes GPU Operator is installed; the mig-setup Job cooperates with `nvidia-mig-manager` by setting `nvidia.com/mig.config` and waiting for the manager's `mig.config.state=success`. Running without GPU Operator requires replacing the Job with an alternative.
- **TP=2 scheduling non-determinism:** `migResourceCount: 2` does not guarantee slices land on different physical GPUs. For the current node layout (one 4g.96gb slice per GPU), this works; a mixed profile with multiple 4g.96gb on one GPU could break NCCL. Document as known limitation.
- **Server-Side Apply field manager:** Use a stable field manager name (`vllm-operator`) so ownership of fields is traceable. External tools patching the same fields will show up as conflicts via `kubectl get --show-managed-fields`.
- **Preset edits roll every referencing Deployment:** Changing a ModelPreset triggers re-reconciliation of every VLLMInstance referencing it (via field indexer), which rolls the Deployment. Presets are shared config, not snapshots — document clearly.
- **PVC deletion while instances run:** The operator checks PVC existence but doesn't gate deletion. Deleting the PVC while a VLLMInstance is Ready will eventually kill the pod. Acceptable — mirrors current manual behavior.
- **No multi-replica support:** Replicas capped at 1 (two pods can't share one MIG slice). Enforced via CEL validation.
- **Endpoint staleness on node churn:** `status.endpoint` is derived from a Ready node's InternalIP; if that node is cordoned/drained, the endpoint rotates on the next reconcile (triggered by the Node watch). NodePort reaches ANY node in the cluster, so the endpoint is a convenience, not a load-balanced VIP.
- **Field indexer carries its weight at small scale:** With ≤10 instances a list-and-filter on every preset change would also work; the indexer is retained for correctness (filters by namespace) and to match idiomatic controller-runtime patterns. Remove if benchmarking shows it's dead weight.
