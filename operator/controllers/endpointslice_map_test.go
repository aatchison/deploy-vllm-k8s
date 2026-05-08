package controllers

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// makeSlice builds a minimal EndpointSlice with a kubernetes.io/service-name
// label — that's the only field the mapping function reads. Helper keeps the
// table-driven test below readable.
func makeSlice(svcName, namespace string) *discoveryv1.EndpointSlice {
	labels := map[string]string{}
	if svcName != "" {
		labels[discoveryv1.LabelServiceName] = svcName
	}
	return &discoveryv1.EndpointSlice{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-slice",
			Namespace: namespace,
			Labels:    labels,
		},
	}
}

// TestVLLMMapEndpointSliceToInstance is the regression guard for issue #83
// fix #2: without the EndpointSlice watch + mapping, resolveEndpoint's
// r.List goes uncached to the API server every reconcile. The mapping is
// load-bearing — drop it and Ready-flip events on backing pods never wake
// the parent VLLMInstance reconcile.
//
// Cases:
//   - svc-<name> → enqueue <name> in slice's namespace (happy path)
//   - empty service-name label → no enqueue (slice not yet labelled)
//   - service name without our svc- prefix → no enqueue (someone else's svc)
//   - non-EndpointSlice object → no enqueue (defensive against type confusion)
func TestVLLMMapEndpointSliceToInstance(t *testing.T) {
	r := &VLLMInstanceReconciler{}
	ctx := context.Background()

	t.Run("svc-prefixed_service_name_enqueues_instance", func(t *testing.T) {
		slice := makeSlice("svc-my-instance", "vllm")
		got := r.mapEndpointSliceToInstance(ctx, slice)
		if len(got) != 1 {
			t.Fatalf("got %d requests, want 1", len(got))
		}
		if got[0].Namespace != "vllm" || got[0].Name != "my-instance" {
			t.Errorf("got %+v, want {Namespace:vllm Name:my-instance}", got[0].NamespacedName)
		}
	})

	t.Run("empty_service_name_label_returns_nil", func(t *testing.T) {
		slice := makeSlice("", "vllm")
		if got := r.mapEndpointSliceToInstance(ctx, slice); got != nil {
			t.Errorf("got %+v, want nil — no service-name label means we can't map it", got)
		}
	})

	t.Run("non_svc_prefixed_service_name_returns_nil", func(t *testing.T) {
		slice := makeSlice("kubernetes", "default")
		if got := r.mapEndpointSliceToInstance(ctx, slice); got != nil {
			t.Errorf("got %+v, want nil — services we don't own (no svc- prefix) must be skipped", got)
		}
	})

	t.Run("non_endpointslice_object_returns_nil", func(t *testing.T) {
		// Defensive: controller-runtime should never send us a non-slice, but
		// the type assertion guards against accidental misregistration.
		var notASlice client.Object = &corev1.Pod{}
		if got := r.mapEndpointSliceToInstance(ctx, notASlice); got != nil {
			t.Errorf("got %+v, want nil — non-EndpointSlice object must be ignored", got)
		}
	})
}

// TestLongContextMapEndpointSliceToInstance mirrors the VLLMInstance test —
// LongContextInstance shares the svc-<name> Service convention so the same
// mapping shape applies. Both controllers must stay in sync; if the prefix
// rule changes in one, the other will silently break too.
func TestLongContextMapEndpointSliceToInstance(t *testing.T) {
	r := &LongContextInstanceReconciler{}
	ctx := context.Background()

	t.Run("svc-prefixed_service_name_enqueues_instance", func(t *testing.T) {
		slice := makeSlice("svc-long-instance", "vllm")
		got := r.mapEndpointSliceToInstance(ctx, slice)
		if len(got) != 1 {
			t.Fatalf("got %d requests, want 1", len(got))
		}
		if got[0].Namespace != "vllm" || got[0].Name != "long-instance" {
			t.Errorf("got %+v, want {Namespace:vllm Name:long-instance}", got[0].NamespacedName)
		}
	})

	t.Run("empty_service_name_label_returns_nil", func(t *testing.T) {
		slice := makeSlice("", "vllm")
		if got := r.mapEndpointSliceToInstance(ctx, slice); got != nil {
			t.Errorf("got %+v, want nil", got)
		}
	})

	t.Run("non_svc_prefixed_service_name_returns_nil", func(t *testing.T) {
		slice := makeSlice("kubernetes", "default")
		if got := r.mapEndpointSliceToInstance(ctx, slice); got != nil {
			t.Errorf("got %+v, want nil", got)
		}
	})
}
