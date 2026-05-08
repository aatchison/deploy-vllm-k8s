# Multi-tenant deployment

This document describes the trust model for sharing model storage across
namespaces and the recommended pattern for clusters serving more than one
tenant or trust zone.

> ⚠️ This guide exists because `README.md` recommends a single
> `vllm-models-pvc` for all instances, and **that recommendation is unsafe
> across mutually distrusting tenants**. Single-tenant clusters can keep using
> the simple recipe; multi-tenant clusters must follow the pattern below.

## Threat model — cross-tenant model poisoning

A `VLLMInstance` mounts its model PVC at `/models` with the HF cache home set
to `/models/huggingface`. By default the mount is read-write (so vLLM can
populate the cache from HuggingFace on first pull).

If two tenants — say `tenant-a` and `tenant-b` — share the same writable
`vllm-models-pvc`, the following attack is straightforward:

1. Tenant A's pod (with full filesystem access to `/models`) replaces a file
   under `/models/huggingface/hub/<repo>/snapshots/<revision>/` — for example
   the `*.safetensors` weights, the `tokenizer_config.json`, or a custom
   `modeling_*.py`.
2. Tenant B's pod loads that model on next pull / next pod restart.
3. Tenant B's vLLM container executes attacker-controlled code:
   - **Poisoned safetensors** — a backdoored model that emits attacker-chosen
     outputs for trigger prompts. Subtle, hard to detect.
   - **Poisoned `tokenizer_config.json`** — alters tokenization in ways that
     change downstream behavior.
   - **Poisoned `modeling_*.py`** — when `trust_remote_code=True` (or any HF
     repo that ships custom modeling code), this is **arbitrary code
     execution** inside tenant B's pod, with the ServiceAccount's
     credentials.

The same attack applies in reverse, and to any third tenant on the same PVC.

This is not a bug in vLLM or in this operator — it's the unavoidable
consequence of giving every tenant write access to a filesystem every other
tenant reads from. The fix is at the storage layer.

## Recommended pattern — shared RO cache + per-tenant RWO download PVC

Two PVCs:

| PVC | Access mode | Owner | Purpose |
|---|---|---|---|
| `vllm-models-shared-ro` | `ReadOnlyMany` | trust-zone admin | Read-only cache of the models the trust zone has approved. Seeded out-of-band by the admin. |
| `vllm-models-<tenant>-rw` | `ReadWriteOnce` per tenant | tenant | Per-tenant download / scratch space. Untrusted across tenants but contained within the namespace. |

Tenants reference the shared cache and opt in to read-only mounting:

```yaml
apiVersion: vllm.aatchison.io/v1alpha1
kind: VLLMInstance
metadata:
  name: e2b
  namespace: tenant-a
spec:
  presetRef: {name: gemma-4-e2b}
  pvcName: vllm-models-shared-ro       # consumed read-only
  pvcReadOnly: true                    # opt-in: /models mounts ro
  hfToken: {name: hf-token, key: token}
```

`pvcReadOnly: true` produces a Pod with `readOnly: true` on the `/models`
VolumeMount, so even if the underlying PVC supports `ReadWriteMany` the
container itself cannot mutate any file. Tenant A poisoning tenant B's cache
becomes a kernel-enforced write to a read-only mount, which fails.

### Seeding the shared cache

The trust-zone admin pre-populates `vllm-models-shared-ro` from a controlled
environment, then flips its access mode to `ReadOnlyMany` for tenants. A
typical workflow:

1. Stand up a one-shot pod with the cache PVC mounted read-write under a
   trusted ServiceAccount.
2. Run vLLM (or `huggingface-cli download`) once per approved model, with the
   admin's HF token.
3. Tear the seeding pod down.
4. Reapply the PVC with `accessModes: [ReadOnlyMany]` if your storage class
   distinguishes the two — for NFS the same RWX backing volume is fine since
   the access enforcement happens at the mount.

A reference manifest lives at
[`operator/config/storage/pvc-shared-readonly.yaml`](../operator/config/storage/pvc-shared-readonly.yaml).

### What about HuggingFace tokens?

Each tenant still supplies its own `hfToken` Secret in its namespace. The
shared cache should be seeded under the *admin's* token (which decides which
gated models the trust zone has access to). Tenant tokens never need to write
to the shared cache because the cache is read-only at mount time.

## Single-tenant clusters

If you operate the whole cluster (or every tenant trusts every other tenant
with arbitrary-code-execution-equivalent privilege), the simple recipe in the
top-level `README.md` is fine — leave `pvcReadOnly` unset (default false) and
let vLLM populate the cache on first pull.

## What `pvcReadOnly` does NOT defend against

- **A compromised admin who controls the seeded cache.** If the admin's
  seeding pipeline pulls poisoned weights, every tenant gets them. The shared
  cache is only as trustworthy as the seeding process.
- **Tenants that share the same RWO scratch PVC.** `pvcReadOnly` is per-pod,
  not per-PVC; if you mount a writable PVC, anyone who can write to it can
  poison anyone who reads from it. Per-tenant RWO PVCs solve this.
- **Tenants with `trust_remote_code=True` against arbitrary HuggingFace
  repos.** The cache is one attack surface; arbitrary HF repos with malicious
  custom code are another. Pin model IDs and audit them out-of-band.
- **Side-channel attacks** between tenants sharing a node. MIG isolates GPU
  state but the host kernel is shared. Treat tenant boundaries as a
  defense-in-depth measure, not a hard isolation guarantee.

## Verification

After applying a `VLLMInstance` with `pvcReadOnly: true`:

```bash
kubectl get pod -n <tenant-ns> -l app=<instance-name> -o yaml \
  | yq '.items[0].spec.containers[0].volumeMounts[] | select(.name=="models")'
```

Expected output:

```yaml
mountPath: /models
name: models
readOnly: true
```

If `readOnly: true` is missing, the field did not propagate — re-check the
spec, regenerate CRDs, and reapply.

## Related issues

- [#76](https://github.com/aatchison/deploy-vllm-k8s/issues/76) — the parent
  issue documenting cross-tenant model poisoning.
- [#37](https://github.com/aatchison/deploy-vllm-k8s/issues/37) — pod-level
  hardening (runAsNonRoot, dropped capabilities). `pvcReadOnly` is a
  storage-layer complement to the pod-layer hardening landed in #37.
