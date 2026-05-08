package controllers

import (
	"context"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	vllmv1alpha1 "github.com/aatchison/deploy-vllm-k8s/operator/api/v1alpha1"
)

// TestSetVLLMCondition_ReturnsTransition pins the helper's transition return
// value: true on first write, false on no-op, true on status flip. The Reconcile
// sites depend on this contract to gate event emission and avoid per-reconcile
// spam (issue #39).
func TestSetVLLMCondition_ReturnsTransition(t *testing.T) {
	inst := &vllmv1alpha1.VLLMInstance{
		ObjectMeta: metav1.ObjectMeta{Name: "foo", Namespace: "bar", Generation: 1},
	}

	if !setVLLMCondition(inst, vllmv1alpha1.ConditionReady, metav1.ConditionFalse,
		vllmv1alpha1.ReasonDeploymentUnavailable, "warming up") {
		t.Error("first write must report transitioned=true")
	}
	if setVLLMCondition(inst, vllmv1alpha1.ConditionReady, metav1.ConditionFalse,
		vllmv1alpha1.ReasonDeploymentUnavailable, "warming up") {
		t.Error("identical re-write must report transitioned=false (steady-state, no event)")
	}
	if !setVLLMCondition(inst, vllmv1alpha1.ConditionReady, metav1.ConditionTrue,
		vllmv1alpha1.ReasonAllReady, "ready") {
		t.Error("status flip must report transitioned=true")
	}
	// Reason-only change with status unchanged: not a transition for our purposes
	// (the issue is about False→True or first-False, not noisy reason flapping).
	if setVLLMCondition(inst, vllmv1alpha1.ConditionReady, metav1.ConditionTrue,
		"DifferentReason", "different msg") {
		t.Error("reason-only change with same Status must report transitioned=false")
	}
}

// TestSetLongContextCondition_ReturnsTransition mirrors the VLLM helper's
// transition contract for the LongContextInstance controller.
func TestSetLongContextCondition_ReturnsTransition(t *testing.T) {
	inst := &vllmv1alpha1.LongContextInstance{
		ObjectMeta: metav1.ObjectMeta{Name: "lc", Namespace: "bar", Generation: 1},
	}
	if !setLongContextCondition(inst, vllmv1alpha1.ConditionStorageReady, metav1.ConditionFalse,
		vllmv1alpha1.ReasonPVCNotFound, "no pvc") {
		t.Error("first write must report transitioned=true")
	}
	if setLongContextCondition(inst, vllmv1alpha1.ConditionStorageReady, metav1.ConditionFalse,
		vllmv1alpha1.ReasonPVCNotFound, "no pvc") {
		t.Error("identical re-write must report transitioned=false")
	}
	if !setLongContextCondition(inst, vllmv1alpha1.ConditionStorageReady, metav1.ConditionTrue,
		vllmv1alpha1.ReasonPVCFound, "ok") {
		t.Error("status flip must report transitioned=true")
	}
}

// TestVLLMReconcile_EmitsPresetNotFoundEvent is the headline regression for
// issue #39: a fresh VLLMInstance referencing a missing ModelPreset must emit
// a Warning event (so kubectl describe shows a breadcrumb) on the first
// reconcile, AND must NOT emit a duplicate event on a steady-state requeue
// when the preset is still missing.
func TestVLLMReconcile_EmitsPresetNotFoundEvent(t *testing.T) {
	scheme := newScheme(t)

	inst := &vllmv1alpha1.VLLMInstance{
		ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default", Generation: 1},
		Spec: vllmv1alpha1.VLLMInstanceSpec{
			PresetRef: &vllmv1alpha1.PresetReference{Name: "missing-preset"},
			PVCName:   "test-pvc",
		},
	}
	cl := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&vllmv1alpha1.VLLMInstance{}).
		WithObjects(inst).
		Build()

	rec := record.NewFakeRecorder(8)
	r := &VLLMInstanceReconciler{Client: cl, Scheme: scheme, Recorder: rec}

	// First reconcile — PresetNotFound condition flips False, must emit a Warning event.
	if _, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(inst)}); err != nil {
		t.Fatalf("first Reconcile: %v", err)
	}
	select {
	case ev := <-rec.Events:
		// FakeRecorder formats as "<eventType> <reason> <message>".
		if !strings.Contains(ev, "Warning") || !strings.Contains(ev, vllmv1alpha1.ReasonPresetNotFound) {
			t.Errorf("expected Warning PresetNotFound event, got: %q", ev)
		}
		if !strings.Contains(ev, "missing-preset") {
			t.Errorf("event missing preset name: %q", ev)
		}
	default:
		t.Fatal("expected a Warning PresetNotFound event after first reconcile, got none")
	}

	// Second reconcile — preset still missing, condition is already False.
	// The transition check must squash the event so steady-state reconciles
	// don't spam the events stream (issue #39 acceptance: "no event spam").
	if _, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(inst)}); err != nil {
		t.Fatalf("second Reconcile: %v", err)
	}
	select {
	case ev := <-rec.Events:
		t.Errorf("steady-state reconcile must not emit a duplicate event, got: %q", ev)
	default:
		// good — no event
	}
}

// TestVLLMReconcile_EventfNoopWhenRecorderNil guarantees the eventf helper is
// safe when the reconciler is built without a Recorder (older tests, or a
// future refactor that drops the field). Without this, tests that exercise
// Reconcile would NPE rather than skip event emission.
func TestVLLMReconcile_EventfNoopWhenRecorderNil(t *testing.T) {
	r := &VLLMInstanceReconciler{}
	// Should not panic.
	r.eventf(&vllmv1alpha1.VLLMInstance{}, corev1.EventTypeWarning, "Reason", "msg")
}

// TestLongContextReconcile_EventfNoopWhenRecorderNil mirrors the VLLM nil-
// recorder safety check.
func TestLongContextReconcile_EventfNoopWhenRecorderNil(t *testing.T) {
	r := &LongContextInstanceReconciler{}
	r.eventf(&vllmv1alpha1.LongContextInstance{}, corev1.EventTypeWarning, "Reason", "msg")
}
