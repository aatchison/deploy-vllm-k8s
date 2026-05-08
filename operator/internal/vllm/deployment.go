package vllm

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	resource "k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"

	vllmv1alpha1 "github.com/aatchison/deploy-vllm-k8s/operator/api/v1alpha1"
)

var labelSanitizer = regexp.MustCompile(`[^a-zA-Z0-9._-]`)

// HF token file mount constants. The token is projected into the pod as a
// read-only file rather than an env var: env vars leak through `kubectl
// describe`, /proc/<pid>/environ, vLLM tracebacks that print os.environ,
// and forked workers that inherit the parent env. A file mount avoids all
// of those vectors.
const (
	// HFTokenVolumeName is the Volume that wraps the HF token Secret.
	HFTokenVolumeName = "hf-token"
	// HFTokenMountDir is the directory the secret volume is mounted at.
	HFTokenMountDir = "/var/run/hf"
	// HFTokenFileName is the filename inside HFTokenMountDir holding the token.
	HFTokenFileName = "token"
	// HFTokenMountPath is the absolute path the token file ends up at,
	// which is also the value of HF_TOKEN_PATH inside the container.
	HFTokenMountPath = HFTokenMountDir + "/" + HFTokenFileName
	// HFTokenFileMode is the file permission applied to the projected token.
	// 0400 — readable only by the container's UID, no group/world bits.
	HFTokenFileMode int32 = 0o400
)

// Pod security context constants for vLLM model pods (issue #37).
//
// The upstream vllm/vllm-openai image does not set a USER directive, so the
// default container UID is 0 (root). PodSecurity admission "restricted" mode
// rejects that. We force a non-root UID at the pod level. Choice rationale:
//   - 1000 is the conventional first non-system user on most Linux distros
//     and is what vLLM's own base images use for the unprivileged path.
//   - vLLM's runtime does not require root: model weights live under
//     /models (PVC, made writable via fsGroup), and the OpenAI server binds
//     to port 8000 (>1024, no CAP_NET_BIND_SERVICE needed).
//   - fsGroup matches runAsUser so the PVC mount is owned by the running
//     user and HF_HOME (/models/huggingface) is writable for weight cache.
//
// If a future preset needs a different UID (e.g. a hardened base image that
// pre-creates user 65532), expose it via the preset CRD and override here.
const (
	// VLLMRunAsUser is the UID used for the vLLM container and fsGroup.
	VLLMRunAsUser int64 = 1000
	// VLLMFsGroup is the supplemental group applied to mounted volumes so
	// the running user can read/write the PVC. Matches VLLMRunAsUser.
	VLLMFsGroup int64 = 1000
	// LMCacheDataDefaultSizeLimitGiB is the fallback emptyDir sizeLimit (in
	// GiB) for the lmcache-data volume when KVOffloadSize is unset (== 0).
	// 16 GiB matches the LMCache preset default documented in BENCHMARKS.
	LMCacheDataDefaultSizeLimitGiB int64 = 16
)

// SanitizeLabel converts a model ID (e.g. "google/gemma-4-e2b-it") into a
// label-safe string. Replaces any invalid char with '-'.
func SanitizeLabel(s string) string {
	s = labelSanitizer.ReplaceAllString(s, "-")
	if len(s) > 63 {
		s = s[:63]
	}
	return strings.Trim(s, "-._")
}

// BuildDeployment renders a vLLM Deployment from the resolved config.
// `name` is the VLLMInstance name; it's used as both the Deployment name
// and the pod label selector.
func BuildDeployment(
	name, namespace string,
	replicas int32,
	e EffectiveConfig,
	pvcName string,
	hfToken corev1.SecretKeySelector,
	ownerRef metav1.OwnerReference,
) *appsv1.Deployment {
	podLabels := map[string]string{
		"app":   name,
		"model": SanitizeLabel(e.ModelID),
	}

	progressDeadline := e.ProgressDeadlineSeconds
	if progressDeadline < 60 {
		progressDeadline = 600
	}

	shmSize := resource.MustParse(e.SHMSizeLimit)
	migQty := resource.MustParse(strconv.Itoa(int(e.MIGResourceCount)))
	hfTokenMode := HFTokenFileMode
	// Issue #74: explicitly disable SA token automount on the pod (see
	// AutomountServiceAccountToken field below for the full rationale).
	automountSAToken := false

	volumes := []corev1.Volume{
		{
			Name: "models",
			VolumeSource: corev1.VolumeSource{
				PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
					ClaimName: pvcName,
				},
			},
		},
		{
			Name: "dshm",
			VolumeSource: corev1.VolumeSource{
				EmptyDir: &corev1.EmptyDirVolumeSource{
					Medium:    corev1.StorageMediumMemory,
					SizeLimit: &shmSize,
				},
			},
		},
		{
			// Project the HF token as a read-only file (mode 0400) rather
			// than an env var. Env vars leak via `kubectl describe`,
			// /proc/PID/environ, and Python tracebacks printing os.environ.
			// The file form is read by huggingface_hub via HF_TOKEN_PATH
			// (set on the container env below).
			Name: HFTokenVolumeName,
			VolumeSource: corev1.VolumeSource{
				Secret: &corev1.SecretVolumeSource{
					SecretName:  hfToken.Name,
					DefaultMode: &hfTokenMode,
					Items: []corev1.KeyToPath{
						{Key: hfToken.Key, Path: HFTokenFileName},
					},
				},
			},
		},
	}

	vllmVolumeMounts := []corev1.VolumeMount{
		{Name: "models", MountPath: "/models"},
		{Name: "dshm", MountPath: "/dev/shm"},
		{Name: HFTokenVolumeName, MountPath: HFTokenMountDir, ReadOnly: true},
	}

	// When LMCache offload is enabled, add the shared emptyDir volume and mount
	// it in the vLLM container. The sidecar gets appended below.
	if e.KVOffloadBackend == "lmcache" {
		// Cap the emptyDir to the configured cache budget (KVOffloadSize, GiB).
		// Without a sizeLimit, an over-eager LMCache could fill the node's
		// ephemeral storage and trip the kubelet eviction manager — taking
		// down every other pod on the node. Default to 16 GiB when the preset
		// leaves KVOffloadSize at 0 (let LMCache pick its own kv_buffer_size).
		sizeGiB := int64(e.KVOffloadSize)
		if sizeGiB <= 0 {
			sizeGiB = LMCacheDataDefaultSizeLimitGiB
		}
		lmcacheSize := resource.MustParse(fmt.Sprintf("%dGi", sizeGiB))
		volumes = append(volumes, corev1.Volume{
			Name: LMCacheDataVolume,
			VolumeSource: corev1.VolumeSource{
				EmptyDir: &corev1.EmptyDirVolumeSource{
					SizeLimit: &lmcacheSize,
				},
			},
		})
		vllmVolumeMounts = append(vllmVolumeMounts, corev1.VolumeMount{
			Name:      LMCacheDataVolume,
			MountPath: LMCacheDataMount,
		})
	}

	containers := []corev1.Container{{
		Name:            ContainerName,
		Image:           e.Image,
		ImagePullPolicy: corev1.PullPolicy(e.ImagePullPolicy),
		Args:            buildArgs(e),
		Ports: []corev1.ContainerPort{{
			Name:          "http",
			ContainerPort: HTTPPort,
			Protocol:      corev1.ProtocolTCP,
		}},
		Env: []corev1.EnvVar{
			// HF_TOKEN_PATH points huggingface_hub at the projected secret
			// file. Do NOT set HF_TOKEN — that re-introduces the env-var
			// leak this volume mount exists to avoid.
			{Name: "HF_TOKEN_PATH", Value: HFTokenMountPath},
			{Name: "HF_HOME", Value: DefaultHFHome},
		},
		Resources: corev1.ResourceRequirements{
			Limits: corev1.ResourceList{
				corev1.ResourceName(e.MIGResource): migQty,
			},
		},
		VolumeMounts:    vllmVolumeMounts,
		LivenessProbe:   buildLivenessProbe(e.LivenessProbe),
		ReadinessProbe:  buildReadinessProbe(e.ReadinessProbe),
		StartupProbe:    buildStartupProbe(e.StartupProbe),
		SecurityContext: buildContainerSecurityContext(),
	}}

	// Append the LMCache sidecar when the backend is "lmcache".
	// The sidecar gets a liveness probe (TCP socket) but NO readiness probe,
	// so a failing LMCache never causes the pod to go NotReady — vLLM can
	// serve correctly even when LMCache is down.
	if e.KVOffloadBackend == "lmcache" {
		containers = append(containers, buildLMCacheSidecar())
	}

	dep := &appsv1.Deployment{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "apps/v1",
			Kind:       "Deployment",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:            name,
			Namespace:       namespace,
			Labels:          map[string]string{"app": name},
			OwnerReferences: []metav1.OwnerReference{ownerRef},
		},
		Spec: appsv1.DeploymentSpec{
			Replicas:                &replicas,
			ProgressDeadlineSeconds: &progressDeadline,
			Strategy: appsv1.DeploymentStrategy{
				Type: appsv1.RecreateDeploymentStrategyType,
			},
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{"app": name},
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: podLabels},
				Spec: corev1.PodSpec{
					Tolerations: []corev1.Toleration{{
						Key:      e.MIGResource,
						Operator: corev1.TolerationOpExists,
						Effect:   corev1.TaintEffectNoSchedule,
					}},
					// Issue #74: do NOT mount the namespace's default
					// ServiceAccount token into vLLM model pods. The model
					// container makes zero kube API calls, but a default-SA
					// token (often cluster-admin on microk8s) would let any
					// vLLM compromise (RCE via poisoned weights, custom
					// modeling code, future CVE) impersonate the SA against
					// kube-apiserver. Disabling the automount removes that
					// vector entirely and complements the HF_TOKEN file
					// mount from #48.
					AutomountServiceAccountToken: &automountSAToken,
					SecurityContext:              buildPodSecurityContext(),
					Volumes:                      volumes,
					Containers:                   containers,
				},
			},
		},
	}

	return dep
}

// buildLMCacheSidecar returns the LMCache sidecar Container spec.
// Resources: 1 CPU request, 4Gi RAM.
// Probe: TCP-socket liveness on LMCacheAdminPort; NO readiness probe so a
// degraded LMCache doesn't take the pod NotReady.
func buildLMCacheSidecar() corev1.Container {
	return corev1.Container{
		Name:            LMCacheContainerName,
		Image:           LMCacheImage,
		ImagePullPolicy: corev1.PullIfNotPresent,
		Resources: corev1.ResourceRequirements{
			Requests: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("1"),
				corev1.ResourceMemory: resource.MustParse("4Gi"),
			},
		},
		VolumeMounts: []corev1.VolumeMount{
			{Name: LMCacheDataVolume, MountPath: LMCacheDataMount},
		},
		// Liveness only — no readiness, so pod readiness is determined solely
		// by the vLLM container.
		LivenessProbe: &corev1.Probe{
			ProbeHandler: corev1.ProbeHandler{
				TCPSocket: &corev1.TCPSocketAction{
					Port: intstr.FromInt(LMCacheAdminPort),
				},
			},
			InitialDelaySeconds: 10,
			PeriodSeconds:       30,
			FailureThreshold:    5,
		},
		SecurityContext: buildContainerSecurityContext(),
	}
}

// buildPodSecurityContext returns the Pod-level securityContext applied to
// every vLLM model pod. Issue #37: the upstream vllm/vllm-openai image runs
// as root by default, which is rejected by the "restricted" PodSecurity
// admission profile. Forcing runAsNonRoot + an explicit UID + fsGroup makes
// the pod admissible while keeping /models writable for HF weight cache.
//
// readOnlyRootFilesystem is intentionally NOT set: vLLM writes to /tmp and
// other dirs at runtime (CUDA caches, torch JIT). Hardening that further is
// tracked separately and would require an emptyDir mounted at /tmp.
func buildPodSecurityContext() *corev1.PodSecurityContext {
	runAsNonRoot := true
	uid := VLLMRunAsUser
	gid := VLLMFsGroup
	return &corev1.PodSecurityContext{
		RunAsNonRoot: &runAsNonRoot,
		RunAsUser:    &uid,
		FSGroup:      &gid,
		SeccompProfile: &corev1.SeccompProfile{
			Type: corev1.SeccompProfileTypeRuntimeDefault,
		},
	}
}

// buildContainerSecurityContext returns the container-level securityContext
// applied to both the vLLM container and the LMCache sidecar (issue #37).
// AllowPrivilegeEscalation:false + drop ALL capabilities are required by the
// "restricted" PodSecurity admission profile.
func buildContainerSecurityContext() *corev1.SecurityContext {
	allowPrivEsc := false
	return &corev1.SecurityContext{
		AllowPrivilegeEscalation: &allowPrivEsc,
		Capabilities: &corev1.Capabilities{
			Drop: []corev1.Capability{"ALL"},
		},
	}
}

func buildArgs(e EffectiveConfig) []string {
	args := []string{"--model", e.ModelID}
	if e.DType != "" {
		args = append(args, "--dtype", e.DType)
	}
	if e.Quantization != "" {
		args = append(args, "--quantization", e.Quantization)
	}
	args = append(args, "--port", strconv.Itoa(HTTPPort))
	args = append(args, "--max-model-len", strconv.Itoa(int(e.MaxModelLen)))
	args = append(args, "--gpu-memory-utilization", e.GPUMemoryUtilization)
	if e.TensorParallelSize > 1 {
		args = append(args, "--tensor-parallel-size", strconv.Itoa(int(e.TensorParallelSize)))
	}
	if e.EnableAutoToolChoice {
		args = append(args, "--enable-auto-tool-choice")
	}
	if e.ToolCallParser != "" {
		args = append(args, "--tool-call-parser", e.ToolCallParser)
	}
	if e.KVCacheDtype != "" {
		args = append(args, "--kv-cache-dtype", e.KVCacheDtype)
	}
	if e.EnablePrefixCaching != nil && *e.EnablePrefixCaching {
		args = append(args, "--enable-prefix-caching")
	}
	if e.ServedModelName != "" {
		args = append(args, "--served-model-name", e.ServedModelName)
	}
	if e.CPUOffloadGiB > 0 {
		args = append(args, "--cpu-offload-gb", strconv.Itoa(int(e.CPUOffloadGiB)))
	}
	if e.MaxNumBatchedTokens > 0 {
		args = append(args, "--max-num-batched-tokens", strconv.Itoa(int(e.MaxNumBatchedTokens)))
	}
	if e.EnableChunkedPrefill {
		args = append(args, "--enable-chunked-prefill")
	}
	if e.KVOffloadBackend == "lmcache" {
		args = append(args, "--kv-transfer-config", buildKVTransferConfig(e.KVOffloadSize))
	}
	return args
}

// kvTransferConfig is the JSON shape for --kv-transfer-config when backend == lmcache.
// kv_buffer_size is omitted when KVOffloadSize == 0 (let LMCache use its default).
type kvTransferConfig struct {
	KVConnector  string `json:"kv_connector"`
	KVRole       string `json:"kv_role"`
	KVBufferSize *int64 `json:"kv_buffer_size,omitempty"`
}

// buildKVTransferConfig serialises the JSON value for --kv-transfer-config.
// sizeGiB is the desired LMCache buffer in GiB; 0 means omit (use LMCache default).
func buildKVTransferConfig(sizeGiB int32) string {
	cfg := kvTransferConfig{
		KVConnector: "LMCacheConnectorV1",
		KVRole:      "kv_both",
	}
	if sizeGiB > 0 {
		bytes := int64(sizeGiB) * (1 << 30)
		cfg.KVBufferSize = &bytes
	}
	b, _ := json.Marshal(cfg)
	return string(b)
}

func healthProbeHandler() corev1.ProbeHandler {
	return corev1.ProbeHandler{
		HTTPGet: &corev1.HTTPGetAction{
			Path: "/health",
			Port: intstr.FromInt(HTTPPort),
		},
	}
}

func buildLivenessProbe(cfg vllmv1alpha1.ProbeConfig) *corev1.Probe {
	return &corev1.Probe{
		ProbeHandler:        healthProbeHandler(),
		InitialDelaySeconds: cfg.InitialDelaySeconds,
		PeriodSeconds:       cfg.PeriodSeconds,
		FailureThreshold:    cfg.FailureThreshold,
	}
}

func buildReadinessProbe(cfg vllmv1alpha1.ProbeConfig) *corev1.Probe {
	return &corev1.Probe{
		ProbeHandler:        healthProbeHandler(),
		InitialDelaySeconds: cfg.InitialDelaySeconds,
		PeriodSeconds:       cfg.PeriodSeconds,
		FailureThreshold:    cfg.FailureThreshold,
	}
}

// buildStartupProbe returns nil when cfg is nil — the canonical k8s
// "no startupProbe" representation. Unlike buildLivenessProbe, period and
// failureThreshold are NOT hardcoded: callers tune them per-preset because
// startup grace varies wildly by model size and weight cache state.
func buildStartupProbe(cfg *vllmv1alpha1.ProbeConfig) *corev1.Probe {
	if cfg == nil {
		return nil
	}
	return &corev1.Probe{
		ProbeHandler:        healthProbeHandler(),
		InitialDelaySeconds: cfg.InitialDelaySeconds,
		PeriodSeconds:       cfg.PeriodSeconds,
		FailureThreshold:    cfg.FailureThreshold,
	}
}

// ServiceName returns the Service name for an instance.
// Uses svc-<name> prefix so the result is always a valid DNS-1035 label
// even when the instance name starts with a digit (e.g. "31b-96").
func ServiceName(instanceName string) string {
	return fmt.Sprintf("svc-%s", instanceName)
}
