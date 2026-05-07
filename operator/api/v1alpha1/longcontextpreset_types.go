package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// LongContextPresetSpec is a preset tuned for maximum context length per model.
// It mirrors ModelPresetSpec field-for-field and adds two long-context-specific
// fields. The new type exists so the existing ModelPreset semantics are
// untouched while opinionated defaults (KV quantization, prefix caching) ship
// here.
type LongContextPresetSpec struct {
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

	// KVCacheDtype controls vLLM's --kv-cache-dtype flag. Required for this
	// preset type — long-context deployments must opt in to a specific KV
	// cache quantization. FP8 KV roughly halves KV memory at long context,
	// approximately doubling max-model-len at the same VRAM budget.
	// +kubebuilder:validation:Enum=fp8;fp8_e5m2;fp8_e4m3
	// +kubebuilder:default=fp8_e5m2
	KVCacheDtype string `json:"kvCacheDtype"`

	// EnablePrefixCaching enables vLLM's RadixAttention-style automatic prefix
	// caching. Defaults to true for this preset type — long-context workloads
	// almost always benefit from KV prefix reuse across requests.
	// +kubebuilder:default=true
	EnablePrefixCaching bool `json:"enablePrefixCaching,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:resource:scope=Namespaced,shortName=lcp
// +kubebuilder:printcolumn:name="Model",type=string,JSONPath=`.spec.modelID`
// +kubebuilder:printcolumn:name="MIG",type=string,JSONPath=`.spec.migResource`
// +kubebuilder:printcolumn:name="Ctx",type=integer,JSONPath=`.spec.maxModelLen`
// +kubebuilder:printcolumn:name="KV",type=string,JSONPath=`.spec.kvCacheDtype`
// +kubebuilder:printcolumn:name="TP",type=integer,JSONPath=`.spec.tensorParallelSize`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// LongContextPreset is a preset for vLLM deployments that prioritize
// max-context-per-model over throughput. Consumed by LongContextInstance.
type LongContextPreset struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec LongContextPresetSpec `json:"spec,omitempty"`
}

// +kubebuilder:object:root=true
type LongContextPresetList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []LongContextPreset `json:"items"`
}

func init() {
	SchemeBuilder.Register(&LongContextPreset{}, &LongContextPresetList{})
}
