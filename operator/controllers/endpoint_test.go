package controllers

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// TestResolveServiceEndpoint_ClusterIP asserts that a ClusterIP service
// publishes the in-cluster DNS form even though the controller never
// performed an EndpointSlice lookup. This is the issue #75 default path.
func TestResolveServiceEndpoint_ClusterIP(t *testing.T) {
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: "svc-vi", Namespace: "vllm"},
		Spec:       corev1.ServiceSpec{Type: corev1.ServiceTypeClusterIP},
	}
	got := resolveServiceEndpoint(svc, 0, "")
	want := "http://svc-vi.vllm.svc:8000/v1"
	if got != want {
		t.Errorf("ClusterIP endpoint: got %q, want %q", got, want)
	}
}

// TestResolveServiceEndpoint_NodePortPassThrough asserts that a NodePort
// service passes the caller-computed nodePort fallback through verbatim. The
// caller is responsible for the EndpointSlice + Node-IP lookup; the helper
// just gates whether to use that result vs. an alternate format.
func TestResolveServiceEndpoint_NodePortPassThrough(t *testing.T) {
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: "svc-vi", Namespace: "vllm"},
		Spec:       corev1.ServiceSpec{Type: corev1.ServiceTypeNodePort},
	}
	got := resolveServiceEndpoint(svc, 31234, "http://10.0.0.1:31234/v1")
	if got != "http://10.0.0.1:31234/v1" {
		t.Errorf("NodePort endpoint should pass through fallback; got %q", got)
	}
}

// TestResolveServiceEndpoint_LoadBalancerWithIP asserts that when the LB
// has an assigned IP, the resolved endpoint uses it.
func TestResolveServiceEndpoint_LoadBalancerWithIP(t *testing.T) {
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: "svc-vi", Namespace: "vllm"},
		Spec:       corev1.ServiceSpec{Type: corev1.ServiceTypeLoadBalancer},
		Status: corev1.ServiceStatus{
			LoadBalancer: corev1.LoadBalancerStatus{
				Ingress: []corev1.LoadBalancerIngress{{IP: "203.0.113.5"}},
			},
		},
	}
	got := resolveServiceEndpoint(svc, 0, "")
	want := "http://203.0.113.5:8000/v1"
	if got != want {
		t.Errorf("LoadBalancer-IP endpoint: got %q, want %q", got, want)
	}
}

// TestResolveServiceEndpoint_LoadBalancerHostname covers the cloud-provider
// case where the LB exposes a Hostname (e.g. AWS NLB) rather than an IP.
func TestResolveServiceEndpoint_LoadBalancerHostname(t *testing.T) {
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: "svc-vi", Namespace: "vllm"},
		Spec:       corev1.ServiceSpec{Type: corev1.ServiceTypeLoadBalancer},
		Status: corev1.ServiceStatus{
			LoadBalancer: corev1.LoadBalancerStatus{
				Ingress: []corev1.LoadBalancerIngress{{Hostname: "lb.example.com"}},
			},
		},
	}
	got := resolveServiceEndpoint(svc, 0, "")
	want := "http://lb.example.com:8000/v1"
	if got != want {
		t.Errorf("LoadBalancer-Hostname endpoint: got %q, want %q", got, want)
	}
}

// TestResolveServiceEndpoint_LoadBalancerProvisioningFallback asserts that
// while a LoadBalancer is still provisioning (no Ingress entries), the
// resolved endpoint falls back to the cluster-DNS form so status.endpoint
// isn't blank for the entire window.
func TestResolveServiceEndpoint_LoadBalancerProvisioningFallback(t *testing.T) {
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: "svc-vi", Namespace: "vllm"},
		Spec:       corev1.ServiceSpec{Type: corev1.ServiceTypeLoadBalancer},
	}
	got := resolveServiceEndpoint(svc, 0, "")
	want := "http://svc-vi.vllm.svc:8000/v1"
	if got != want {
		t.Errorf("provisioning LB should fall back to cluster DNS; got %q, want %q", got, want)
	}
}

// TestResolveServiceEndpoint_NilSafe is a defensive guard for any code path
// that calls the helper before the actual Service is read back from the API
// server (e.g. an early-return on cache miss).
func TestResolveServiceEndpoint_NilSafe(t *testing.T) {
	if got := resolveServiceEndpoint(nil, 0, ""); got != "" {
		t.Errorf("nil svc: got %q, want empty string", got)
	}
}
