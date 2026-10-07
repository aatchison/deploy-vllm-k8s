package controllers

import (
	"context"
	"fmt"
	api "github.com/aatchison/deploy-vllm-k8s/operator/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	"strings"
	"testing"
)

func TestFlags178InvalidJSONNeverApplies(t *testing.T) {
	for _, field := range []string{"engramConfig"} {
		t.Run(field, func(t *testing.T) {
			scheme := fullScheme(t)
			overrides := &api.LongContextOverrides{ModelID: strPtr("m"), MIGResource: strPtr("nvidia.com/mig-4g.96gb"), MaxModelLen: intPtr(4096), KVCacheDtype: strPtr("auto"), SHMSizeLimit: strPtr("8Gi")}
			switch field {
			case "engramConfig":
				overrides.EngramConfig = strPtr(`{broken}`)
			}
			inst := &api.LongContextInstance{ObjectMeta: metav1.ObjectMeta{Name: "lci", Namespace: "ns"}, Spec: api.LongContextInstanceSpec{Overrides: overrides, PVCName: "models", HFToken: corev1.SecretKeySelector{LocalObjectReference: corev1.LocalObjectReference{Name: "hf"}, Key: "token"}}}
			pvc := &corev1.PersistentVolumeClaim{ObjectMeta: metav1.ObjectMeta{Name: "models", Namespace: "ns"}, Spec: corev1.PersistentVolumeClaimSpec{AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce}}}
			applies := 0
			cl := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(&api.LongContextInstance{}).WithObjects(inst, pvc).WithInterceptorFuncs(interceptor.Funcs{Apply: func(ctx context.Context, c client.WithWatch, obj runtime.ApplyConfiguration, opts ...client.ApplyOption) error {
				applies++
				return fmt.Errorf("apply sentinel")
			}}).Build()
			r := &LongContextInstanceReconciler{Client: cl, Scheme: scheme}
			reconcile := func() error {
				_, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(inst)})
				return err
			}
			if err := reconcile(); err == nil || !strings.Contains(err.Error(), "invalid "+field) {
				t.Fatalf("err=%v", err)
			}
			if applies != 0 {
				t.Fatalf("invalid JSON reached Apply %d times", applies)
			}
			if err := cl.Get(context.Background(), client.ObjectKeyFromObject(inst), inst); err != nil {
				t.Fatal(err)
			}
			if cond := findCond(inst.Status.Conditions, api.ConditionReady); cond == nil || cond.Status != metav1.ConditionFalse || cond.Reason != api.ReasonInvalidConfiguration {
				t.Fatalf("ready=%+v", cond)
			}
			// Positive control proves the Apply interceptor is reachable with valid JSON.
			switch field {
			case "engramConfig":
				inst.Spec.Overrides.EngramConfig = strPtr(`{}`)
			}
			if err := cl.Update(context.Background(), inst); err != nil {
				t.Fatal(err)
			}
			if err := reconcile(); err == nil || !strings.Contains(err.Error(), "apply sentinel") {
				t.Fatalf("positive control err=%v", err)
			}
			if applies == 0 {
				t.Fatal("positive control did not reach Apply")
			}
		})
	}
}
