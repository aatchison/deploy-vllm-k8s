package vllm

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
)

// BuildService renders the NodePort Service fronting the vLLM Deployment.
// If nodePort is nil, the NodePort field is omitted so Kubernetes auto-assigns
// a port from the cluster's NodePort range.
func BuildService(
	instanceName, namespace string,
	nodePort *int32,
	ownerRef metav1.OwnerReference,
) *corev1.Service {
	port := corev1.ServicePort{
		Name:       "http",
		Port:       HTTPPort,
		TargetPort: intstr.FromInt(HTTPPort),
		Protocol:   corev1.ProtocolTCP,
	}
	if nodePort != nil {
		port.NodePort = *nodePort
	}
	return &corev1.Service{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "v1",
			Kind:       "Service",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:            ServiceName(instanceName),
			Namespace:       namespace,
			Labels:          map[string]string{"app": instanceName},
			OwnerReferences: []metav1.OwnerReference{ownerRef},
		},
		Spec: corev1.ServiceSpec{
			Type:     corev1.ServiceTypeNodePort,
			Selector: map[string]string{"app": instanceName},
			Ports:    []corev1.ServicePort{port},
		},
	}
}
