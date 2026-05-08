package vllm

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
)

// BuildService renders the Service fronting the vLLM Deployment.
//
// serviceType selects the resulting Service.spec.type. The empty string and
// any unrecognised value default to ClusterIP — the safe-by-default since
// issue #75. NodePort and LoadBalancer are accepted explicitly via the CRD
// enum.
//
// nodePort is honored only when serviceType is NodePort; for ClusterIP and
// LoadBalancer the field is silently ignored to prevent a stale value on the
// CR from re-opening a host-port the user thought they had closed by
// switching types. (The CRD doesn't `clear` nodePort on type change — that's
// a runtime concern, handled here.)
//
// When serviceType is NodePort and nodePort is nil, the NodePort field is
// omitted so Kubernetes auto-assigns a port from the cluster's NodePort
// range.
func BuildService(
	instanceName, namespace string,
	serviceType corev1.ServiceType,
	nodePort *int32,
	ownerRef metav1.OwnerReference,
) *corev1.Service {
	// Default to ClusterIP for both empty-string and any unsupported value.
	// The CRD enum prevents arbitrary strings, but defending against an
	// upgrade path where the field is unset on an existing CR keeps the
	// safe-by-default invariant.
	switch serviceType {
	case corev1.ServiceTypeNodePort, corev1.ServiceTypeLoadBalancer, corev1.ServiceTypeClusterIP:
		// keep
	default:
		serviceType = corev1.ServiceTypeClusterIP
	}

	port := corev1.ServicePort{
		Name:       "http",
		Port:       HTTPPort,
		TargetPort: intstr.FromInt(HTTPPort),
		Protocol:   corev1.ProtocolTCP,
	}
	// NodePort field is only meaningful for type=NodePort. Honoring it on
	// ClusterIP would be silently ignored by the API server today, but
	// LoadBalancer would treat it as the externally-exposed port — a
	// surprise we don't want when the user just changed serviceType.
	if serviceType == corev1.ServiceTypeNodePort && nodePort != nil {
		port.NodePort = *nodePort
	}

	return &corev1.Service{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "v1",
			Kind:       "Service",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      ServiceName(instanceName),
			Namespace: namespace,
			// app.kubernetes.io/managed-by gates the controller-runtime cache
			// scope (issue #83): the manager only loads Services labelled
			// "managed-by=vllm-operator" into the informer cache. Drop this
			// label and re-reads of the Service we just SSA-applied will
			// IsNotFound forever because the cache never sees the object.
			Labels: map[string]string{
				"app":             instanceName,
				ManagedByLabelKey: ManagedByLabelValue,
			},
			OwnerReferences: []metav1.OwnerReference{ownerRef},
		},
		Spec: corev1.ServiceSpec{
			Type:     serviceType,
			Selector: map[string]string{"app": instanceName},
			Ports:    []corev1.ServicePort{port},
		},
	}
}
