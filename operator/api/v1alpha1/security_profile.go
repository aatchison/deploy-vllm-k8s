package v1alpha1

// SecurityProfile selects the long-context host-offload security policy.
// +kubebuilder:validation:Enum=default;engram-ipc
type SecurityProfile string

const (
	SecurityProfileDefault   SecurityProfile = "default"
	SecurityProfileEngramIPC SecurityProfile = "engram-ipc"
)
