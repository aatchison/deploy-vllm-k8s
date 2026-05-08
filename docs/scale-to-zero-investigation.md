# Scale-to-zero investigation memo

Tracking issue: [#18](https://github.com/aatchison/deploy-vllm-k8s/issues/18). This is a decision memo, not a spec — implementation is out of scope until a follow-up is filed.

## Problem

vLLM cold-start on this hardware runs 3–15 minutes: weight load over PCIe, KV-cache allocator setup, and cudagraph capture all happen before the first token. While the model is idle, a 31B-NVFP4 workspace still pins a `4g.96gb` MIG slice. On a 2-slice cluster, an idle workspace consumes 50% of the GPU pool — the slice cannot host another tenant or experiment until the operator deletes the `VLLMInstance` / `LongContextInstance`. The opportunity cost is real but bounded: at most one slice, on one node.

The question this memo answers: should the operator grow a "scale to zero on idle" mode, and if so, how?

## Approaches surveyed

### 1. Operator-native idle timer

The controller writes `lastRequestTime` into the CR status (scraped from vLLM's `/metrics` endpoint), and after `idleTimeoutSeconds` it patches the managed `Deployment` to `replicas: 0`. Wakeup is the hard part: with zero pods, the `Service` has no endpoints, so the next request has nowhere to land.

- Pros: stays inside the operator boundary; no new control-plane dependency; idle policy is a CR field, easy to reason about.
- Cons: wakeup wiring is non-trivial. Need either an Activator-style request-buffering proxy or an always-on "doorbell" pod that signals the operator. Either path is real controller work.
- Cold-start cost: full 3–15 min on first request after idle, unless paired with LMCache.
- Complexity: ~2–3 weeks of controller work plus a sidecar/proxy component the project has to own forever.

### 2. Knative Serving (Activator + Autoscaler)

Off-the-shelf scale-to-zero. Knative's Activator buffers the first request, the Autoscaler triggers scale-up, traffic is released once the pod is Ready.

- Pros: solved problem; well-tested wakeup wiring; community-maintained.
- Cons: introduces Knative Serving as a long-lived cluster dependency (control plane, CRDs, webhook). The operator would need to either emit Knative `Service` objects or hand workload ownership to Knative — both are intrusive shifts in the CRD model.
- Cold-start cost: 3–15 min for the model itself; Knative wakeup adds ~1–2 s on top, negligible.
- Complexity: hours to install; weeks of churn to integrate cleanly with the operator's CRD ownership model.

### 3. KEDA `ScaledObject` with HTTP scaler

KEDA's `http-add-on` is a slimmer Knative-style pattern: an interceptor pod sits in front of the `Service`, a controller scales the target `Deployment` based on request rate, scale-to-zero is a built-in mode.

- Pros: lighter than Knative; KEDA is already a common cluster add-on; integrates with existing `Deployment`/`Service` rather than replacing them.
- Cons: still an external dependency with its own CRDs and interceptor. Adds a hop in the request path. Operator would need to emit `ScaledObject` CRs and trust KEDA's lifecycle.
- Cold-start cost: same 3–15 min model load; KEDA interceptor adds tens of ms.
- Complexity: days to wire up; cleaner than Knative but still ships a second control plane.

## What this leaves unanswered

**State preservation.** Scale-to-zero loses the in-process KV prefix cache, which matters most for the long-context-agent north-star workload (shared system prompts, multi-turn sessions). The LMCache sidecar from #13 gives a partial answer — host-RAM tier survives pod restart and can rehydrate KV — but LMCache itself does not trigger scale-down. Pairing #13 with any of the three approaches above would turn a 3–15 min cold-start into something closer to a warm restart, but only for prefixes that fit in the LMCache tier.

**MIG slice reclamation.** When `replicas: 0` and the pod terminates, the NVIDIA device plugin reports the MIG slice as free on the next allocation cycle (seconds, not minutes). The slice is genuinely returned to the pool — this part works. The pin only persists if the `Deployment` itself stays at `replicas: 1`.

**Wakeup wiring** is the architectural fork. Operator-native means we own a request-buffering proxy. Knative/KEDA means we don't own it but inherit a control-plane dependency. There is no zero-cost option.

## Recommendation: defer

The cluster has 2 slices. The realistic worst case is one idle workspace pinning one slice — saved-GPU-hour math is small. Against that, a 3–15 min cold-start is painful for an interactive agent workload; users will not tolerate it without warning, which means scale-to-zero is realistically only useful for batch or scheduled patterns this repo does not yet target.

Operator-native is the cleanest architectural fit but is the most expensive (~2–3 weeks of controller work plus a long-lived proxy component). Knative/KEDA are cheaper to install but mortgage the project against an external control plane forever — a high price for a 2-slice cluster.

### Trigger conditions that would flip this to "build"

- Cluster grows past 4 MIG slices with 2+ idle workspaces typical during off-hours.
- LMCache (#13) lands and demonstrates sub-minute warm restart for the 31B-NVFP4 north-star workload — this collapses the cold-start objection.
- A scheduled or batch usage pattern emerges where users explicitly accept multi-minute first-request latency.

If any one of those holds, revisit. The preferred path at that point is operator-native idle timer plus LMCache-backed warm restart, with the doorbell-pod variant of wakeup wiring (avoids the Knative/KEDA dependency tax). Until then, leave instances pinned and document `kubectl delete vllminstance` as the manual reclamation path.
