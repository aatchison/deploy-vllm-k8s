# Host memory for vLLM

Set `spec.memoryRequest` and `spec.memoryLimit` on a `ModelPreset` or
`LongContextPreset`. Set the same fields under an instance's `spec.overrides`
to replace either value. These fields set only the vLLM container's
`resources.requests.memory` and `resources.limits.memory`. They do not change
the MIG GPU limit or LMCache sidecar resources.

Values are positive Kubernetes quantities, such as `64Gi`, `80G`, or `1024Mi`.
When both values are set, the limit must be at least the request. Admission
checks values in each object. The controller checks the merged preset and
instance values before it applies a Deployment or Service. An empty string in
an override clears that inherited resource. Unset fields add no memory resource
and do not change existing renders or config hashes. Kubernetes may copy a
limit into the request if only a limit is set; set both for explicit scheduling.

## PLE offload and shared memory

The Qwen3.8-Flash-Next PLE CPU-offload table occupies about 48 GiB in FP8 and
counts against the vLLM container's host-memory budget. Size host RAM at the
pinned table plus about 16 GiB for the process: about 64 GiB for that table.
Add headroom for other allocations, including actual shared-memory use.
Set the request to cover expected resident memory so the scheduler reserves
room on the node. Set the limit high enough to avoid an OOM kill.

`shmSizeLimit` only caps the memory-backed `/dev/shm` volume. It does not
reserve node RAM, set a container memory limit, or allocate space for the
pinned PLE table. The table uses process host RAM, not `/dev/shm`. Pages actually
written to the shared-memory volume also count toward container memory use;
the volume's size limit is not a separate RAM budget.

For example, add these fields to a complete preset:

```yaml
spec:
  memoryRequest: 64Gi
  memoryLimit: 80Gi
  # /dev/shm cap only; the PLE table needs separate host RAM above.
  shmSizeLimit: 8Gi
```

To raise only the limit for one instance:

```yaml
spec:
  overrides:
    memoryLimit: 96Gi
```

## Explicit prefix caching

On the long-context API, `enablePrefixCaching: true` emits
`--enable-prefix-caching`. An explicit `false` emits
`--no-enable-prefix-caching`. If the field is unset, neither flag is emitted.
This lets an explicit false reach vLLM when `mambaCacheMode: none` is used;
vLLM normalizes prefix caching enabled with Mamba mode `none` into `align`.
An instance override of false replaces a preset's true value.
