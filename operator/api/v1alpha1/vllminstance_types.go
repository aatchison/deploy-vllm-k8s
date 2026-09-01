package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// PresetReference names a ModelPreset in the same namespace.
type PresetReference struct {
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="presetRef.name is immutable"
	Name string `json:"name"`
}

// ModelConfigOverrides mirrors ModelPresetSpec field-for-field; every field is a pointer —
// non-nil means override. Probe overrides replace the whole ProbeConfig struct.
type ModelConfigOverrides struct {
	ModelID *string `json:"modelID,omitempty"`
	Image   *string `json:"image,omitempty"`
	// +kubebuilder:validation:Enum=Always;IfNotPresent;Never
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
	// +kubebuilder:validation:Minimum=0
	MaxNumBatchedTokens  *int32 `json:"maxNumBatchedTokens,omitempty"`
	EnableChunkedPrefill *bool  `json:"enableChunkedPrefill,omitempty"`

	// PVCReadOnly, when true, mounts the model PVC at /models with
	// readOnly=true. Set this for any tenant that should consume — but never
	// poison — a shared model cache. See docs/multi-tenant-deployment.md and
	// the security warning in the README. Default false preserves current
	// single-tenant write-cache behavior.
	PVCReadOnly *bool `json:"pvcReadOnly,omitempty"`

	// APIKey, when set, overrides the instance-level apiKey. Same semantics as
	// VLLMInstanceSpec.APIKey: secret value is projected as a read-only file at
	// /var/run/vllm/api-key and passed to vLLM via --api-key. See
	// VLLMInstanceSpec.APIKey for the file-vs-env-var rationale.
	APIKey *corev1.SecretKeySelector `json:"apiKey,omitempty"`
}

// VLLMInstanceSpec is the desired state of a single vLLM deployment.
//
// +kubebuilder:validation:XValidation:rule="has(self.presetRef) || (has(self.overrides) && has(self.overrides.modelID) && has(self.overrides.migResource) && has(self.overrides.maxModelLen))",message="presetRef or (overrides.modelID, overrides.migResource, overrides.maxModelLen) must be set"
// +kubebuilder:validation:XValidation:rule="!has(self.overrides) || !has(self.overrides.tensorParallelSize) || self.overrides.tensorParallelSize <= 1 || (has(self.overrides.migResourceCount) && self.overrides.migResourceCount == self.overrides.tensorParallelSize)",message="overrides.tensorParallelSize > 1 requires overrides.migResourceCount == tensorParallelSize"
// +kubebuilder:validation:XValidation:rule="!has(self.replicas) || self.replicas <= 2",message="replicas must be 0, 1, or 2 (each replica consumes one independently schedulable MIG slice)"
// +kubebuilder:validation:XValidation:rule="!has(self.replicas) || self.replicas <= 1 || self.sharedStorage == true",message="replicas > 1 requires sharedStorage=true (PVC must support multi-node access)"
type VLLMInstanceSpec struct {
	PresetRef *PresetReference      `json:"presetRef,omitempty"`
	Overrides *ModelConfigOverrides `json:"overrides,omitempty"`

	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="pvcName is immutable"
	PVCName string `json:"pvcName"`

	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="hfToken is immutable"
	HFToken corev1.SecretKeySelector `json:"hfToken"`

	// NodePort to expose on every cluster node. Must be in 30000-32767.
	// If omitted, Kubernetes auto-assigns a free port from the NodePort range.
	// Honored only when serviceType is NodePort; ignored for ClusterIP and
	// LoadBalancer.
	// +kubebuilder:validation:Minimum=30000
	// +kubebuilder:validation:Maximum=32767
	NodePort *int32 `json:"nodePort,omitempty"`

	// ServiceType selects the Kubernetes Service type fronting the vLLM pod.
	// Defaults to ClusterIP — the safe-by-default for production deployments
	// behind an Ingress/LoadBalancer with TLS termination. NodePort is provided
	// for dev clusters that need a host-port shortcut, and LoadBalancer for
	// cloud-managed external IPs (charges apply on most providers).
	//
	// BREAKING (issue #75): prior to this field existing, Services were always
	// rendered as NodePort. Existing manifests that rely on the auto-NodePort
	// exposure must set serviceType: NodePort explicitly.
	// +kubebuilder:default=ClusterIP
	// +kubebuilder:validation:Enum=ClusterIP;NodePort;LoadBalancer
	ServiceType corev1.ServiceType `json:"serviceType,omitempty"`

	// APIKey, when set, enables per-request authentication on the vLLM HTTP
	// endpoint. The referenced Secret key is projected into the pod as a
	// read-only file (mode 0400) and passed to vLLM via --api-key. Clients
	// must then send `Authorization: Bearer <token>` on every request.
	//
	// Opt-in by design: an unauthenticated endpoint behind a strict
	// NetworkPolicy and ClusterIP Service is still a defensible posture for
	// trusted-tenant clusters. Setting this field is the right call any time
	// the endpoint is reachable from outside the cluster (Ingress,
	// LoadBalancer, NodePort).
	//
	// File-mount form (not env var) follows the same rationale as HFToken:
	// env vars leak via `kubectl describe`, /proc/PID/environ, and Python
	// tracebacks printing os.environ.
	APIKey *corev1.SecretKeySelector `json:"apiKey,omitempty"`

	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=2
	Replicas *int32 `json:"replicas,omitempty"`

	// SharedStorage acknowledges that PVCName supports multi-node access (RWX/ROX)
	// when replicas is greater than one. It is required for replicas=2.
	SharedStorage bool `json:"sharedStorage,omitempty"`

	// PVCReadOnly, when true, mounts the model PVC at /models with
	// readOnly=true. Set this for any tenant that should consume — but never
	// poison — a shared model cache. See docs/multi-tenant-deployment.md and
	// the security warning in the README. Default false preserves current
	// single-tenant write-cache behavior. May also be set on Overrides.
	PVCReadOnly *bool `json:"pvcReadOnly,omitempty"`
}

// VLLMInstanceStatus reflects the observed state.
type VLLMInstanceStatus struct {
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

// Condition type constants.
const (
	ConditionPresetResolved     = "PresetResolved"
	ConditionStorageReady       = "StorageReady"
	ConditionProgressing        = "Progressing"
	ConditionDeploymentAvail    = "DeploymentAvailable"
	ConditionReady              = "Ready"
	ReasonPresetNotFound        = "PresetNotFound"
	ReasonPresetFound           = "PresetFound"
	ReasonOverridesUsed         = "OverridesUsed"
	ReasonPVCNotFound           = "PVCNotFound"
	ReasonPVCFound              = "PVCFound"
	ReasonDeploymentProgressing = "DeploymentProgressing"
	ReasonDeploymentAvailable   = "DeploymentAvailable"
	ReasonDeploymentUnavailable = "DeploymentUnavailable"
	ReasonAllReady              = "AllReady"
	ReasonScaledToZero          = "ScaledToZero"
	ReasonApplyFailed           = "ApplyFailed"
)

// +kubebuilder:object:root=true
// +kubebuilder:resource:scope=Namespaced,shortName=vllm
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
// +kubebuilder:printcolumn:name="Endpoint",type=string,JSONPath=`.status.endpoint`
// +kubebuilder:printcolumn:name="Preset",type=string,JSONPath=`.spec.presetRef.name`
// +kubebuilder:printcolumn:name="NodePort",type=integer,JSONPath=`.spec.nodePort`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// VLLMInstance is a declarative vLLM Deployment + NodePort Service bundle.
type VLLMInstance struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   VLLMInstanceSpec   `json:"spec,omitempty"`
	Status VLLMInstanceStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true
type VLLMInstanceList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []VLLMInstance `json:"items"`
}

func init() {
	SchemeBuilder.Register(&VLLMInstance{}, &VLLMInstanceList{})
}
