# vLLM Operator

Declarative vLLM deployments on microk8s with NVIDIA MIG slices. Replaces the shell scripts and static YAMLs at the repo root.

Design doc: [`../docs/superpowers/specs/2026-04-16-vllm-operator-design.md`](../docs/superpowers/specs/2026-04-16-vllm-operator-design.md).

## CRDs

- `ModelPreset` (`vllm.aatchison.io/v1alpha1`) — reusable vLLM config (model, MIG resource, context length, probes). 7 presets ship in `config/samples/presets/`.
- `VLLMInstance` (`vllm.aatchison.io/v1alpha1`) — one instance = one Deployment + one NodePort Service. References a preset by name plus optional overrides.

## Quick start

```bash
# 1. MIG setup (one-time, cooperates with NVIDIA GPU Operator's mig-manager)
cd operator && make mig-setup
microk8s kubectl wait --for=condition=complete job/mig-setup -n kube-system --timeout=300s

# 2. Namespace + HuggingFace token (edit 00-base.yaml first)
microk8s kubectl apply -f ../00-base.yaml

# 3. Storage — pick one
microk8s kubectl apply -f config/storage/pv-nfs.yaml       # NFS (RWX)
# or
microk8s kubectl apply -f config/storage/pvc-local.yaml    # local hostpath (RWO)
# Multi-tenant clusters: see config/storage/pvc-shared-readonly.yaml and
# ../docs/multi-tenant-deployment.md for the shared-RO + per-tenant-RWO
# pattern. Sharing a writable PVC across tenants enables cross-tenant model
# poisoning (arbitrary code execution via poisoned weights / modeling_*.py).

# 4. Build image, load into microk8s, install CRDs, deploy operator
make image-load
make install
make deploy

# 5. Apply presets and sample instances
make apply-samples
microk8s kubectl wait -n vllm vllminstance/e2b --for=condition=Ready --timeout=600s
```

## Image publishing

CI publishes the operator image to `ghcr.io/aatchison/vllm-operator` on every push to `master` that touches `operator/`. Tags: short SHA only (the floating `latest` tag was dropped in PR #62 because it lets a deploy silently roll forward across breaking changes). `manager.yaml` pins to a digest; re-pin after each release. See issue #64 for rationale.

### Release flow (re-pinning manager.yaml)

After cutting a new operator image, update the digest in `config/manager/manager.yaml` so `make deploy` rolls forward:

```bash
# 1. Find the digest of the latest master build
gh api -H "Accept: application/vnd.github+json" \
  /users/aatchison/packages/container/vllm-operator/versions \
  | jq -r '.[] | select(.metadata.container.tags | length > 0) | "\(.metadata.container.tags[0])  \(.name)"' \
  | head

# 2. Replace the image: line in operator/config/manager/manager.yaml with
#    image: ghcr.io/aatchison/vllm-operator@sha256:<digest>
# 3. Commit and PR.
```

## Multi-tenant security

> ⚠️ **The `vllm-models-pvc` PVC must be per-tenant or per-trust-zone.** Never
> share `vllm-models-pvc` across mutually distrusting namespaces — tenant A
> can replace HuggingFace cache files in `/models` that tenant B loads on next
> pull, enabling arbitrary code execution via poisoned weights or
> `modeling_*.py`. For shared model caches, mount the cache PVC read-only by
> setting `pvcReadOnly: true` on the `VLLMInstance` / `LongContextInstance`
> spec, and pair it with a per-tenant RWO PVC for downloads. See
> [`../docs/multi-tenant-deployment.md`](../docs/multi-tenant-deployment.md) and
> [`config/storage/pvc-shared-readonly.yaml`](config/storage/pvc-shared-readonly.yaml).

## Security operations

This repo uses Dependabot in two distinct modes — **enable both**:

1. **Version-update PRs** (weekly cadence, configured in [`.github/dependabot.yml`](../.github/dependabot.yml)). These produce the routine "Bump foo from x to y" PRs (e.g. #52–#60). Already on.
2. **Security alerts + security updates** (real-time, configured in repo **Settings**, not in YAML). These fire when GitHub's advisory database flags a CVE in a transitive dependency, and produce out-of-band patch PRs that don't wait for the weekly window. **Currently OFF** — see issue #80.

To enable the security channel (maintainer-only — requires repo admin in the GitHub UI):

> **Settings → Code security and analysis** → enable **Dependabot alerts** + **Dependabot security updates**.

Verify after flipping:

```bash
gh api repos/aatchison/deploy-vllm-k8s/dependabot/alerts
# Expected: 200 with a (possibly empty) JSON array.
# While disabled: 403 "Dependabot alerts are disabled".
```

Issue #80 tracks this. The PR landing this section is documentation-only; the maintainer must flip the UI setting for the verification command above to return 200, at which point #80 closes.

## Makefile targets

| Target | What it does |
|---|---|
| `generate` | runs `controller-gen` to regenerate `zz_generated.deepcopy.go` |
| `manifests` | runs `controller-gen` to emit CRDs + RBAC into `config/` |
| `build` | `go build` the manager binary |
| `test` | `go test ./...` |
| `lint` | runs `golangci-lint` |
| `ci` | generate + fmt + vet + lint + test (full local CI) |
| `docker-build` | build operator image (`IMG` var, default `docker.io/library/vllm-operator:local`) |
| `image-load` | build + `microk8s ctr image import` |
| `install` | apply CRDs |
| `deploy` | apply RBAC + manager Deployment |
| `undeploy` | tear down manager + RBAC |
| `mig-setup` | delete-then-apply the MIG setup Job bundle |
| `apply-samples` | apply all presets + single-model instances |

## Parity with the old shell scripts

| Old | New |
|---|---|
| `./deploy.sh e2b` | `kubectl apply -f config/samples/instances/e2b.yaml` |
| `./deploy.sh triple` | `kubectl apply -f config/samples/instances/triple.yaml` |
| `./deploy.sh undeploy` | `kubectl delete vllminstance --all -n vllm` |
| `./setup-mig.sh` | `make mig-setup` |
