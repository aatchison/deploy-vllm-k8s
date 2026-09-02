package controllers

import (
	"context"
	"encoding/json"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	vllmv1alpha1 "github.com/aatchison/deploy-vllm-k8s/operator/api/v1alpha1"
)

func TestRemediationApplyPayloadIsIdentityAndReplicasOnly(t *testing.T) {
	owner := &vllmv1alpha1.VLLMInstance{ObjectMeta: metav1.ObjectMeta{Name: "vi", Namespace: "ns", UID: types.UID("owner-uid")}}
	dep := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: "vi", Namespace: "ns", UID: types.UID("dep-uid"), ResourceVersion: "7", OwnerReferences: []metav1.OwnerReference{{UID: owner.GetUID(), Controller: ptrBool(true)}}}, Spec: appsv1.DeploymentSpec{Replicas: ptr(int32(2))}}
	applyStarted := false
	var payload map[string]interface{}
	inter := interceptor.Funcs{Apply: func(_ context.Context, _ client.WithWatch, ac runtime.ApplyConfiguration, _ ...client.ApplyOption) error {
		applyStarted = true
		raw, err := json.Marshal(ac)
		if err != nil {
			t.Fatalf("marshal apply config %T: %v", ac, err)
		}
		if err := json.Unmarshal(raw, &payload); err != nil {
			t.Fatalf("unmarshal apply config payload: %v", err)
		}
		return nil
	}}
	cl := fake.NewClientBuilder().WithScheme(fullScheme(t)).WithObjects(dep).WithInterceptorFuncs(inter).Build()
	post := dep.DeepCopy()
	post.Spec.Replicas = ptr(int32(1))
	reader := &orderingReader{Reader: cl, applyStarted: &applyStarted, postApply: post}
	if _, err := remediateUnsafeDeployment(context.Background(), cl, reader, owner); err != nil {
		t.Fatal(err)
	}
	if payload["apiVersion"] != "apps/v1" || payload["kind"] != "Deployment" {
		t.Fatalf("identity=%v", payload)
	}
	metadata, ok := payload["metadata"].(map[string]interface{})
	if !ok {
		t.Fatalf("metadata has unexpected type: %T", payload["metadata"])
	}
	if metadata["name"] != "vi" || metadata["namespace"] != "ns" || metadata["resourceVersion"] != "7" {
		t.Fatalf("metadata=%v", metadata)
	}
	if len(metadata) != 3 {
		t.Fatalf("metadata contains fields beyond identity/resourceVersion: %v", metadata)
	}
	spec, ok := payload["spec"].(map[string]interface{})
	if !ok {
		t.Fatalf("spec has unexpected type: %T", payload["spec"])
	}
	if len(spec) != 1 || spec["replicas"] != float64(1) {
		t.Fatalf("spec=%v", spec)
	}
}
