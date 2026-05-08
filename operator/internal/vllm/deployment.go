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
	}

	vllmVolumeMounts := []corev1.VolumeMount{
		{Name: "models", MountPath: "/models"},
		{Name: "dshm", MountPath: "/dev/shm"},
	}

	// When LMCache offload is enabled, add the shared emptyDir volume and mount
	// it in the vLLM container. The sidecar gets appended below.
	if e.KVOffloadBackend == "lmcache" {
		volumes = append(volumes, corev1.Volume{
			Name: LMCacheDataVolume,
			VolumeSource: corev1.VolumeSource{
				EmptyDir: &corev1.EmptyDirVolumeSource{},
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
			{
				Name: "HF_TOKEN",
				ValueFrom: &corev1.EnvVarSource{
					SecretKeyRef: &hfToken,
				},
			},
			{Name: "HF_HOME", Value: DefaultHFHome},
		},
		Resources: corev1.ResourceRequirements{
			Limits: corev1.ResourceList{
				corev1.ResourceName(e.MIGResource): migQty,
			},
		},
		VolumeMounts:   vllmVolumeMounts,
		LivenessProbe:  buildLivenessProbe(e.LivenessProbe),
		ReadinessProbe: buildReadinessProbe(e.ReadinessProbe),
		StartupProbe:   buildStartupProbe(e.StartupProbe),
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
					Volumes:    volumes,
					Containers: containers,
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
		PeriodSeconds:       LivenessPeriodSeconds,
		FailureThreshold:    LivenessFailureThresh,
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
