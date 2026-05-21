# vLLM Operator

Declarative vLLM deployments on microk8s with NVIDIA MIG slices. Replaces the shell scripts and static YAMLs at the repo root.

Design doc: [`../docs/superpowers/specs/2026-04-16-vllm-operator-design.md`](../docs/superpowers/specs/2026-04-16-vllm-operator-design.md).

## CRDs

- `ModelPreset` (`vllm.aatchison.io/v1alpha1`) — reusable vLLM config (model, MIG resource, context length, probes). 7 presets ship in `config/samples/presets/`.
- `VLLMInstance` (`vllm.aatchison.io/v1alpha1`) — one instance = one Deployment + one Service. References a preset by name plus optional overrides. Service type is configurable (`spec.serviceType`, default `ClusterIP`); see [Network policy](#network-policy).

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

CI publishes the operator image to `ghcr.io/aatchison/vllm-operator` on every push to `main` that touches `operator/`. Tags: short SHA only (the floating `latest` tag was dropped in PR #62 because it lets a deploy silently roll forward across breaking changes). `manager.yaml` pins to a digest; re-pin after each release. See issue #64 for rationale.

### Release flow (re-pinning manager.yaml)

After cutting a new operator image, update the digest in `config/manager/manager.yaml` so `make deploy` rolls forward:

```bash
# 1. Find the digest of the latest main build
gh api -H "Accept: application/vnd.github+json" \
  /users/aatchison/packages/container/vllm-operator/versions \
  | jq -r '.[] | select(.metadata.container.tags | length > 0) | "\(.metadata.container.tags[0])  \(.name)"' \
  | head

# 2. Replace the image: line in operator/config/manager/manager.yaml with
#    image: ghcr.io/aatchison/vllm-operator@sha256:<digest>
# 3. Commit and PR.
```

`make deploy` reapplies the CRDs in `config/crd/bases/` before rolling the manager Deployment (issue #110), so a release that introduces new CRD fields lands schema-first and avoids silently stripped fields on user CRs.

## Monitoring (Prometheus + Grafana)

`make deploy-monitoring` wires three ServiceMonitors and one Grafana dashboard onto a kube-prometheus-stack cluster:

| Resource | Path | Purpose |
|---|---|---|
| `config/prometheus/monitor.yaml` | operator metrics | controller-runtime reconcile / workqueue / leader stats from the operator binary |
| `config/prometheus/vllm-instances.yaml` | vLLM `/metrics` | per-pod serving stats: request concurrency, throughput, TTFT/ITL histograms, KV cache usage, prefix-cache hit rate |
| `config/prometheus/dcgm-exporter.yaml` | NVIDIA DCGM | per-GPU utilization, framebuffer, power, temperature, MIG profile labels — fills a gap in the stock gpu-operator ClusterPolicy which ships an SM that only scrapes the operator binary, never DCGM |
| `config/grafana/vllm-dashboard.json` | Grafana dashboard | two-row dashboard (GPU + vLLM serving) wrapped at apply time into a ConfigMap with the kiwigrid sidecar label `grafana_dashboard=1`; lands in the `vLLM` folder |

The vLLM ServiceMonitor selects every Service with `app.kubernetes.io/managed-by=vllm-operator` (stamped by `BuildService`), so new instances appear in the dashboard without any per-instance configuration.

### Cluster-side egress requirement

If your `monitoring` namespace runs with a default-deny NetworkPolicy and an explicit Prometheus egress allow-list, that allow-list must include the scrape ports:

- **8000/TCP** — vLLM serving + `/metrics` endpoint
- **9400/TCP** — DCGM exporter

Without these, Prometheus's SYN packets are dropped and the dashboard panels stay empty with `up == 0` for the scrape pools. The operator-side ServiceMonitor (port 8080) typically already works because Prometheus's own metrics endpoint shares that port.

### Override the Grafana namespace

The dashboard ConfigMap is applied to `monitoring` by default. Override for non-default monitoring stacks:

```bash
make deploy-monitoring GRAFANA_NAMESPACE=observability
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

## Network policy

Issue #75 changed the **default Service type to `ClusterIP`** (was `NodePort`)
and added an opt-in `spec.apiKey` field for HTTP authentication. ClusterIP +
NetworkPolicy + Ingress with TLS is the recommended production posture;
NodePort remains available as a dev-cluster shortcut and `LoadBalancer` is
available for cloud environments.

### Breaking change: ClusterIP default

Existing manifests that depended on the auto-NodePort exposure must add an
explicit serviceType:

```yaml
spec:
  serviceType: NodePort   # was implicit before #75 landed
  nodePort: 31000         # honored only when serviceType: NodePort
```

Without this change, `status.endpoint` will publish the in-cluster DNS form
(`http://svc-<name>.vllm.svc:8000/v1`) and external NodePort access will stop
working. `status.endpoint` is now derived from `spec.serviceType` directly;
issue #78's `vllm.aatchison.io/expose-node-ip` annotation has been superseded
and removed — set `serviceType: NodePort` to opt into the NodeIP form.

### API key authentication

vLLM does not enable per-request auth unless `--api-key` is set. Provide a
Secret and reference it via `spec.apiKey`:

```bash
kubectl -n vllm create secret generic vllm-api-key \
  --from-literal=token=$(openssl rand -hex 32)
```

```yaml
spec:
  apiKey:
    name: vllm-api-key
    key: token
```

The operator projects the Secret as a read-only file at `/var/run/vllm/api-key`
and exec-wraps the entrypoint so the key value never lands on the rendered
PodSpec (avoids the `kubectl describe` leak that env-var-based wiring would
introduce). Clients then send `Authorization: Bearer <token>`.

### NetworkPolicy templates

Three templates ship under `config/networkpolicy/`. They are **not** applied
by `make deploy` because they require a CNI that enforces NetworkPolicy
(Calico, Cilium, kube-router, Antrea, etc.). Apply explicitly:

```bash
make deploy-networkpolicy
```

| File | Purpose |
|---|---|
| `default-deny-vllm-ingress.yaml` | Denies all inbound traffic to pods in the `vllm` namespace. The floor; layered policies grant access by union. |
| `allow-from-ingress-controller.yaml` | Allows ingress from a designated ingress-controller namespace. **Customise the namespaceSelector** (default targets nginx-ingress in `ingress-nginx`). |
| `allow-egress-huggingface.yaml` | Allows egress on UDP/TCP 53 (DNS) and TCP 443 (HuggingFace Hub). Excludes RFC1918 ranges so a compromised pod can't pivot internally. |

On a cluster whose CNI does not enforce NetworkPolicy, these manifests apply
but nothing enforces them — silently insecure. Confirm enforcement before
relying on the policies.

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
| `deploy-networkpolicy` | apply the issue #75 NetworkPolicy templates (CNI-dependent — see [Network policy](#network-policy)) |

