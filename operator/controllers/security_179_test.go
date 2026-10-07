package controllers

import (
	"context"
	"encoding/json"
	v1alpha1 "github.com/aatchison/deploy-vllm-k8s/operator/api/v1alpha1"
	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"strings"
	"testing"
)

func TestSecurity179NamespacePolicyRefusesByDefault(t *testing.T) {
	for _, presetPath := range []bool{false, true} {
		t.Run(map[bool]string{false: "overrides", true: "preset"}[presetPath], func(t *testing.T) {
			scheme := fullScheme(t)
			var inst v1alpha1.LongContextInstance
			if err := json.Unmarshal([]byte(`{"metadata":{"name":"engram","namespace":"ns","generation":7},"spec":{"pvcName":"models","hfToken":{"name":"hf","key":"token"},"overrides":{"modelID":"m","migResource":"nvidia.com/mig-4g.96gb","maxModelLen":1024,"kvCacheDtype":"auto","shmSizeLimit":"8Gi","securityProfile":"engram-ipc","engramConfig":"{\"cpu_offload\":true}"}}}`), &inst); err != nil {
				t.Fatal(err)
			}
			objects := []client.Object{&inst}
			if presetPath {
				var preset v1alpha1.LongContextPreset
				if err := json.Unmarshal([]byte(`{"metadata":{"name":"p","namespace":"ns"},"spec":{"modelID":"m","migResource":"nvidia.com/mig-4g.96gb","maxModelLen":1024,"kvCacheDtype":"auto","shmSizeLimit":"8Gi","securityProfile":"engram-ipc","engramConfig":"{\"cpu_offload\":true}"}}`), &preset); err != nil {
					t.Fatal(err)
				}
				inst.Spec.PresetRef = &v1alpha1.LongContextPresetReference{Name: "p"}
				inst.Spec.Overrides = nil
				objects = append(objects, &preset)
			}
			cl := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(&v1alpha1.LongContextInstance{}).WithObjects(objects...).Build()
			rec := record.NewFakeRecorder(8)
			r := &LongContextInstanceReconciler{Client: cl, Scheme: scheme, Recorder: rec}
			_, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(&inst)})
			if err == nil || !strings.Contains(err.Error(), "not allowed") {
				t.Fatalf("namespace policy must refuse before storage lookup/apply: %v", err)
			}
			var got v1alpha1.LongContextInstance
			if err := cl.Get(context.Background(), client.ObjectKeyFromObject(&inst), &got); err != nil {
				t.Fatal(err)
			}
			c := findCond(got.Status.Conditions, v1alpha1.ConditionReady)
			if c == nil || c.Status != metav1.ConditionFalse || c.Reason != v1alpha1.ReasonInvalidConfiguration {
				t.Fatalf("Ready=%+v", c)
			}
			if got.Status.ObservedGeneration != 0 {
				t.Fatalf("denied config advanced observedGeneration=%d", got.Status.ObservedGeneration)
			}
			select {
			case ev := <-rec.Events:
				if !strings.Contains(ev, "Warning") || !strings.Contains(ev, v1alpha1.ReasonInvalidConfiguration) {
					t.Fatalf("event=%s", ev)
				}
			default:
				t.Fatal("missing refusal event")
			}
			var deps appsv1.DeploymentList
			if err := cl.List(context.Background(), &deps); err != nil {
				t.Fatal(err)
			}
			if len(deps.Items) != 0 {
				t.Fatal("denied config created Deployment")
			}
		})
	}
}
