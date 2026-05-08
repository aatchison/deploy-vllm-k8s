package controllers

import (
	"context"
	"testing"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	vllmv1alpha1 "github.com/aatchison/deploy-vllm-k8s/operator/api/v1alpha1"
)

// newScheme registers the v1alpha1 types for the fake client.
func newScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	if err := vllmv1alpha1.AddToScheme(s); err != nil {
		t.Fatalf("AddToScheme: %v", err)
	}
	return s
}

// TestSetVLLMCondition_PreservesLastTransitionTimeOnNoOp asserts that the
// helper dedupes writes: when the same condition is set twice with identical
// status/reason/message, LastTransitionTime is unchanged on the second call.
// The previous hand-rolled setCondition always rewrote the condition fields
// (even on no-op), causing churn on Status.Update; apimeta.SetStatusCondition
// (the upstream helper we now wrap) handles this correctly.
func TestSetVLLMCondition_PreservesLastTransitionTimeOnNoOp(t *testing.T) {
	inst := &vllmv1alpha1.VLLMInstance{
		ObjectMeta: metav1.ObjectMeta{Name: "foo", Namespace: "bar", Generation: 7},
	}

	setVLLMCondition(inst, vllmv1alpha1.ConditionReady, metav1.ConditionFalse,
		vllmv1alpha1.ReasonDeploymentUnavailable, "still warming up")

	if len(inst.Status.Conditions) != 1 {
		t.Fatalf("expected 1 condition, got %d", len(inst.Status.Conditions))
	}
	first := inst.Status.Conditions[0].LastTransitionTime
	if first.IsZero() {
		t.Fatal("expected LastTransitionTime to be set on first call")
	}
	if got := inst.Status.Conditions[0].ObservedGeneration; got != 7 {
		t.Errorf("ObservedGeneration: got %d, want 7", got)
	}

	// Second call with identical fields — LastTransitionTime must NOT change.
	setVLLMCondition(inst, vllmv1alpha1.ConditionReady, metav1.ConditionFalse,
		vllmv1alpha1.ReasonDeploymentUnavailable, "still warming up")
	if !inst.Status.Conditions[0].LastTransitionTime.Equal(&first) {
		t.Errorf("LastTransitionTime mutated on no-op write: was %v, now %v",
			first, inst.Status.Conditions[0].LastTransitionTime)
	}

	// Status flip — LastTransitionTime SHOULD update.
	setVLLMCondition(inst, vllmv1alpha1.ConditionReady, metav1.ConditionTrue,
		vllmv1alpha1.ReasonAllReady, "all ready")
	if inst.Status.Conditions[0].LastTransitionTime.Equal(&first) {
		t.Error("LastTransitionTime should have updated on status flip")
	}
	if got := inst.Status.Conditions[0].Status; got != metav1.ConditionTrue {
		t.Errorf("Status: got %q, want True", got)
	}
}

// TestSetLongContextCondition_PreservesLastTransitionTimeOnNoOp mirrors the
// VLLMInstance test for LongContextInstance, since the two controllers share
// the pattern verbatim.
func TestSetLongContextCondition_PreservesLastTransitionTimeOnNoOp(t *testing.T) {
	inst := &vllmv1alpha1.LongContextInstance{
		ObjectMeta: metav1.ObjectMeta{Name: "foo", Namespace: "bar", Generation: 3},
	}

	setLongContextCondition(inst, vllmv1alpha1.ConditionStorageReady, metav1.ConditionTrue,
		vllmv1alpha1.ReasonPVCFound, "PVC exists")
	first := inst.Status.Conditions[0].LastTransitionTime
	if first.IsZero() {
		t.Fatal("expected LastTransitionTime to be set on first call")
	}

	setLongContextCondition(inst, vllmv1alpha1.ConditionStorageReady, metav1.ConditionTrue,
		vllmv1alpha1.ReasonPVCFound, "PVC exists")
	if !inst.Status.Conditions[0].LastTransitionTime.Equal(&first) {
		t.Errorf("LastTransitionTime mutated on no-op write: was %v, now %v",
			first, inst.Status.Conditions[0].LastTransitionTime)
	}
}

// TestPatchStatus_HappyPath verifies patchStatus uses Patch (not Update) and
// stamps ObservedGeneration. The fake client's status subresource supports
// Patch, so this exercises the merge-from-orig path end-to-end.
func TestPatchStatus_HappyPath(t *testing.T) {
	scheme := newScheme(t)
	inst := &vllmv1alpha1.VLLMInstance{
		ObjectMeta: metav1.ObjectMeta{Name: "foo", Namespace: "bar", Generation: 5},
	}
	cl := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&vllmv1alpha1.VLLMInstance{}).
		WithObjects(inst).
		Build()

	r := &VLLMInstanceReconciler{Client: cl, Scheme: scheme}

	// Read fresh, copy as orig, mutate, patch — the same shape Reconcile uses.
	var got vllmv1alpha1.VLLMInstance
	if err := cl.Get(context.Background(), client.ObjectKeyFromObject(inst), &got); err != nil {
		t.Fatalf("Get: %v", err)
	}
	orig := got.DeepCopy()
	setVLLMCondition(&got, vllmv1alpha1.ConditionReady, metav1.ConditionTrue,
		vllmv1alpha1.ReasonAllReady, "all ready")

	res, err := r.patchStatus(context.Background(), &got, orig, ctrl.Result{})
	if err != nil {
		t.Fatalf("patchStatus: %v", err)
	}
	if res != (ctrl.Result{}) {
		t.Errorf("Result: got %+v, want zero", res)
	}

	// Re-Get and verify the condition + ObservedGeneration are persisted.
	var stored vllmv1alpha1.VLLMInstance
	if err := cl.Get(context.Background(), client.ObjectKeyFromObject(inst), &stored); err != nil {
		t.Fatalf("Get after patch: %v", err)
	}
	if stored.Status.ObservedGeneration != 5 {
		t.Errorf("ObservedGeneration: got %d, want 5", stored.Status.ObservedGeneration)
	}
	if len(stored.Status.Conditions) != 1 || stored.Status.Conditions[0].Type != vllmv1alpha1.ConditionReady {
		t.Errorf("Conditions: got %+v", stored.Status.Conditions)
	}
}

// TestPatchStatus_SurfacesConflictAsError is the headline regression test for
// bug #2 in issue #33: previously, a Conflict on Status.Update was swallowed
// as Result{Requeue: true}, nil, bypassing the workqueue's exponential
// backoff. The fix returns the error so controller-runtime can rate-limit.
func TestPatchStatus_SurfacesConflictAsError(t *testing.T) {
	scheme := newScheme(t)
	inst := &vllmv1alpha1.VLLMInstance{
		ObjectMeta: metav1.ObjectMeta{Name: "foo", Namespace: "bar", Generation: 1},
	}

	// Interceptor that fails every status patch with a Conflict, simulating a
	// concurrent writer (e.g. a parallel reconcile triggered by an Owns()
	// watch firing on the Deployment).
	conflictErr := apierrors.NewConflict(
		schema.GroupResource{Group: vllmv1alpha1.GroupVersion.Group, Resource: "vllminstances"},
		inst.Name,
		nil,
	)
	cl := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&vllmv1alpha1.VLLMInstance{}).
		WithObjects(inst).
		WithInterceptorFuncs(interceptor.Funcs{
			SubResourcePatch: func(_ context.Context, _ client.Client, _ string, _ client.Object, _ client.Patch, _ ...client.SubResourcePatchOption) error {
				return conflictErr
			},
		}).
		Build()

	r := &VLLMInstanceReconciler{Client: cl, Scheme: scheme}
	var got vllmv1alpha1.VLLMInstance
	if err := cl.Get(context.Background(), client.ObjectKeyFromObject(inst), &got); err != nil {
		t.Fatalf("Get: %v", err)
	}
	orig := got.DeepCopy()
	setVLLMCondition(&got, vllmv1alpha1.ConditionReady, metav1.ConditionTrue,
		vllmv1alpha1.ReasonAllReady, "ready")

	res, err := r.patchStatus(context.Background(), &got, orig, ctrl.Result{})
	if err == nil {
		t.Fatal("expected error on Conflict, got nil (would have hot-looped)")
	}
	if !apierrors.IsConflict(err) {
		t.Errorf("expected IsConflict error, got: %v", err)
	}
	// Confirm the previous bug shape is gone: Result{Requeue: true} with nil
	// error is what the buggy code returned to bypass backoff.
	if res.Requeue {
		t.Errorf("Result.Requeue must be false on error path so workqueue backoff applies; got %+v", res)
	}
}

// TestLongContextPatchStatus_SurfacesConflictAsError mirrors the conflict
// regression test for the LongContextInstance controller.
func TestLongContextPatchStatus_SurfacesConflictAsError(t *testing.T) {
	scheme := newScheme(t)
	inst := &vllmv1alpha1.LongContextInstance{
		ObjectMeta: metav1.ObjectMeta{Name: "lc", Namespace: "bar", Generation: 1},
	}
	conflictErr := apierrors.NewConflict(
		schema.GroupResource{Group: vllmv1alpha1.GroupVersion.Group, Resource: "longcontextinstances"},
		inst.Name,
		nil,
	)
	cl := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&vllmv1alpha1.LongContextInstance{}).
		WithObjects(inst).
		WithInterceptorFuncs(interceptor.Funcs{
			SubResourcePatch: func(_ context.Context, _ client.Client, _ string, _ client.Object, _ client.Patch, _ ...client.SubResourcePatchOption) error {
				return conflictErr
			},
		}).
		Build()

	r := &LongContextInstanceReconciler{Client: cl, Scheme: scheme}
	var got vllmv1alpha1.LongContextInstance
	if err := cl.Get(context.Background(), client.ObjectKeyFromObject(inst), &got); err != nil {
		t.Fatalf("Get: %v", err)
	}
	orig := got.DeepCopy()
	setLongContextCondition(&got, vllmv1alpha1.ConditionReady, metav1.ConditionTrue,
		vllmv1alpha1.ReasonAllReady, "ready")

	res, err := r.patchStatus(context.Background(), &got, orig, ctrl.Result{})
	if err == nil {
		t.Fatal("expected error on Conflict, got nil")
	}
	if !apierrors.IsConflict(err) {
		t.Errorf("expected IsConflict error, got: %v", err)
	}
	if res.Requeue {
		t.Errorf("Result.Requeue must be false on error path; got %+v", res)
	}
}
