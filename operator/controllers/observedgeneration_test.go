package controllers

import (
	"context"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	vllmv1alpha1 "github.com/aatchison/deploy-vllm-k8s/operator/api/v1alpha1"
)

// Issue #146: status.observedGeneration must only reach metadata.generation
// AFTER the desired-state (Deployment + Service SSA) apply succeeds — never on
// the PresetNotFound / PVCNotFound / resolve-error early-exit paths. Otherwise
// a consumer watching `generation == observedGeneration` would conclude the
// spec was applied when in fact reconcile bailed before touching the cluster.

// TestReconcile_PresetNotFound_DoesNotAdvanceObservedGeneration is the headline
// regression: a missing preset must NOT bump observedGeneration.
func TestReconcile_PresetNotFound_DoesNotAdvanceObservedGeneration(t *testing.T) {
	scheme := fullScheme(t)

	// No ModelPreset object exists → the preset Get returns NotFound.
	inst := &vllmv1alpha1.VLLMInstance{
		ObjectMeta: metav1.ObjectMeta{Name: "vi", Namespace: "ns", Generation: 4},
		Spec: vllmv1alpha1.VLLMInstanceSpec{
			PresetRef: &vllmv1alpha1.PresetReference{Name: "missing"},
			PVCName:   "pvc",
			HFToken:   corev1.SecretKeySelector{LocalObjectReference: corev1.LocalObjectReference{Name: "hf"}, Key: "token"},
			Replicas:  ptr(int32(1)),
		},
	}

	cl := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&vllmv1alpha1.VLLMInstance{}).
		WithObjects(inst).
		Build()

	r := &VLLMInstanceReconciler{Client: cl, Scheme: scheme}
	if _, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(inst)}); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	var got vllmv1alpha1.VLLMInstance
	if err := cl.Get(context.Background(), client.ObjectKeyFromObject(inst), &got); err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Status.ObservedGeneration != 0 {
		t.Errorf("ObservedGeneration must NOT advance on PresetNotFound; got %d, want 0 (apply never ran)", got.Status.ObservedGeneration)
	}
	// Sanity: we really did take the PresetNotFound exit.
	cond := findCond(got.Status.Conditions, vllmv1alpha1.ConditionPresetResolved)
	if cond == nil || cond.Reason != vllmv1alpha1.ReasonPresetNotFound {
		t.Errorf("expected PresetResolved=%s, got %+v", vllmv1alpha1.ReasonPresetNotFound, cond)
	}
}

// TestReconcile_HappyPath_AdvancesObservedGeneration is the positive case: once
// the Deployment + Service applies succeed, observedGeneration must equal the
// spec generation.
func TestReconcile_HappyPath_AdvancesObservedGeneration(t *testing.T) {
	scheme := fullScheme(t)

	preset := &vllmv1alpha1.ModelPreset{
		ObjectMeta: metav1.ObjectMeta{Name: "p", Namespace: "ns"},
		Spec:       presetSpec(),
	}
	pvc := &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{Name: "pvc", Namespace: "ns"},
	}
	inst := &vllmv1alpha1.VLLMInstance{
		ObjectMeta: metav1.ObjectMeta{Name: "vi", Namespace: "ns", Generation: 4},
		Spec: vllmv1alpha1.VLLMInstanceSpec{
			PresetRef: &vllmv1alpha1.PresetReference{Name: "p"},
			PVCName:   "pvc",
			HFToken:   corev1.SecretKeySelector{LocalObjectReference: corev1.LocalObjectReference{Name: "hf"}, Key: "token"},
			Replicas:  ptr(int32(1)),
		},
	}
	// Pre-create stub Deployment + Service so Reconcile's post-Apply Get()s find
	// them (the SSA Apply is no-op'd by ssaTolerantInterceptor).
	depStub := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: inst.Name, Namespace: inst.Namespace}}
	svcStub := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: "svc-" + inst.Name, Namespace: inst.Namespace},
		Spec:       corev1.ServiceSpec{Ports: []corev1.ServicePort{{NodePort: 32000}}},
	}

	cl := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&vllmv1alpha1.VLLMInstance{}).
		WithObjects(preset, pvc, inst, depStub, svcStub).
		WithInterceptorFuncs(ssaTolerantInterceptor).
		Build()

	r := &VLLMInstanceReconciler{Client: cl, Scheme: scheme}
	if _, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(inst)}); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	var got vllmv1alpha1.VLLMInstance
	if err := cl.Get(context.Background(), client.ObjectKeyFromObject(inst), &got); err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Status.ObservedGeneration != 4 {
		t.Errorf("ObservedGeneration must equal generation after apply; got %d, want 4", got.Status.ObservedGeneration)
	}
}

// TestReconcileLongContext_PresetNotFound_DoesNotAdvanceObservedGeneration
// mirrors the PresetNotFound regression for the LongContextInstance controller.
func TestReconcileLongContext_PresetNotFound_DoesNotAdvanceObservedGeneration(t *testing.T) {
	scheme := fullScheme(t)

	inst := &vllmv1alpha1.LongContextInstance{
		ObjectMeta: metav1.ObjectMeta{Name: "lci", Namespace: "ns", Generation: 4},
		Spec: vllmv1alpha1.LongContextInstanceSpec{
			PresetRef: &vllmv1alpha1.LongContextPresetReference{Name: "missing"},
			PVCName:   "pvc",
			HFToken:   corev1.SecretKeySelector{LocalObjectReference: corev1.LocalObjectReference{Name: "hf"}, Key: "token"},
			Replicas:  ptr(int32(1)),
		},
	}

	cl := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&vllmv1alpha1.LongContextInstance{}).
		WithObjects(inst).
		Build()

	r := &LongContextInstanceReconciler{Client: cl, Scheme: scheme}
	if _, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(inst)}); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	var got vllmv1alpha1.LongContextInstance
	if err := cl.Get(context.Background(), client.ObjectKeyFromObject(inst), &got); err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Status.ObservedGeneration != 0 {
		t.Errorf("ObservedGeneration must NOT advance on PresetNotFound; got %d, want 0", got.Status.ObservedGeneration)
	}
}

// TestReconcileLongContext_HappyPath_AdvancesObservedGeneration mirrors the
// positive case for the LongContextInstance controller.
func TestReconcileLongContext_HappyPath_AdvancesObservedGeneration(t *testing.T) {
	scheme := fullScheme(t)

	lcPreset := &vllmv1alpha1.LongContextPreset{
		ObjectMeta: metav1.ObjectMeta{Name: "lcp", Namespace: "ns"},
		Spec:       longContextPresetSpec(),
	}
	pvc := &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{Name: "pvc", Namespace: "ns"},
	}
	inst := &vllmv1alpha1.LongContextInstance{
		ObjectMeta: metav1.ObjectMeta{Name: "lci", Namespace: "ns", Generation: 4},
		Spec: vllmv1alpha1.LongContextInstanceSpec{
			PresetRef: &vllmv1alpha1.LongContextPresetReference{Name: "lcp"},
			PVCName:   "pvc",
			HFToken:   corev1.SecretKeySelector{LocalObjectReference: corev1.LocalObjectReference{Name: "hf"}, Key: "token"},
			Replicas:  ptr(int32(1)),
		},
	}
	depStub := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: inst.Name, Namespace: inst.Namespace}}
	svcStub := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: "svc-" + inst.Name, Namespace: inst.Namespace},
		Spec:       corev1.ServiceSpec{Ports: []corev1.ServicePort{{NodePort: 32001}}},
	}

	cl := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&vllmv1alpha1.LongContextInstance{}).
		WithObjects(lcPreset, pvc, inst, depStub, svcStub).
		WithInterceptorFuncs(ssaTolerantInterceptor).
		Build()

	r := &LongContextInstanceReconciler{Client: cl, Scheme: scheme}
	if _, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(inst)}); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	var got vllmv1alpha1.LongContextInstance
	if err := cl.Get(context.Background(), client.ObjectKeyFromObject(inst), &got); err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Status.ObservedGeneration != 4 {
		t.Errorf("ObservedGeneration must equal generation after apply; got %d, want 4", got.Status.ObservedGeneration)
	}
}

// findCond is a tiny local helper to avoid importing apimeta just for a lookup.
func findCond(conds []metav1.Condition, condType string) *metav1.Condition {
	for i := range conds {
		if conds[i].Type == condType {
			return &conds[i]
		}
	}
	return nil
}
