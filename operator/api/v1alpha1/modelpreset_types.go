package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ProbeConfig is the tunable subset of a k8s probe.
// Liveness periodSeconds=30 and failureThreshold=10 are hardcoded constants in the builder.
type ProbeConfig struct {
	// +kubebuilder:validation:Minimum=0
	InitialDelaySeconds int32 `json:"initialDelaySeconds"`

	// +kubebuilder:validation:Minimum=1
	PeriodSeconds int32 `json:"periodSeconds"`

	// +kubebuilder:validation:Minimum=1
	FailureThreshold int32 `json:"failureThreshold"`
}

// ModelPresetSpec holds a reusable bundle of vLLM args.
// ModelPreset has no controller — it's consumed by VLLMInstance at reconcile time.
type ModelPresetSpec struct {
	ModelID string `json:"modelID"`

	// +kubebuilder:default="docker.io/library/vllm-gemma4:local"
	Image string `json:"image,omitempty"`

	// +kubebuilder:default=Never
	// +kubebuilder:validation:Enum=Always;IfNotPresent;Never
	ImagePullPolicy string `json:"imagePullPolicy,omitempty"`

	// +kubebuilder:validation:Pattern=`^nvidia\.com/mig-[0-9]+g\.[0-9]+gb$`
	MIGResource string `json:"migResource"`

	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=8
	MIGResourceCount int32 `json:"migResourceCount"`

	Quantization string `json:"quantization,omitempty"`
	DType        string `json:"dtype,omitempty"`

	// ServedModelName overrides the name vLLM advertises to clients
	// (`/v1/models` and the `model:` field in chat-completion requests). Maps
	// to vLLM's `--served-model-name`. When empty, vLLM serves under the same
	// name as `modelID`. Useful for serving the same logical model under a
	// stable name even when swapping checkpoints (e.g. `google/gemma-4-31B-it`
	// regardless of whether the backend weights are BF16 or NVFP4).
	ServedModelName string `json:"servedModelName,omitempty"`

	// +kubebuilder:validation:Minimum=1024
	MaxModelLen int32 `json:"maxModelLen"`

	// +kubebuilder:validation:Pattern=`^0?\.[0-9]+$|^1\.0$`
	GPUMemoryUtilization string `json:"gpuMemoryUtilization"`

	// +kubebuilder:validation:Minimum=1
	TensorParallelSize int32 `json:"tensorParallelSize"`

	EnableAutoToolChoice bool   `json:"enableAutoToolChoice,omitempty"`
	ToolCallParser       string `json:"toolCallParser,omitempty"`

	// +kubebuilder:validation:Pattern=`^[0-9]+[KMGT]i?$`
	SHMSizeLimit string `json:"shmSizeLimit"`

	// +kubebuilder:validation:Minimum=60
	ProgressDeadlineSeconds int32 `json:"progressDeadlineSeconds"`

	LivenessProbe  ProbeConfig `json:"livenessProbe"`
	ReadinessProbe ProbeConfig `json:"readinessProbe"`
}

// +kubebuilder:object:root=true
// +kubebuilder:resource:scope=Namespaced,shortName=mp
// +kubebuilder:printcolumn:name="Model",type=string,JSONPath=`.spec.modelID`
// +kubebuilder:printcolumn:name="MIG",type=string,JSONPath=`.spec.migResource`
// +kubebuilder:printcolumn:name="TP",type=integer,JSONPath=`.spec.tensorParallelSize`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// ModelPreset is a reusable bundle of vLLM configuration consumed by VLLMInstance.
type ModelPreset struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec ModelPresetSpec `json:"spec,omitempty"`
}

// +kubebuilder:object:root=true
type ModelPresetList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ModelPreset `json:"items"`
}

func init() {
	SchemeBuilder.Register(&ModelPreset{}, &ModelPresetList{})
}
