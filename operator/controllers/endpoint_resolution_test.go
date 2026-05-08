package controllers

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

// TestResolveEndpoint_NodeIP covers the EndpointSlice -> Node InternalIP
// lookup that backs the spec.serviceType: NodePort path (issue #75). The
// caller (Reconcile) only invokes resolveEndpoint when actualSvc.Spec.Type ==
// NodePort and feeds the result through resolveServiceEndpoint, so this test
// pins the inner lookup in isolation.
//
// Issue #78's vllm.aatchison.io/expose-node-ip annotation was superseded by
// spec.serviceType in PR #75 + #93; the annotation path no longer exists.
func TestResolveEndpoint_NodeIP(t *testing.T) {
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

	t.Run("VLLMInstance", func(t *testing.T) {
		r := &VLLMInstanceReconciler{Client: cl, Scheme: scheme}
		got := r.resolveEndpoint(context.Background(), "vllm", "svc-foo", 32000)
		want := "http://10.0.0.42:32000/v1"
		if got != want {
			t.Errorf("NodePort endpoint: got %q want %q", got, want)
		}
	})

	t.Run("LongContextInstance", func(t *testing.T) {
		r := &LongContextInstanceReconciler{Client: cl, Scheme: scheme}
		got := r.resolveEndpoint(context.Background(), "vllm", "svc-foo", 32000)
		want := "http://10.0.0.42:32000/v1"
		if got != want {
			t.Errorf("NodePort endpoint: got %q want %q", got, want)
		}
	})
}
