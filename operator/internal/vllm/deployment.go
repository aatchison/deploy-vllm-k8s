package vllm

import (
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
					Volumes: []corev1.Volume{
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
					},
					Containers: []corev1.Container{{
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
						VolumeMounts: []corev1.VolumeMount{
							{Name: "models", MountPath: "/models"},
							{Name: "dshm", MountPath: "/dev/shm"},
						},
						LivenessProbe:  buildLivenessProbe(e.LivenessProbe),
						ReadinessProbe: buildReadinessProbe(e.ReadinessProbe),
					}},
				},
			},
		},
	}

	return dep
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
	return args
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

// ServiceName returns the Service name for an instance.
// Uses svc-<name> prefix so the result is always a valid DNS-1035 label
// even when the instance name starts with a digit (e.g. "31b-96").
func ServiceName(instanceName string) string {
	return fmt.Sprintf("svc-%s", instanceName)
}
