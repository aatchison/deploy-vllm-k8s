package vllm

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// TestBuildServiceClusterIP is the headline regression for issue #75: when
// serviceType is ClusterIP (the new default), the rendered Service must:
//   - have spec.type=ClusterIP, and
//   - NOT carry a NodePort on its port — even if the spec.nodePort field is
//     set (a stale value left over from a prior NodePort deployment).
//
// Honoring nodePort on a ClusterIP service is silently ignored by the
// apiserver today, but the implementation guards against future K8s
// behaviour changes (e.g. apiserver rejecting the combination) and against
// confusion in `kubectl describe`.
func TestBuildServiceClusterIP(t *testing.T) {
	np := int32(30001)
	svc := BuildService("vi", "ns", corev1.ServiceTypeClusterIP, &np, metav1.OwnerReference{})

	if svc.Spec.Type != corev1.ServiceTypeClusterIP {
		t.Errorf("spec.type: got %q, want ClusterIP", svc.Spec.Type)
	}
	if len(svc.Spec.Ports) != 1 {
		t.Fatalf("expected 1 port; got %d", len(svc.Spec.Ports))
	}
	if got := svc.Spec.Ports[0].NodePort; got != 0 {
		t.Errorf("ClusterIP service must not carry a NodePort even if spec.nodePort "+
			"is set on the CR (stale-value safety); got %d", got)
	}
}

// TestBuildServiceNodePortHonorsField asserts the legacy behaviour: when
// serviceType=NodePort and nodePort is set, the rendered Service carries the
// requested NodePort.
func TestBuildServiceNodePortHonorsField(t *testing.T) {
	np := int32(30123)
	svc := BuildService("vi", "ns", corev1.ServiceTypeNodePort, &np, metav1.OwnerReference{})

	if svc.Spec.Type != corev1.ServiceTypeNodePort {
		t.Errorf("spec.type: got %q, want NodePort", svc.Spec.Type)
	}
	if got := svc.Spec.Ports[0].NodePort; got != 30123 {
		t.Errorf("nodePort: got %d, want 30123", got)
	}
}

// TestBuildServiceNodePortAutoAssign asserts the auto-assign path: when
// serviceType=NodePort and nodePort is nil, the rendered Service has NodePort=0
// so the apiserver picks a free port from the cluster's range.
func TestBuildServiceNodePortAutoAssign(t *testing.T) {
	svc := BuildService("vi", "ns", corev1.ServiceTypeNodePort, nil, metav1.OwnerReference{})

	if svc.Spec.Type != corev1.ServiceTypeNodePort {
		t.Errorf("spec.type: got %q, want NodePort", svc.Spec.Type)
	}
	if got := svc.Spec.Ports[0].NodePort; got != 0 {
		t.Errorf("nodePort must be 0 (auto-assign) when CR's nodePort is nil; got %d", got)
	}
}

// TestBuildServiceLoadBalancer asserts the LoadBalancer branch: type is set
// to LoadBalancer and a stale spec.nodePort does not bleed onto the port.
func TestBuildServiceLoadBalancer(t *testing.T) {
	np := int32(30001)
	svc := BuildService("vi", "ns", corev1.ServiceTypeLoadBalancer, &np, metav1.OwnerReference{})

	if svc.Spec.Type != corev1.ServiceTypeLoadBalancer {
		t.Errorf("spec.type: got %q, want LoadBalancer", svc.Spec.Type)
	}
	if got := svc.Spec.Ports[0].NodePort; got != 0 {
		t.Errorf("LoadBalancer must not carry a NodePort from a stale spec.nodePort; got %d", got)
	}
}

// TestBuildServiceEmptyDefaultsToClusterIP guards the safe-by-default
// invariant: an empty serviceType (the zero value of corev1.ServiceType) must
// resolve to ClusterIP, not the prior NodePort default.
func TestBuildServiceEmptyDefaultsToClusterIP(t *testing.T) {
	svc := BuildService("vi", "ns", corev1.ServiceType(""), nil, metav1.OwnerReference{})
	if svc.Spec.Type != corev1.ServiceTypeClusterIP {
		t.Errorf("empty serviceType must default to ClusterIP (issue #75 safe-by-default); got %q", svc.Spec.Type)
	}
}
