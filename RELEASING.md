# Releasing

This repository ships a Kubernetes operator (`ghcr.io/aatchison/vllm-operator`)
whose images are produced by two distinct GitHub Actions workflows. Releases
are explicit, low-cadence, and currently driven by hand — there is no
release-please / semantic-release automation.

## Image-publishing surface

| Workflow                                      | Trigger              | Image tags emitted                                      | Intended use                          |
|-----------------------------------------------|----------------------|---------------------------------------------------------|---------------------------------------|
| `.github/workflows/operator-image.yaml`       | push to `main`       | `<short-sha>` (e.g. `a8dcd45`)                          | continuous deploy / GitOps anchor     |
| `.github/workflows/release.yaml`              | push of `v*` tag     | `vX.Y.Z`, `X.Y.Z`, `X.Y`, `X`                           | versioned consumer pin                |

Both workflows publish to GHCR with the same supply-chain guarantees:

- multi-arch build via `docker/build-push-action`
- keyless `cosign` signature at the immutable digest (GitHub OIDC, no key material)
- SBOM + SLSA provenance attestations
- Trivy gate on CRITICAL / HIGH OS + library CVEs (unfixed ignored)

`release.yaml` is intentionally a near-copy of `operator-image.yaml`'s
`publish` job rather than a reusable workflow — see the comment block at the
top of `release.yaml` for the rationale (cross-workflow permission inheritance
footguns, single-file audit surface).

## Versioning policy

The operator follows [Semantic Versioning 2.0](https://semver.org/):

- **MAJOR** — CRD-breaking changes (field removed, field semantics changed in a
  way that requires manifests to be edited, controller reconcile behavior that
  silently changes existing object lifecycles).
- **MINOR** — backwards-compatible feature work (new CRD field with a sensible
  default, new Grafana panel, new optional knob on an existing preset).
- **PATCH** — bug fixes, dependency bumps, security patches with no observable
  behavior change.

Pre-1.0 (the current state), the MAJOR/MINOR distinction is advisory: breaking
changes may land in a MINOR bump if they are well-flagged in the release notes.
Once `v1.0.0` is cut, the contract above becomes binding.

## Cutting a release

Releases are tagged off `main`. The CI on `main` must be green before tagging.

```bash
# 1. Make sure you are at the commit you want to release.
git checkout main
git pull --ff-only

# 2. Pick a version. Look at the previous tag + Conventional Commits since:
git tag --list 'v*' | sort -V | tail -1
git log "$(git tag --list 'v*' | sort -V | tail -1)..HEAD" --pretty='%h %s'
#   - any `feat:` since the last tag  -> bump MINOR
#   - only `fix:` / `chore:` / deps   -> bump PATCH
#   - any breaking change             -> bump MAJOR (pre-1.0: bump MINOR + note)

# 3. Create an annotated tag. The message becomes the GitHub Release body
#    when promoted (see step 5).
git tag -a v0.1.0 -m "v0.1.0 — initial tagged release

Highlights:
- ...

See CHANGELOG.md for the full entry."

# 4. Push the tag. release.yaml fires on the tag push.
git push origin v0.1.0

# 5. (Optional) Promote to a GitHub Release.
gh release create v0.1.0 --notes-from-tag --verify-tag
```

`release.yaml` will, in order: log in to GHCR, build the operator image from
the tagged commit, push it under all four semver-shape tags, cosign-sign each
tag at the immutable digest, attach SBOM + provenance, and run Trivy. A
failure in any step aborts the workflow — the tag remains in the repo but the
release artifacts are incomplete. If that happens:

- inspect the workflow logs (`gh run list --workflow=release.yaml`),
- fix the root cause on `main`,
- delete the broken tag (`git tag -d vX.Y.Z && git push --delete origin vX.Y.Z`),
- re-tag the corrected commit.

Never re-use a tag at a different commit without first deleting the broken
GHCR images and signatures — `cosign verify` will fail closed for downstream
consumers if the digest under a tag silently changes.

## CHANGELOG

Maintain `CHANGELOG.md` per [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).
Add the entry **in the same commit** that creates the tag annotation, so the
release commit reflects the version about to be cut.

Conventional Commits already used in the repo (`feat:` / `fix:` / `chore:` /
`sec:` / `perf:` / `docs:` / `build:`) map cleanly to changelog sections:

| Commit prefix     | CHANGELOG section |
|-------------------|-------------------|
| `feat:`           | Added             |
| `fix:`            | Fixed             |
| `sec:`            | Security          |
| `perf:`           | Changed           |
| `chore:` / `build:` | Maintenance     |
| `docs:`           | Documentation     |

## Consumer pinning guidance

Downstream consumers (homelab ArgoCD, etc.) should pin to **one** of:

- an immutable digest — `ghcr.io/aatchison/vllm-operator@sha256:...` (strongest)
- a fully-qualified semver tag — `ghcr.io/aatchison/vllm-operator:v0.1.0`
- the major.minor floating tag — `ghcr.io/aatchison/vllm-operator:0.1` (only
  if you want automatic patch uptake)

The short-SHA tag (`a8dcd45`) is fine for in-house GitOps with commit-back
digest pinning, but is not appropriate for external consumers since it is not
discoverable without reading `main`. Do **not** pin to `latest` — it is
intentionally not published.

## Future automation

Once release cadence exceeds ~one tag per quarter, consider:

- [`release-please`](https://github.com/googleapis/release-please) — uses
  Conventional Commits to open a release PR per increment, owns
  `CHANGELOG.md`, cuts the tag on merge. Modest setup cost.
- [`semantic-release`](https://github.com/semantic-release/semantic-release) — fully
  automated; tag-on-merge-to-main. Higher cost: you lose the
  human-in-the-loop checkpoint that the current flow has.

Neither is justified at the current cadence (single-maintainer homelab).
