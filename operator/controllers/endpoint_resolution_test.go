package controllers

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

// TestResolveEndpoint_DefaultsToServiceDNS pins the issue #78 fix:
// without ExposeNodeIPAnnotation, status.endpoint must be the in-cluster
// Service DNS form so namespace tenants with vllminstances:get cannot
// enumerate Node InternalIPs (a cluster-recon primitive otherwise gated
// behind cluster-scoped nodes:get RBAC).
//
// We populate an EndpointSlice + Node with InternalIPs to prove the
// default path does NOT consult them: even with a Ready node IP available,
// the published endpoint stays in-cluster.
func TestResolveEndpoint_DefaultsToServiceDNS(t *testing.T) {
	scheme := fullScheme(t)
	ready := true
	slice := &discoveryv1.EndpointSlice{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "svc-foo-abc",
			Namespace: "vllm",
			Labels:    map[string]string{discoveryv1.LabelServiceName: "svc-foo"},
		},
		AddressType: discoveryv1.AddressTypeIPv4,
		Endpoints: []discoveryv1.Endpoint{{
			Addresses:  []string{"10.244.0.5"},
			NodeName:   ptr("node-a"),
			Conditions: discoveryv1.EndpointConditions{Ready: &ready},
		}},
	}
	node := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: "node-a"},
		Status: corev1.NodeStatus{
			Addresses: []corev1.NodeAddress{{Type: corev1.NodeInternalIP, Address: "10.0.0.42"}},
		},
	}
	cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(slice, node).Build()

	t.Run("VLLMInstance default", func(t *testing.T) {
		r := &VLLMInstanceReconciler{Client: cl, Scheme: scheme}
		got := r.resolveEndpoint(context.Background(), "vllm", "svc-foo", 32000, nil)
		want := "http://svc-foo.vllm.svc.cluster.local:8000/v1"
		if got != want {
			t.Errorf("default endpoint must be in-cluster DNS: got %q want %q", got, want)
		}
	})

	t.Run("LongContextInstance default", func(t *testing.T) {
		r := &LongContextInstanceReconciler{Client: cl, Scheme: scheme}
		got := r.resolveEndpoint(context.Background(), "vllm", "svc-foo", 32000, nil)
		want := "http://svc-foo.vllm.svc.cluster.local:8000/v1"
		if got != want {
			t.Errorf("default endpoint must be in-cluster DNS: got %q want %q", got, want)
		}
	})

	t.Run("VLLMInstance annotation map without opt-in still defaults", func(t *testing.T) {
		// An empty (or wrong-value) annotation map must not flip the default.
		r := &VLLMInstanceReconciler{Client: cl, Scheme: scheme}
		got := r.resolveEndpoint(context.Background(), "vllm", "svc-foo", 32000, map[string]string{
			"some.other/annotation":            "true",
			"vllm.aatchison.io/expose-node-ip": "false",
		})
		want := "http://svc-foo.vllm.svc.cluster.local:8000/v1"
		if got != want {
			t.Errorf("default endpoint must be in-cluster DNS unless explicitly opted-in: got %q want %q", got, want)
		}
	})
}

// TestResolveEndpoint_NodeIPWhenAnnotated covers the opt-in path: with
// the ExposeNodeIPAnnotation set to "true", the legacy NodeIP form returns.
// This preserves the dev-cluster ergonomics for benchmark scripts that
// curl the service from a laptop, while keeping the namespace-readable
// default safe.
func TestResolveEndpoint_NodeIPWhenAnnotated(t *testing.T) {
	scheme := fullScheme(t)
	ready := true
	slice := &discoveryv1.EndpointSlice{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "svc-foo-abc",
			Namespace: "vllm",
			Labels:    map[string]string{discoveryv1.LabelServiceName: "svc-foo"},
		},
		AddressType: discoveryv1.AddressTypeIPv4,
		Endpoints: []discoveryv1.Endpoint{{
			Addresses:  []string{"10.244.0.5"},
			NodeName:   ptr("node-a"),
			Conditions: discoveryv1.EndpointConditions{Ready: &ready},
		}},
	}
	node := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: "node-a"},
		Status: corev1.NodeStatus{
			Addresses: []corev1.NodeAddress{{Type: corev1.NodeInternalIP, Address: "10.0.0.42"}},
		},
	}
	cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(slice, node).Build()
	exposeNodeIP := map[string]string{ExposeNodeIPAnnotation: "true"}

	t.Run("VLLMInstance with annotation", func(t *testing.T) {
		r := &VLLMInstanceReconciler{Client: cl, Scheme: scheme}
		got := r.resolveEndpoint(context.Background(), "vllm", "svc-foo", 32000, exposeNodeIP)
		want := "http://10.0.0.42:32000/v1"
		if got != want {
			t.Errorf("opted-in endpoint must be NodeIP form: got %q want %q", got, want)
		}
	})

	t.Run("LongContextInstance with annotation", func(t *testing.T) {
		r := &LongContextInstanceReconciler{Client: cl, Scheme: scheme}
		got := r.resolveEndpoint(context.Background(), "vllm", "svc-foo", 32000, exposeNodeIP)
		want := "http://10.0.0.42:32000/v1"
		if got != want {
			t.Errorf("opted-in endpoint must be NodeIP form: got %q want %q", got, want)
		}
	})
}
