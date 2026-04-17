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

# 4. Build image, load into microk8s, install CRDs, deploy operator
make image-load
make install
make deploy

# 5. Apply presets and sample instances
make apply-samples
microk8s kubectl wait -n vllm vllminstance/e2b --for=condition=Ready --timeout=600s
```

## Makefile targets

CI publishes the operator image to `ghcr.io/aatchison/vllm-operator` on every push to `master` that touches `operator/`. Tags: short SHA + `latest`.

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
