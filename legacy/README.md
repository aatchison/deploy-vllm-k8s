# Legacy scripts and manifests

These files predate the Kubernetes operator in `../operator/` and are kept here for reference only. They are **not maintained** and should not be used for new deployments.

| File | Replaced by |
|------|-------------|
| `deploy.sh` | `kubectl apply -f operator/config/samples/instances/<name>.yaml` |
| `setup-mig.sh` | `cd operator && make mig-setup` |
| `deploy-gemma4-*.yaml` | `operator/config/samples/presets/` + `operator/config/samples/instances/` |
| `deploy-dual.yaml` | `operator/config/samples/instances/dual.yaml` |
| `deploy-dual-moe.yaml` | `operator/config/samples/instances/dual-moe.yaml` |
| `deploy-triple.yaml` | `operator/config/samples/instances/triple.yaml` |

See the operator quick-start in `../operator/README.md` or the operator section of the root `README.md`.
