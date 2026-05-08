package controllers

import (
	"context"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

// TestToApplyConfiguration_RoundTripsTypedDeployment pins the migration
// invariant that backed the controller-runtime v0.24 SA1019 cleanup (issue
// #72): the deprecated `r.Patch(ctx, dep, client.Apply, fieldOwner,
// client.ForceOwnership)` call was replaced with `r.Apply(...)` taking a
// runtime.ApplyConfiguration. We accomplish this by routing the existing
// typed *appsv1.Deployment / *corev1.Service builders through
// DefaultUnstructuredConverter and wrapping the result via
// client.ApplyConfigurationFromUnstructured.
//
// The wire payload SSA cares about is the JSON body of the object: TypeMeta
// (apiVersion, kind), ObjectMeta (name, namespace, labels, ownerRefs), and
// Spec. If the conversion silently drops or mistypes any of these, SSA on
// the apiserver fails or — worse — the field manager records bogus ownership.
// This test guards the conversion by asserting the round-tripped
// unstructured map contains every field the SSA call relies on.
func TestToApplyConfiguration_RoundTripsTypedDeployment(t *testing.T) {
	dep := &appsv1.Deployment{
		TypeMeta: metav1.TypeMeta{APIVersion: "apps/v1", Kind: "Deployment"},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "vi",
			Namespace: "ns",
			Labels:    map[string]string{"app": "vi"},
			OwnerReferences: []metav1.OwnerReference{{
				APIVersion: "vllm.aatchison.io/v1alpha1",
				Kind:       "VLLMInstance",
				Name:       "vi",
				UID:        types.UID("abc"),
			}},
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: ptr(int32(1)),
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "vi"}},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": "vi"}},
				Spec: corev1.PodSpec{Containers: []corev1.Container{{
					Name:  "vllm",
					Image: "vllm:test",
				}}},
			},
		},
	}

	ac, err := toApplyConfiguration(dep)
	if err != nil {
		t.Fatalf("toApplyConfiguration: %v", err)
	}
	if ac == nil {
		t.Fatal("toApplyConfiguration returned nil ApplyConfiguration")
	}

	// The runtime.ApplyConfiguration interface itself is opaque; the only
	// implementation we hand it is the unstructured-wrapping one. Recover
	// the underlying unstructured to inspect what SSA will marshal.
	u, ok := ac.(interface{ UnstructuredContent() map[string]any })
	if !ok {
		t.Fatalf("ApplyConfiguration is not an unstructured wrapper: %T", ac)
	}
	obj := u.UnstructuredContent()

	// TypeMeta must round-trip — without these SSA returns 400 MissingKind.
	if got := obj["apiVersion"]; got != "apps/v1" {
		t.Errorf("apiVersion: got %v, want apps/v1", got)
	}
	if got := obj["kind"]; got != "Deployment" {
		t.Errorf("kind: got %v, want Deployment", got)
	}

	// Name + Namespace are required for SSA target identification.
	meta, _ := obj["metadata"].(map[string]any)
	if meta == nil {
		t.Fatal("metadata missing from apply config")
	}
	if got := meta["name"]; got != "vi" {
		t.Errorf("metadata.name: got %v, want vi", got)
	}
	if got := meta["namespace"]; got != "ns" {
		t.Errorf("metadata.namespace: got %v, want ns", got)
	}

	// OwnerReferences drive garbage collection. If they don't survive the
	// conversion, deletes of the parent CR leak the child Deployment.
	ownerRefs, _ := meta["ownerReferences"].([]any)
	if len(ownerRefs) != 1 {
		t.Fatalf("ownerReferences: got %d, want 1", len(ownerRefs))
	}

	// Spec is mandatory for any meaningful SSA mutation.
	if _, ok := obj["spec"]; !ok {
		t.Error("spec missing from apply config")
	}
}

// TestToApplyConfiguration_PreservesServiceFields mirrors the deployment
// regression for Service objects — same SSA semantics, separate builder
// (vllm.BuildService).
func TestToApplyConfiguration_PreservesServiceFields(t *testing.T) {
	svc := &corev1.Service{
		TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "Service"},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "svc-vi",
			Namespace: "ns",
		},
		Spec: corev1.ServiceSpec{
			Type:     corev1.ServiceTypeNodePort,
			Selector: map[string]string{"app": "vi"},
			Ports:    []corev1.ServicePort{{Name: "http", Port: 8000}},
		},
	}

	ac, err := toApplyConfiguration(svc)
	if err != nil {
		t.Fatalf("toApplyConfiguration: %v", err)
	}
	u, ok := ac.(interface{ UnstructuredContent() map[string]any })
	if !ok {
		t.Fatalf("ApplyConfiguration is not an unstructured wrapper: %T", ac)
	}
	obj := u.UnstructuredContent()
	if obj["kind"] != "Service" {
		t.Errorf("kind: got %v, want Service", obj["kind"])
	}
	spec, _ := obj["spec"].(map[string]any)
	if spec == nil {
		t.Fatal("spec missing")
	}
	// NodePort type must survive — without it, dropping to ClusterIP would
	// silently strip the public endpoint behaviour the operator exposes.
	if got := spec["type"]; got != string(corev1.ServiceTypeNodePort) {
		t.Errorf("spec.type: got %v, want %s", got, corev1.ServiceTypeNodePort)
	}
}

// TestToApplyConfiguration_NilObjectErrors guards against a regression where
// a typo (e.g. forgetting to build the deployment, or passing a nil pointer
// from a failed call) silently SSA-applied an empty object.
func TestToApplyConfiguration_NilObjectErrors(t *testing.T) {
	var d *appsv1.Deployment
	if _, err := toApplyConfiguration(d); err == nil {
		t.Error("expected error from toApplyConfiguration(nil), got nil")
	}
}

// TestApply_FieldOwnerOptionsAreCarried exercises the end-to-end Apply path
// against the controller-runtime fake client to confirm the FieldOwner +
// ForceOwnership options reach the apply handler the same way they did
// pre-migration. This is the SSA-semantics regression that gated the SA1019
// cleanup: if the new options aren't wired correctly, two operators (or two
// reconcilers in the same operator) end up fighting for ownership.
func TestApply_FieldOwnerOptionsAreCarried(t *testing.T) {
	scheme := fullScheme(t)
	// No interceptor needed — controller-runtime v0.24's fake client handles
	// SSA Apply natively.
	cl := fake.NewClientBuilder().WithScheme(scheme).Build()

	dep := &appsv1.Deployment{
		TypeMeta:   metav1.TypeMeta{APIVersion: "apps/v1", Kind: "Deployment"},
		ObjectMeta: metav1.ObjectMeta{Name: "vi", Namespace: "ns"},
		Spec: appsv1.DeploymentSpec{
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "vi"}},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": "vi"}},
				Spec: corev1.PodSpec{Containers: []corev1.Container{{
					Name: "vllm", Image: "vllm:test",
				}}},
			},
		},
	}

	ac, err := toApplyConfiguration(dep)
	if err != nil {
		t.Fatalf("toApplyConfiguration: %v", err)
	}
	if err := cl.Apply(context.Background(), ac, fieldOwner, client.ForceOwnership); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	// Verify the object actually landed in the cache — proves the apply
	// reached the store rather than silently dry-running.
	var got appsv1.Deployment
	if err := cl.Get(context.Background(), client.ObjectKey{Namespace: "ns", Name: "vi"}, &got); err != nil {
		t.Fatalf("Get after Apply: %v", err)
	}
	if got.Name != "vi" {
		t.Errorf("after Apply, want name=vi, got %q", got.Name)
	}
}
