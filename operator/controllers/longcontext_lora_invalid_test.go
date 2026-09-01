package controllers

import (
	"context"
	v1alpha1 "github.com/aatchison/deploy-vllm-k8s/operator/api/v1alpha1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"strings"
	"testing"
)

func TestReconcileLongContextInvalidLoraWarnsStatusAndDoesNotApply(t *testing.T) {
	scheme := fullScheme(t)
	inst := &v1alpha1.LongContextInstance{ObjectMeta: metav1.ObjectMeta{Name: "lci", Namespace: "ns", Generation: 1}, Spec: v1alpha1.LongContextInstanceSpec{
		Overrides: &v1alpha1.LongContextOverrides{ModelID: strPtr("m"), MIGResource: strPtr("nvidia.com/mig-1g.5gb"), MaxModelLen: intPtr(1024), KVCacheDtype: strPtr("fp8_e4m3"), LoraModules: strPtr("adapter=/models/foo/../bar")},
		PVCName:   "pvc", HFToken: corev1.SecretKeySelector{LocalObjectReference: corev1.LocalObjectReference{Name: "hf"}, Key: "token"},
	}}
	cl := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(&v1alpha1.LongContextInstance{}).WithObjects(inst).Build()
	rec := record.NewFakeRecorder(8)
	r := &LongContextInstanceReconciler{Client: cl, Scheme: scheme, Recorder: rec}
	if _, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(inst)}); err == nil || !strings.Contains(err.Error(), "invalid loraModules") {
		t.Fatalf("Reconcile err=%v", err)
	}
	select {
	case ev := <-rec.Events:
		if !strings.Contains(ev, "Warning") || !strings.Contains(ev, v1alpha1.ReasonInvalidConfiguration) {
			t.Fatalf("event=%q", ev)
		}
	default:
		t.Fatal("expected invalid-config Warning event")
	}
	var got v1alpha1.LongContextInstance
	if err := cl.Get(context.Background(), client.ObjectKeyFromObject(inst), &got); err != nil {
		t.Fatal(err)
	}
	if c := findCond(got.Status.Conditions, v1alpha1.ConditionReady); c == nil || c.Status != metav1.ConditionFalse || c.Reason != v1alpha1.ReasonInvalidConfiguration {
		t.Fatalf("ready condition=%+v", c)
	}
	var deployments appsv1.DeploymentList
	if err := cl.List(context.Background(), &deployments, client.InNamespace("ns")); err != nil {
		t.Fatal(err)
	}
	if len(deployments.Items) != 0 {
		t.Fatalf("invalid config applied %d Deployments", len(deployments.Items))
	}
	var services corev1.ServiceList
	if err := cl.List(context.Background(), &services, client.InNamespace("ns")); err != nil {
		t.Fatal(err)
	}
	if len(services.Items) != 0 {
		t.Fatalf("invalid config applied %d Services", len(services.Items))
	}
}

func strPtr(s string) *string { return &s }
func intPtr(i int32) *int32   { return &i }
