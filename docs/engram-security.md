# Engram/PLE security profile (issue #179)

`LongContextPreset.spec.securityProfile` and
`LongContextInstance.spec.overrides.securityProfile` accept `default` or
`engram-ipc`. Omission uses `default`. An absent override inherits the preset;
an explicit `default` override opts out. Both profiles retain the same pod
security settings for the reviewed vLLM v0.31.0 CPU-offload path:

- Non-root UID 1000 and fsGroup 1000.
- `allowPrivilegeEscalation: false` and `capabilities.drop: [ALL]`, with no added capabilities.
- `seccompProfile: RuntimeDefault`.
- No host PID, host IPC, host network, shared process namespace, or service-account token.

The name `engram-ipc` does not grant root, `SYS_PTRACE`, or a seccomp exception.
The reviewed source does not justify those changes. No Localhost seccomp file
is required or installed by this operator.

## Policy and fields

| Field or operator setting | Default | Rendered flag or effect |
|---|---|---|
| `securityProfile` | `default` | No vLLM flag. `engram-ipc` requires nonempty `engramConfig` and an approved namespace. Pod security remains restricted. |
| `engramConfig` | absent | `--engram-config <JSON object>`; the operator preserves the JSON bytes. This is the typed prerequisite shared with #178. |
| `--engram-ipc-namespaces` / `ENGRAM_IPC_NAMESPACES` | empty | Operator policy only. A comma-separated list of exact namespace names. The flag overrides the environment variable, including an explicitly empty flag. No wildcards. |

For example, an operator administrator can configure
`--engram-ipc-namespaces=vllm` or `ENGRAM_IPC_NAMESPACES=vllm`.
The namespace must also be inside the operator's watch scope. CR authors
cannot set the allowlist. Empty or unset means every `engram-ipc` request is
refused before storage lookup, Deployment rendering, or resource apply.
The controller reports `Ready=False`, reason `InvalidConfiguration`. Warning
events fire on Ready-status transitions. A refusal does not advance
`.status.observedGeneration`; the Ready condition’s own observedGeneration is updated.

CEL rejects `engram-ipc` on a preset or explicit profile override without a
nonempty config in that same object. An explicit `engram-ipc` override must
repeat `overrides.engramConfig`, even if the preset already has it.
CEL cannot dereference another CR. After merging, the controller also validates
the effective profile, config, and namespace. Clearing an inherited config
while retaining `engram-ipc` is refused. Invalid JSON, arrays, and `null` are
refused; nonempty configs must encode a JSON object of at most 65536 bytes.

The allowlist controls the profile, not CPU offload itself. A config under
`default` still runs with the restricted context. There is no elevated profile
for a tenant to obtain by changing this field. Removing approval prevents new
reconciliations of `engram-ipc`; it does not terminate existing pods. The
operator does not change namespace Pod Security Admission settings.

## Evidence at the target source pin

The vLLM `v0.31.0` tag resolves to
[`db9527a46873454610df6dbedf79a36d6bf1a7f6`](https://github.com/vllm-project/vllm/tree/db9527a46873454610df6dbedf79a36d6bf1a7f6).
Its [CUDA requirements](https://github.com/vllm-project/vllm/blob/db9527a46873454610df6dbedf79a36d6bf1a7f6/requirements/cuda.txt#L7)
pin PyTorch 2.13.0. These are source pins, not a measurement of the image or node.

1. [PLE allocates its own CPU tensor with `pin_memory=True`](https://github.com/vllm-project/vllm/blob/db9527a46873454610df6dbedf79a36d6bf1a7f6/vllm/models/qwen4_exp/common/ngram_embedding.py#L416-L441).
   The [vLLM UVA helper](https://github.com/vllm-project/vllm/blob/db9527a46873454610df6dbedf79a36d6bf1a7f6/csrc/libtorch_stable/cuda_view.cu#L33-L43)
   calls `cudaHostGetDevicePointer` on that same tensor.
2. [PyTorch's pinned host allocator](https://github.com/pytorch/pytorch/blob/cf30153c4c131c8164ee7798e5022d810682e2cb/aten/src/ATen/cuda/CachingHostAllocator.cpp#L70-L80)
   uses `cudaHostAlloc`, or `malloc` plus `cudaHostRegister` when configured.
   This is not the device expandable-segment CUDA IPC allocator.
3. [DeepSeek Engram DP sharing](https://github.com/vllm-project/vllm/blob/db9527a46873454610df6dbedf79a36d6bf1a7f6/vllm/models/deepseek_v41/nvidia/engram.py#L181-L206)
   publishes a `/dev/shm` file path. Each rank opens, maps, and registers its
   own mapping. It does not import another process's host allocation via
   `pidfd_getfd`. TP1/DP1 does not need this DP-sharing path.

Thus the reviewed PLE/Engram CPU-offload allocation paths do not establish a
need for root, `SYS_PTRACE`, or `pidfd_getfd`. The narrow proven privilege
addition is empty. GPU/UVA support, host RAM, writable cache access, host
registration limits, and `/dev/shm` capacity still need runtime validation.

### Host RAM and /dev/shm sizing

The operator mounts `/dev/shm` as a Memory `emptyDir` with
`sizeLimit: shmSizeLimit`. A roughly 50 GiB table backed by this tmpfs cannot
fit in 8Gi or 16Gi. Shared-table modes need a limit above the full table and
scale footprint plus IPC headroom. For example, 64Gi is suitable only if the
measured total footprint fits. Raising `shmSizeLimit` does not reserve RAM or
raise a container memory limit. Tmpfs pages count against the memory limit of
the container that writes them and any applicable pod memory budget, not
ordinary disk ephemeral-storage usage. See the
[Kubernetes emptyDir documentation](https://kubernetes.io/docs/concepts/storage/volumes/#emptydir).

The TP1 Qwen example sets `dp_shared_memory: false`. Its PLE table uses private
pinned host RAM, not `/dev/shm`, so the table alone does not require raising the
example's 8Gi `shmSizeLimit`. It still needs roughly table-sized host RAM plus
loading and runtime headroom. The renderer currently sets only the MIG resource
limit for the vLLM container, not a RAM request or limit. Memory fit remains
untested. Typed container memory requests and limits belong to issue #186.

### A separate `pidfd_getfd` failure is not proof of a host-offload requirement

PyTorch's [device expandable-segment IPC path](https://github.com/pytorch/pytorch/blob/cf30153c4c131c8164ee7798e5022d810682e2cb/c10/cuda/CUDACachingAllocator.cpp#L690-L718)
does call `pidfd_getfd`. If a future test reaches it, identify that caller
and exported allocation type before proposing a security change.

- [Linux v6.12](https://github.com/torvalds/linux/blob/adc218676eef25575469234709c2d87185ca223a/kernel/pid.c#L680-L683)
  checks `PTRACE_MODE_ATTACH_REALCREDS`. Same UID alone is insufficient:
  GIDs, target dumpability, capability relationships, and LSM policy also matter.
  [Yama scope 1](https://github.com/torvalds/linux/blob/adc218676eef25575469234709c2d87185ca223a/security/yama/yama_lsm.c#L367-L385)
  requires importer-to-descendant ancestry or a target-declared ptracer
  exception without `SYS_PTRACE`. Sibling or child-to-parent imports do not
  qualify by ancestry alone. Scope 2 requires the capability; scope 3 denies attach.
- [Containerd v2.1.5's default filter](https://github.com/containerd/containerd/blob/fcd43222d6b07379a4be9786bda52438f0dd16a1/contrib/seccomp/seccomp_default.go#L637-L647)
  permits `pidfd_getfd` only in its `CAP_SYS_PTRACE` branch. Passing Yama
  checks does not bypass this seccomp filter.
- [Containerd clears ambient capabilities](https://github.com/containerd/containerd/blob/fcd43222d6b07379a4be9786bda52438f0dd16a1/internal/cri/server/container_create.go#L829-L835).
  Added pod capabilities do not prove an ordinary non-root Python process
  retains effective capabilities after exec. Root alone does not bypass seccomp.

Those kernel/runtime versions are reference sources, not verified cluster
versions. No image, GPU, allocator environment, kernel, Yama, AppArmor, or
SELinux runtime was tested here. A future privilege change requires new
source/runtime evidence and a separate security review. This profile does not
promise that an arbitrary image using device IPC will start.

## Untested example

[Qwen3.8-Flash-Next on one MIG slice](examples/qwen3.8-flash-next-security-179.yaml)
is **untested**. It needs the typed revision field from #178 as well as this
profile. Do not apply it before both CRD changes are present. It is not a
memory-fit, startup, or throughput claim. No live resources were applied,
synced, or scaled for this change. No image was built or pushed.
