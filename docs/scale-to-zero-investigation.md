# Scale-to-zero investigation — VLLMInstance / LongContextInstance

**Issue:** [#18](https://github.com/aatchison/deploy-vllm-k8s/issues/18)
**Date:** 2026-05-07
**Status:** Decision memo — investigation only, no implementation in this PR.

## TL;DR

**Verdict: defer.** Scale-to-zero is a real problem only on a cluster where the
2/2 MIG slices are oversubscribed *and* idle hours/day on at least one
long-running instance materially exceeds working-set time. This cluster has 2
slices and 1–3 active workloads; manual `kubectl scale --replicas=0` already
solves the rare case. The 3–15 minute cold-start floor on Gemma 4 31B NVFP4
also exceeds the request-buffering timeout of every off-the-shelf solution
(Knative activator: default 5 min, configurable up to 10 min hard ceiling;
KEDA HTTP Add-on interceptor: configurable but the project itself notes it
cannot yet be recommended for production), so any "transparent" wakeup needs
non-trivial custom plumbing. Re-open when the trigger conditions below are
met, and revisit once vLLM's CUDA-checkpoint RFC ([vllm#34303](https://github.com/vllm-project/vllm/issues/34303))
ships — that changes the math.

## Question 1. Triggering — what defines "idle"?

**The question (verbatim):** "Last-request timestamp on the
VLLMInstance/LongContextInstance status (controller polls), Knative Serving /
Activator pattern (extra component, intercepts traffic), KEDA ScaledObject with
custom metric (HTTP request rate from the Service), or vLLM-side metrics
endpoint scraped by the operator?"

**Options surveyed:**

1. **vLLM `/metrics` scrape from the operator.** vLLM exposes
   `vllm:request_success_total`, `vllm:num_requests_running`,
   `vllm:num_requests_waiting`. The operator already has the Service endpoint
   (`status.endpoint`) and could poll on a timer. No extra cluster components.
2. **KEDA ScaledObject pointing at the same Prometheus metric.** Requires
   Prometheus + KEDA installed. KEDA owns the replica decision via its scaler
   loop; we lose direct control of the desired-state→Deployment translation
   the operator currently centralizes.
3. **Activator-style traffic interception.** Either Knative or KEDA HTTP
   Add-on sits in front of the Service. Idle is "no in-flight requests for N
   seconds." Adds at least one extra component cluster-wide, and (see Q2 / Q6)
   the wakeup latency budget is the harder problem here.

**Recommendation:** If we ever build this, **vLLM `/metrics` scrape** is the
right fit. The operator already runs a reconcile loop, already knows the
endpoint, and the existing CRD pattern (`LongContextPreset` defaults +
`LongContextInstance` overrides, sibling-CRD style — see the project's
`feedback_sibling_crds_over_field_additions.md` lesson) gives us a clean place
to land an `idleTimeoutSeconds` field. Adding KEDA or Knative pulls in a whole
control plane for one knob; that's the wrong cost-shape for a 2-slice rig.

## Question 2. Wakeup latency budget

**The question (verbatim):** "Gemma 4 31B NVFP4 cold-start is ~3 min. Is that
acceptable for 'first-request after idle'? Or does the design need a
hot-spare / serverless-style request-buffering proxy?"

**Options surveyed:**

1. **Just accept the latency.** First-request-after-idle returns when the pod
   is ready; the client retries or waits. Works if the upstream is an
   interactive agent that can show a "warming up" state.
2. **Request buffering at an activator/proxy.** The proxy holds the TCP
   connection until the pod is ready, then forwards. Knative does this.
   Critical caveat: Knative's request timeout (`revision-timeout-seconds`)
   defaults to 5 minutes and can be raised to a **hard ceiling of 10
   minutes** [^1]. Our cached-cold-start floor (3 min) fits this
   comfortably; the uncached worst-case (~15 min) does not. So
   Knative-style transparent activation works only for the cached fast path;
   users hitting first-time download still need a different mechanism.
   KEDA's HTTP Add-on can stretch the wait window beyond Knative's, but the
   project itself notes it **cannot yet be recommended for production
   use** [^2], with maintainers assisting but not directly responsible for
   the codebase.

[^1]: knative/serving `pkg/apis/config/defaults.go` — search for `RevisionMaxTimeoutSeconds`.
[^2]: https://github.com/kedacore/http-add-on — see project README's status note.
3. **CUDA checkpoint/restore (vLLM RFC).**
   [vllm#34303](https://github.com/vllm-project/vllm/issues/34303) proposes
   freezing GPU state via `cuCheckpointProcess*` (driver 570+) so weights,
   compiled kernels, CUDA graphs, and `torch.compile` artifacts survive
   suspend. Modal has shown 10× improvement in similar setups; round-trip
   compresses from ~6–32s warm-restart to ~4–10s. **Status: draft RFC, not
   implemented.** The RFC operates at the CUDA driver level and is
   format-agnostic with respect to weight quantization. The RFC itself flags
   two open risks: **UVM allocation compatibility** (whether the checkpoint
   mechanism covers all memory pools vLLM uses) and **NCCL process-group
   re-init cost** when `tensorParallelSize > 1`. Both are tractable but
   unresolved as of the RFC. For our single-slice north-star workload, NCCL
   re-init is moot; UVM compatibility is the load-bearing question.

**Recommendation:** Today's cold-start floor (~3 min model-cached, up to ~15
min uncached) is in the wrong order of magnitude for any off-the-shelf
"transparent" wakeup. If we build scale-to-zero now, the design must be
**explicit**: the user/agent triggers the wakeup (e.g. by `kubectl patch
lci/foo --replicas=1` or a `wake` subresource) and waits, instead of pretending
the first request will just work. This is an honest UX given the hardware
reality.

## Question 3. State preservation (KV-prefix-cache)

**The question (verbatim):** "KV-prefix-cache (in-process) is lost on
scale-to-zero. Does that matter for our north-star workload (long-context
agents with shared system prompts)? If yes, does the LMCache sidecar from #13
give us scale-to-zero with cache survival via host-RAM tier?"

**Options surveyed:**

1. **Accept cache loss.** First N requests after wakeup pay full prefill cost
   on the shared system prompt; after that the in-process radix cache rebuilds.
   For long-context agents this is meaningful — a 100K-token shared system
   prompt prefill can be tens of seconds.
2. **LMCache sidecar (issue #13 / #20–#22).** External KV store, host-RAM
   tier-1, persists across vLLM process restarts. If the sidecar stays alive
   when vLLM scales to zero, prefix cache survives and post-wakeup TTFT on
   shared prompts is preserved.

**Recommendation:** Cache loss matters for our north-star (long-context agents
share large system prompts). But this is the long pole *only* if we've
already accepted the multi-minute cold-start — which is itself the bigger
problem. **LMCache as the cache-survival path is plausible but introduces a
chicken-and-egg in the deployment topology**: if vLLM and the LMCache sidecar
are in the same pod (the intended #21 design), scaling the Deployment to 0
kills both. To preserve cache across scale-to-zero, LMCache would need to be a
separate StatefulSet/Pod that vLLM reconnects to on wakeup — a bigger refactor
than #13's current shape. Practical answer for this memo: **scale-to-zero +
LMCache cache survival is a co-design problem, not an additive feature**.
Closing #13 as currently scoped does not unlock it.

## Question 4. MIG slice reclamation

**The question (verbatim):** "When replicas=0, does k8s actually free the MIG
resource immediately so other pods can claim it, or does the GPU Operator hold
the device until the Deployment is deleted?"

**Investigation:** The `Deployment` with `replicas: 0` produces zero
ReplicaSets/Pods. The NVIDIA device plugin allocates `nvidia.com/mig-*`
resources at the **pod level**, not the Deployment level — when the pod
terminates, the device plugin returns the slice to the allocatable pool on
the next kubelet update. Verified mechanically by reading the operator's
`deployment.go`: the MIG resource is in `Resources.Limits` on the container
spec; nothing pins the device at the Deployment object level. The
`LongContextInstanceSpec` already validates `replicas: 0|1`, so the API
surface is ready.

**Recommendation:** No reclamation problem exists. `replicas=0` is sufficient
to free the slice. The cluster-side risk is the inverse: the slice may be
*claimed by another pod before the wakeup pod can re-pull it*, which is fine
(that's the whole point) but means scale-up may now `Pending` until the
peer instance is itself scaled down. This is acceptable; the user signed up
for it by enabling scale-to-zero.

## Question 5. Operator model — field vs. sibling CRD

**The question (verbatim):** "Does scale-to-zero belong as a field on
LongContextPreset (e.g. idleTimeoutSeconds) with the operator polling, or as a
separate sibling controller/CRD that wraps existing instances?"

**Options surveyed:**

1. **Field on `LongContextInstance.spec`** (`idleTimeoutSeconds`,
   `wakeupStrategy`). Smallest blast radius. Reuses the existing reconciler.
2. **Field on `LongContextPreset.spec`** (preset-level default). Risky: the
   preset is the shareable, low-risk artifact; idle behavior is per-instance
   policy, not a model property.
3. **Sibling CRD (`IdlePolicy` or `ScaleToZeroPolicy`)** that references an
   existing `LongContextInstance` by name and runs its own controller. Maps
   cleanly onto the project's preferred sibling-CRD pattern
   (`feedback_sibling_crds_over_field_additions.md`) and keeps the
   long-context reconciler unchanged.

**Recommendation:** **Sibling CRD if we ever build this.** Reasoning:

- The current `LongContextInstance` reconcile loop is small and focused
  (resolve preset → SSA Deployment → SSA Service → mirror conditions).
  Adding "poll vLLM /metrics → patch replicas → react to wakeup signal"
  changes the loop's character (now it's two timers instead of event-driven)
  and the test surface meaningfully.
- Idle policy is orthogonal to long-context tuning. Other instance types
  (`VLLMInstance`) might want it too. A sibling controller naturally serves
  both.
- The `IdlePolicy` CRD can be installed/uninstalled independently — the user
  who doesn't need scale-to-zero pays no cost.
- Aligns with the documented project preference.

Field placement is the tertiary question; the operator-shape question is the
primary one, and sibling-CRD wins.

## Question 6. Wakeup wiring

**The question (verbatim):** "When scaled to 0, the NodePort Service has no
Endpoints. How does the first request reach the operator (or a proxy) to
trigger scale-up?"

**Options surveyed:**

1. **Activator proxy (Knative-style).** Highest UX (transparent wakeup) but
   blocked by Knative's 10-minute hard ceiling on `revision-timeout-seconds`
   vs. our 15-minute worst-case cold-cache. The pattern doesn't fit our
   hardware for uncached cold starts.
2. **Always-on doorbell pod.** A tiny pod in front of the Service that, on
   any inbound request, patches `replicas=1` on the owning instance and
   long-polls for ready. Cheap in resource cost (no GPU); fragile in the
   single-replica case (the doorbell itself is now an SPOF). Equivalent to
   building a mini-activator without a 5-minute timeout cap.
3. **Explicit wakeup (no proxy).** No request interception. The user/agent
   issues `kubectl patch lci/foo` (or a `wake` subresource exposed by the
   operator) before sending traffic. The Service is unreachable while
   replicas=0; that's a feature, not a bug — it surfaces the cold-start cost
   honestly.
4. **Gateway API hook.** A controller-side Gateway extension that triggers
   wakeup. More platform-heavy than the doorbell, equivalent in semantics.

**Recommendation:** If we build, do **explicit wakeup** first
(`kubectl patch` is fine; a `wake` action subresource is sugar). The doorbell
pod is plausible follow-on once we have telemetry on how often it's actually
needed. Activator-class transparent wakeup is the wrong target until vLLM
cold-start drops well below the 10-minute ceiling — i.e. post-CUDA-checkpoint
with UVM compatibility confirmed.

## Question 7. Cost vs. operational complexity

**The question (verbatim):** "For a 2-slice cluster with 1–3 active
workloads, does the saved-GPU-hour math justify the architectural
complexity?"

**Investigation:**

- **2 MIG slices, max.** Even at 100% scale-to-zero coverage, there are at
  most 2 slices to reclaim.
- **Active workloads: 1–3.** The bottleneck is the slice count, not the
  Deployment count; today, when slice 2 is needed, it's freed via
  `kubectl delete lci/foo` or `kubectl scale --replicas=0`. Manual.
- **Idle hours required to break even.** A sibling controller + CRD + e2e
  tests is roughly 200–400 LOC of Go and 1–2 weeks of build/review/iterate
  including the reactive #18-implementation issue, follow-ups for edge cases,
  and the doorbell follow-up if it's needed. Vs. saved GPU-hours: we own the
  GPUs (no $/hour pressure), so "saved" is "available for another tenant",
  i.e. an opportunity cost only if there's *a queued tenant*. Today there
  isn't reliably.
- **Hidden complexity tax.** Every CRD adds a status surface, a CRD
  upgrade/migration path, sample manifests, BENCHMARKS notes, and
  potentially a webhook. Sibling-CRD is the cheapest version, still not
  free.
- **Precondition: ClusterRoleBinding.** Any new CRD/controller widens the
  RBAC surface; today's `vllm-operator-role` is missing its
  `ClusterRoleBinding` (issue [#23](https://github.com/aatchison/deploy-vllm-k8s/issues/23)).
  Scale-to-zero work cannot ship cleanly until #23 lands.

**Recommendation:** Complexity does not justify the savings on the current
hardware/utilization profile. Re-evaluate when (a) the cluster grows, or (b)
utilization shifts toward "many idle hours per day on long-running
instances".

## Verdict: defer

**Defer**, with explicit re-trigger conditions:

1. **Hardware grows past 4 MIG slices** (e.g. add a third Blackwell, enable
   2-slice MIG profiles, or both). At that point manual scale-down stops
   being tractable and per-instance idle policy starts paying for itself.
2. **Idle hours/day on the longest-running instance crosses 8h sustained**
   for 2+ weeks. Track this via the existing `vllm:request_success_total`
   counter — if rate is ~0 for 8h+ daily on a hot-cache instance, we're
   paying real opportunity cost.
3. **vLLM CUDA-checkpoint RFC ships** ([vllm#34303](https://github.com/vllm-project/vllm/issues/34303))
   *and* validates on our hardware — specifically, UVM allocation
   compatibility is confirmed and the round-trip cold-start drops to
   single-digit seconds. That makes a Knative-class transparent-wakeup
   design viable, which changes the verdict to "build".
   Independent of the cluster-growth condition.

Until then: `kubectl scale --replicas=0 deployment/<name>` (or
`kubectl patch lci/<name> --type=merge -p '{"spec":{"replicas":0}}'`) is the
official manual operation. Document it in the README as the supported way to
free a slice without deleting the instance — this is a **tiny doc-PR-sized
follow-up**, not part of this memo.

## Implementation skeleton (only if verdict flips to "build")

Captured here so a future reader doesn't restart cold. **Do not file as an
issue yet** — scope creep risk if the verdict turns out to be reject.

**Phase 1 (types-only, atomic):**
- New CRD: `IdlePolicy` (`vllm.aatchison.io/v1alpha1`,
  `operator/api/v1alpha1/idlepolicy_types.go`).
  Fields: `targetRef` (kind=`LongContextInstance|VLLMInstance`, name),
  `idleTimeoutSeconds` (min=60), `wakeupStrategy` (enum:
  `manual|doorbell`, default `manual`),
  `metricsSource` (enum: `vllmRequestRate`, default; reserved for future
  scalers).
- Status: `lastObservedActivity` (timestamp), `currentPhase`
  (`active|idle|scaled-to-zero|waking`), conditions.

**Phase 2 (controller, atomic):**
- New file: `operator/controllers/idlepolicy_controller.go`. Polls the
  target instance's `status.endpoint` for vLLM `/metrics`; if
  `vllm:request_success_total` rate is 0 for `idleTimeoutSeconds`, patches
  the target's `spec.replicas=0`. On any non-zero observation, patches
  back to 1. (Note: the watcher needs a clamp — don't fight a user who
  manually set 0.)
- Reuse existing `setCondition` helper pattern from
  `longcontextinstance_controller.go`.

**Phase 3 (samples + BENCHMARKS + README, atomic):**
- One sample manifest under `operator/config/samples/` showing
  `IdlePolicy` referencing an existing `LongContextInstance`.
- BENCHMARKS note: cold-start re-warm time on a known model.
- README: explicit-wakeup workflow + reclaim semantics.

**Phase 4 (optional, gated on usage signal):**
- Doorbell sidecar pod + `wakeupStrategy: doorbell` enabling
  on-traffic auto-wakeup. Defer to second issue.

**Hard prerequisites before Phase 1:**
- Issue [#23](https://github.com/aatchison/deploy-vllm-k8s/issues/23)
  (ClusterRoleBinding) merged. Without it the new controller will silently
  work on dev microk8s and silently break on hardened clusters.

## Suggested follow-up issues (do not file from this PR)

- "Document `kubectl scale --replicas=0` as the supported manual reclaim"
  — README + maybe a `make reclaim NAME=foo` target. Doc-only PR, ~30 lines.
- "Wire `LongContextInstance.status.lastRequestTime` from vLLM /metrics
  scrape" — purely observational, not an action; would let us *measure* the
  idle-hours/day trigger condition without committing to scale-to-zero. ~150
  LOC + tests.
- "Track vllm CUDA-checkpoint RFC ([vllm#34303](https://github.com/vllm-project/vllm/issues/34303))
  until UVM compatibility and NCCL re-init questions are resolved" — meta-issue, no work, just a watching brief.

---

## References

- [KubeAI autoscaling concepts](https://www.kubeai.org/concepts/autoscaling/) — the request-queueing-during-cold-start design.
- [Self-Hosting LLMs in Production: vLLM + KubeAI Stack (Fadhel)](https://mfadhel.com/vllm-deepseek/) — production cold-start observations; "two-thirds of GPU budget on idle pods" anecdote.
- [Knative Serving scale-to-zero docs](https://knative.dev/docs/serving/autoscaling/scale-to-zero/) and [knative/serving `pkg/apis/config/defaults.go`](https://github.com/knative/serving/blob/main/pkg/apis/config/defaults.go) (`RevisionMaxTimeoutSeconds`) — default 5 min, 10 min hard ceiling, relevant to our cold-start budget.
- [KEDA HTTP Add-on README](https://github.com/kedacore/http-add-on) and [interceptor configuration](https://keda.sh/http-add-on/0.14/operations/configure-interceptor/) — interceptor `waitTimeout`, beta status.
- [vLLM CUDA Checkpoint/Restore RFC #34303](https://github.com/vllm-project/vllm/issues/34303) — the cold-start floor likely changes here.
- Project memory: `feedback_sibling_crds_over_field_additions.md`,
  `feedback_atomic_small_prs.md`.
- Related repo issues: [#13](https://github.com/aatchison/deploy-vllm-k8s/issues/13) (LMCache sidecar), [#23](https://github.com/aatchison/deploy-vllm-k8s/issues/23) (RBAC binding precondition).
