package controllers

import (
	"context"
	"strings"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	vllmv1alpha1 "github.com/aatchison/deploy-vllm-k8s/operator/api/v1alpha1"
)

func TestReconcileInvalidLoraModulesFailsClosed(t *testing.T) {
	scheme := fullScheme(t)
	presetSpec := presetSpec()
	presetSpec.EnableLora = true
	presetSpec.LoraModules = "fleetv1=/models/../../etc/passwd"
	preset := &vllmv1alpha1.ModelPreset{ObjectMeta: metav1.ObjectMeta{Name: "p", Namespace: "ns"}, Spec: presetSpec}
	pvc := &corev1.PersistentVolumeClaim{ObjectMeta: metav1.ObjectMeta{Name: "pvc", Namespace: "ns"}}
	inst := &vllmv1alpha1.VLLMInstance{
		ObjectMeta: metav1.ObjectMeta{Name: "vi", Namespace: "ns", Generation: 1},
		Spec: vllmv1alpha1.VLLMInstanceSpec{
			PresetRef: &vllmv1alpha1.PresetReference{Name: "p"}, PVCName: "pvc",
			HFToken: corev1.SecretKeySelector{LocalObjectReference: corev1.LocalObjectReference{Name: "hf"}, Key: "token"},
		},
	}
	cl := fake.NewClientBuilder().WithScheme(scheme).
		WithStatusSubresource(&vllmv1alpha1.VLLMInstance{}).
		WithObjects(preset, pvc, inst).Build()
	rec := record.NewFakeRecorder(1)
	r := &VLLMInstanceReconciler{Client: cl, Scheme: scheme, Recorder: rec}
	_, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(inst)})
	if err == nil || !strings.Contains(err.Error(), "invalid loraModules") {
		t.Fatalf("Reconcile error = %v, want invalid loraModules", err)
	}
	select {
	case event := <-rec.Events:
		if !strings.Contains(event, "Warning") || !strings.Contains(event, vllmv1alpha1.ReasonInvalidConfiguration) {
			t.Fatalf("invalid LoRA event = %q, want Warning/%s", event, vllmv1alpha1.ReasonInvalidConfiguration)
		}
	case <-time.After(time.Second):
		t.Fatal("invalid LoRA reconcile emitted no Warning event")
	}
	var got vllmv1alpha1.VLLMInstance
	if err := cl.Get(context.Background(), client.ObjectKeyFromObject(inst), &got); err != nil {
		t.Fatal(err)
	}
	cond := apimeta.FindStatusCondition(got.Status.Conditions, vllmv1alpha1.ConditionReady)
	if cond == nil || cond.Status != metav1.ConditionFalse || cond.Reason != vllmv1alpha1.ReasonInvalidConfiguration {
		t.Fatalf("Ready condition = %#v, want False/%s", cond, vllmv1alpha1.ReasonInvalidConfiguration)
	}
	var dep appsv1.Deployment
	if err := cl.Get(context.Background(), client.ObjectKey{Name: inst.Name, Namespace: inst.Namespace}, &dep); err == nil {
		t.Fatal("invalid LoRA configuration must not create a Deployment")
	}
}
