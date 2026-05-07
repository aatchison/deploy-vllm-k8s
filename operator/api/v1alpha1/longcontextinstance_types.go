package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// LongContextPresetReference names a LongContextPreset in the same namespace.
type LongContextPresetReference struct {
	Name string `json:"name"`
}

// LongContextOverrides mirrors LongContextPresetSpec field-for-field; every
// field is a pointer — non-nil means override. Probe overrides replace the
// whole ProbeConfig struct.
type LongContextOverrides struct {
	ModelID                 *string      `json:"modelID,omitempty"`
	Image                   *string      `json:"image,omitempty"`
	ImagePullPolicy         *string      `json:"imagePullPolicy,omitempty"`
	MIGResource             *string      `json:"migResource,omitempty"`
	MIGResourceCount        *int32       `json:"migResourceCount,omitempty"`
	Quantization            *string      `json:"quantization,omitempty"`
	DType                   *string      `json:"dtype,omitempty"`
	ServedModelName         *string      `json:"servedModelName,omitempty"`
	MaxModelLen             *int32       `json:"maxModelLen,omitempty"`
	GPUMemoryUtilization    *string      `json:"gpuMemoryUtilization,omitempty"`
	TensorParallelSize      *int32       `json:"tensorParallelSize,omitempty"`
	EnableAutoToolChoice    *bool        `json:"enableAutoToolChoice,omitempty"`
	ToolCallParser          *string      `json:"toolCallParser,omitempty"`
	SHMSizeLimit            *string      `json:"shmSizeLimit,omitempty"`
	ProgressDeadlineSeconds *int32       `json:"progressDeadlineSeconds,omitempty"`
	LivenessProbe           *ProbeConfig `json:"livenessProbe,omitempty"`
	ReadinessProbe          *ProbeConfig `json:"readinessProbe,omitempty"`
	StartupProbe            *ProbeConfig `json:"startupProbe,omitempty"`
	KVCacheDtype            *string      `json:"kvCacheDtype,omitempty"`
	EnablePrefixCaching     *bool        `json:"enablePrefixCaching,omitempty"`
	CPUOffloadGiB           *int32       `json:"cpuOffloadGiB,omitempty"`
	// +kubebuilder:validation:Minimum=0
	MaxNumBatchedTokens     *int32       `json:"maxNumBatchedTokens,omitempty"`
	EnableChunkedPrefill    *bool        `json:"enableChunkedPrefill,omitempty"`
	// +kubebuilder:validation:Enum=none;lmcache
	KVOffloadBackend        *string      `json:"kvOffloadBackend,omitempty"`
	// +kubebuilder:validation:Minimum=0
	KVOffloadSize           *int32       `json:"kvOffloadSize,omitempty"`
}

// LongContextInstanceSpec is the desired state of a single long-context vLLM
// deployment.
//
// +kubebuilder:validation:XValidation:rule="has(self.presetRef) || (has(self.overrides) && has(self.overrides.modelID) && has(self.overrides.migResource) && has(self.overrides.maxModelLen) && has(self.overrides.kvCacheDtype))",message="presetRef or (overrides.modelID, overrides.migResource, overrides.maxModelLen, overrides.kvCacheDtype) must be set"
// +kubebuilder:validation:XValidation:rule="!has(self.overrides) || !has(self.overrides.tensorParallelSize) || self.overrides.tensorParallelSize <= 1 || (has(self.overrides.migResourceCount) && self.overrides.migResourceCount == self.overrides.tensorParallelSize)",message="overrides.tensorParallelSize > 1 requires overrides.migResourceCount == tensorParallelSize"
// +kubebuilder:validation:XValidation:rule="!has(self.replicas) || self.replicas <= 1",message="replicas must be 0 or 1 (MIG slice cannot host multiple pods)"
// +kubebuilder:validation:XValidation:rule="!has(self.overrides) || !has(self.overrides.kvOffloadBackend) || self.overrides.kvOffloadBackend != 'lmcache' || ((!has(self.overrides.migResourceCount) || self.overrides.migResourceCount <= 1) && (!has(self.overrides.tensorParallelSize) || self.overrides.tensorParallelSize <= 1))",message="LMCache offload is single-slice only in the current implementation"
type LongContextInstanceSpec struct {
	PresetRef *LongContextPresetReference `json:"presetRef,omitempty"`
	Overrides *LongContextOverrides       `json:"overrides,omitempty"`

	PVCName string `json:"pvcName"`

	HFToken corev1.SecretKeySelector `json:"hfToken"`

	// NodePort to expose on every cluster node. Must be in 30000-32767.
	// If omitted, Kubernetes auto-assigns a free port from the NodePort range.
	// +kubebuilder:validation:Minimum=30000
	// +kubebuilder:validation:Maximum=32767
	NodePort *int32 `json:"nodePort,omitempty"`

	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=1
	Replicas *int32 `json:"replicas,omitempty"`
}

// LongContextInstanceStatus reflects the observed state.
type LongContextInstanceStatus struct {
	// +patchStrategy=merge
	// +patchMergeKey=type
	// +listType=map
	// +listMapKey=type
	Conditions []metav1.Condition `json:"conditions,omitempty" patchStrategy:"merge" patchMergeKey:"type"`

	DeploymentName string `json:"deploymentName,omitempty"`
	ServiceName    string `json:"serviceName,omitempty"`
	ReadyReplicas  int32  `json:"readyReplicas,omitempty"`
	Endpoint       string `json:"endpoint,omitempty"`

	ResolvedConfigHash string `json:"resolvedConfigHash,omitempty"`
	ObservedGeneration int64  `json:"observedGeneration,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:resource:scope=Namespaced,shortName=lci
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
// +kubebuilder:printcolumn:name="Endpoint",type=string,JSONPath=`.status.endpoint`
// +kubebuilder:printcolumn:name="Preset",type=string,JSONPath=`.spec.presetRef.name`
// +kubebuilder:printcolumn:name="NodePort",type=integer,JSONPath=`.spec.nodePort`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// LongContextInstance is a declarative vLLM Deployment + NodePort Service
// bundle tuned for max-context-per-model serving.
type LongContextInstance struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   LongContextInstanceSpec   `json:"spec,omitempty"`
	Status LongContextInstanceStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true
type LongContextInstanceList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []LongContextInstance `json:"items"`
}

func init() {
	SchemeBuilder.Register(&LongContextInstance{}, &LongContextInstanceList{})
}
