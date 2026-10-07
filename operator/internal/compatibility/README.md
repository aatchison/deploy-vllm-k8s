# Forgejo operator port verification

Refs #182. GitHub baseline: `a60095486ed01280fe41f84f803a8d2e27a4ad0b`.
Forgejo source: `aeaf88a4fe4fa6fba886d6c2f89e8f5e01ba5cbd`.

## Source commit accounting

| Feature | Forgejo source commits | GitHub disposition |
|---|---|---|
| Reasoning parser | [`1d53aa9`](https://forgejo.atchison.io/aatchison/deploy-vllm-k8s/commit/1d53aa9a44ea024e93f0898731ac515b673e9e84) | `a104011`. Ported fields and flags; retained GitHub security changes from the mixed source commit. |
| Speculative config | [`d3b949b`](https://forgejo.atchison.io/aatchison/deploy-vllm-k8s/commit/d3b949b628e63c5a9fa26fa90a28f1e5db33e578), [`f181feb`](https://forgejo.atchison.io/aatchison/deploy-vllm-k8s/commit/f181feb1c4751c9807be9ed86f1aea4a488339b6), [`8337430`](https://forgejo.atchison.io/aatchison/deploy-vllm-k8s/commit/8337430b34a5cd61e1f6bbf075a92256d2093f11) | `8e8d849`. Ported the completed field/override/flag implementation. |
| Chat template | [`d64dd59`](https://forgejo.atchison.io/aatchison/deploy-vllm-k8s/commit/d64dd590a43b0846c376965671da043469013b4a), [`1b99346`](https://forgejo.atchison.io/aatchison/deploy-vllm-k8s/commit/1b993461a900d2cdc2697746e11ff1bc42051e67) | `8cfcbd2`. Ported preset/override fields and flag. |
| Dependency bumps | [`470ec0a`](https://forgejo.atchison.io/aatchison/deploy-vllm-k8s/commit/470ec0a3ce653de7d976e151e7caf918a490f0d1), [`da37627`](https://forgejo.atchison.io/aatchison/deploy-vllm-k8s/commit/da37627763202120206b362e52f1e811908b7166), [`4f3e545`](https://forgejo.atchison.io/aatchison/deploy-vllm-k8s/commit/4f3e545a50625b3092a112dc6fcfb4a3f2cdcc8a) | `a600954`. Already present: x/net v0.56.0, x/text v0.39.0; go get/tidy left no diff. |
| Multimodal limits | [`dfe290d`](https://forgejo.atchison.io/aatchison/deploy-vllm-k8s/commit/dfe290dc6f8d218f78cbca00c7a0a69c670abd7e), [`8be0a43`](https://forgejo.atchison.io/aatchison/deploy-vllm-k8s/commit/8be0a433eecfea445cb09874d87111250f43a86c), [`36eed76`](https://forgejo.atchison.io/aatchison/deploy-vllm-k8s/commit/36eed764867be946b99aa5d5b6842aa002a5b77b) | `1749524`. Ported raw value and composed renderer. |
| Scheduler concurrency | [`0653284`](https://forgejo.atchison.io/aatchison/deploy-vllm-k8s/commit/06532844e1d386f391221832235290e0d0606d0e), [`b3c1352`](https://forgejo.atchison.io/aatchison/deploy-vllm-k8s/commit/b3c135215d397362c07fec22522456e6fda8e31a) | `99fec25`. Ported maxNumSeqs to --max-num-seqs. |
| KV cache layer exclusions | [`1320d80`](https://forgejo.atchison.io/aatchison/deploy-vllm-k8s/commit/1320d8071dfdb0d115dca6f9f08bdcb733d61d9c) | `fb20905`. Ported field and flag. |
| Serving preset churn | [`fcd440f`](https://forgejo.atchison.io/aatchison/deploy-vllm-k8s/commit/fcd440f11e61142847426681ffc4f69367242355), [`df98752`](https://forgejo.atchison.io/aatchison/deploy-vllm-k8s/commit/df98752ad4f5e4cefb63637722504040c1a22c0d), [`bc65200`](https://forgejo.atchison.io/aatchison/deploy-vllm-k8s/commit/bc65200ba630a2e64fa574409f134e0c93a63510) | Excluded by brief: image/checkpoint preset changes. |
| Go toolchain | [`90206b1`](https://forgejo.atchison.io/aatchison/deploy-vllm-k8s/commit/90206b1b2070fb3650eabcfee10b009714ba5b98) | `a600954`. Already present: toolchain go1.26.7. |
| Mamba flags | [`52d308e`](https://forgejo.atchison.io/aatchison/deploy-vllm-k8s/commit/52d308e364c007c890be3c90976df7065352730e) | `beb53f5`. Ported fields, enum, override precedence, flags, and tests. |
| Eager and compilation config | [`c42f97a`](https://forgejo.atchison.io/aatchison/deploy-vllm-k8s/commit/c42f97aebd9074871934922c8189f2568ce8e2dd) | `1663b7e`. Ported pointer shapes, nullable eager bool, raw config, and tests. |
| LoRA live schema | [`f168397`](https://forgejo.atchison.io/aatchison/deploy-vllm-k8s/commit/f16839728d9f4f8b13fc594c0c548ff3fbbde51f), [`99f6a4e`](https://forgejo.atchison.io/aatchison/deploy-vllm-k8s/commit/99f6a4e16e2f1e3928860b2479bb82492ee62d95), [`c2e2c27`](https://forgejo.atchison.io/aatchison/deploy-vllm-k8s/commit/c2e2c27f6073878bad6f00dcee1fa5923bab1e0f) | `97386e0`. Ported *bool and *int preset fields, ModelPreset defaults, enabled-only options, and schema/tests; follow-up c8f0ddd aligns descriptions/vectors. |
| Environment passthrough | [`f8eff28`](https://forgejo.atchison.io/aatchison/deploy-vllm-k8s/commit/f8eff284f6b51695d7dc82beee2dcd1e6a4da9aa) | `6016516`. Ported EnvVar lists, merge order/deep copies/hash/controller wiring and tests; docs in c8f0ddd. |
| Two MIG replicas | [`59cea23`](https://forgejo.atchison.io/aatchison/deploy-vllm-k8s/commit/59cea23527ce830e7eeebb6a192ac0b943e182d9) | `a600954`. Already supported. Kept sharedStorage CEL and controller PVC access-mode safety. |
| Go builder | [`14f3e9e`](https://forgejo.atchison.io/aatchison/deploy-vllm-k8s/commit/14f3e9e6f1dfa4db0875f856a8fc6be256bef793) | `ccc3a36`. Ported digest-pinned golang:1.26.8; kept cache_scope.go build inclusion. |

## Compatibility results

The saved corpus has 22 LongContextPresets, nine LongContextInstances, eight
ModelPresets, and zero VLLMInstances. All 39 CRs pass generated-schema OpenAPI,
CEL, enum, and pattern validation. Structural pruning loses no saved fields.
**Rejected CRs: none.** The gate uses Kubernetes apiextensions libraries offline,
not the live API server. It checks defaults and includes enum, pattern, CEL,
fractional-integer, and exact-allowlist negative controls.

All nine LongContextInstance/preset pairs have equal full Deployment and Service
JSON under Forgejo `aeaf88a4fe4fa6fba886d6c2f89e8f5e01ba5cbd` and the port.
Args retain their original order. Env, resources, volumes, pod/container security
contexts, and Services match. There are no live VLLMInstance/ModelPreset pairs;
ModelPresets receive schema coverage and synthetic LoRA/flag tests.
**Intended render differences for saved live pairs: none.** The eight baseline
render differences were missing ported flags/env, not security exceptions.

The oracle was produced by adding the same small Go harness to a scratch
checkout of Forgejo `aeaf88a` and running `go test`. The parent independently
re-ran that harness on Forgejo and the port. Both JSON files have SHA-256
`d5f8e051cab8fb0d3b5274666de59bbbf5615b070589ca6dbf6c4bac7a744d28`.
Normal `go test ./...` runs the strict comparison; there is no bypass or broad
ignore. The corpus and oracle are committed in `testdata/`.

## Live/generated CRD diff

Both preset CRDs have no semantic differences from their live fixtures.
Neither instance CRD has a live-only field. Each instance CRD has these four
generated-only fields:

- `spec.sharedStorage`: boolean. GitHub's retained multi-replica storage acknowledgement.
- `spec.overrides.enableLora`: boolean. Retained GitHub per-instance LoRA override.
- `spec.overrides.loraModules`: string. Retained GitHub per-instance LoRA override.
- `spec.overrides.maxLoraRank`: integer, int32, minimum 1. Retained GitHub override.

Each instance CRD also retains five stricter override constraints from #177:

- `migResource`: `^nvidia\.com/mig-[0-9]+g\.[0-9]+gb$`.
- `migResourceCount`: minimum 1, maximum 8.
- `gpuMemoryUtilization`: `^0?\.[0-9]+$|^1\.0$`.
- `shmSizeLimit`: `^[0-9]+[KMGT]i?$`.

Both retain the exact CEL rule requiring `sharedStorage=true` for replicas >1.
That is ten semantic differences per instance CRD: four added properties, five
constraint keys, and one added CEL rule. All allowances are exact path/value
matches; altered types and limits fail the test. No live CR is rejected by these
retained safeguards. Descriptions are excluded from semantic schema comparison.

## Additive fields from #178

The schema gate also allows exact generated-only definitions for
`modelRevision`, `codeRevision`, `tokenizerRevision`, `trustRemoteCode`,
`engramConfig`, `disableCustomAllReduce`, and `flashinferAutotune` on the two
long-context CRDs. `maxNumSeqs` gains an instance override with the preset's
minimum 0 contract. The ported preset definition is unchanged.

These are named path/value allowances, not changes to the saved live schemas
or render oracle. Negative controls reject changed types, patterns, defaults,
limits, CRD names, field names, and changes to existing live fields.

## Security, migration, and rollback

The port keeps #177's shared-memory parsing, MIG validation, namespace-scoped
informers, leader-lease RBAC, IPv6 endpoints, LoRA path validation, API-key/HF-token
handling, and pod security settings. It keeps GitHub's controller checks for
RWX or read-only ROX storage with two replicas. Invalid override MIG names remain
rejected, even though the live override schema has no pattern.

LoRA preset types now match Forgejo: `EnableLora *bool`, `MaxLoraRank *int`.
ModelPreset defaults are false and 64. LoRA option flags are emitted only when
LoRA is enabled. Existing GitHub LoRA overrides remain available. Some resolved
config hashes can change because new fields/defaults now participate; this may
trigger reconciliation without changing a saved live Deployment.

Env passthrough is trusted workload configuration. It can override built-in env
names and reference same-namespace Secrets. Namespace isolation/admission policy
is needed for untrusted authors; the README states this limitation.

This PR does not deploy CRDs or rebuild/publish an image. When deploying later,
apply the generated CRDs before using the new fields. For rollback, keep the
expanded CRDs and return to the prior operator image. Reverting the CRDs would
prune the ported fields on re-apply.

## Scope left out

All 27 non-merge Forgejo-only commits touching `operator/` are accounted for in
the table. Serving-image/checkpoint preset changes are excluded by the brief.
The Go toolchain, x/net/x/text versions, and two-replica support already exist on
GitHub, so they were not duplicated or downgraded. Workflow/registry publishing
belongs to another lane. PRs #180 and #181, root serving-image code, unrelated
docs, and forgejo-experiments are untouched. No cluster writes, image builds,
image pushes, or Forgejo pushes were performed.

## Reproduce

From `operator/`:

```text
make generate manifests fmt vet test
golangci-lint run ./...
go install golang.org/x/vuln/cmd/govulncheck@v1.3.0
govulncheck ./...
go test ./internal/compatibility -count=1 -v
```

Use controller-gen v0.20.1 and golangci-lint v2.12.2, as pinned by
`.github/workflows/operator-image.yaml`. The workflow runs
`make generate fmt vet test`; this lane also ran `manifests` and checked that
regeneration left the tracked tree clean. Local tests used Go 1.26.8.

To recreate the oracle, check out Forgejo `aeaf88a` in a scratch worktree. Copy
`render_equivalence_test.go` and the saved CR/oracle fixtures into the same package.
Then run `VLLM_RENDER_DUMP_DIR=<disk-backed-output> go test
./internal/compatibility -run TestRenderLive -count=1 -v`. This is read-only with
respect to the cluster and uses no live objects or Secrets.

The field-type, raw-field wiring, schema, and strict render controls were red
on baseline `a600954` before the port. Missing fields/types and lost schema data
caused the schema failures. Eight of nine baseline renders differed because of
missing reasoning/Mamba/speculative/multimodal/concurrency/compilation flags or
env. The port makes those controls pass. Eager mode, env deep copies/precedence,
and LoRA disabled-state behavior also have focused synthetic tests.
