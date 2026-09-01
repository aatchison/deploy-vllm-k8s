package controllers

import (
 "context"
 "testing"
 appsv1 "k8s.io/api/apps/v1"
 metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
 "k8s.io/apimachinery/pkg/runtime"
 "sigs.k8s.io/controller-runtime/pkg/client"
 "sigs.k8s.io/controller-runtime/pkg/client/interceptor"
 "sigs.k8s.io/controller-runtime/pkg/client/fake"
 vllmv1alpha1 "github.com/aatchison/deploy-vllm-k8s/operator/api/v1alpha1"
)
func testOwner(t *testing.T) client.Object { return &vllmv1alpha1.VLLMInstance{ObjectMeta: metav1.ObjectMeta{Name:"vi",Namespace:"ns",UID:"owner"}} }
func fakeClientWithApply(t *testing.T, dep *appsv1.Deployment, funcs interceptor.Funcs) client.Client { return fake.NewClientBuilder().WithScheme(fullScheme(t)).WithObjects(dep).WithInterceptorFuncs(funcs).Build() }

func TestRemediationApplyPayloadHasNoNullOrUnrelatedFields(t *testing.T) {
 owner := testOwner(t)
 dep := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: owner.GetName(), Namespace: owner.GetNamespace(), UID: owner.GetUID(), OwnerReferences: []metav1.OwnerReference{{UID: owner.GetUID(), Controller: ptrBool(true)}}, ResourceVersion:"7"}, Spec: appsv1.DeploymentSpec{Replicas: ptr(int32(2))}}
 var applied runtime.ApplyConfiguration
 cl := fakeClientWithApply(t, dep, interceptor.Funcs{Apply: func(ctx context.Context, c client.WithWatch, obj runtime.ApplyConfiguration, opts ...client.ApplyOption) error { applied=obj; return nil }})
 _, err := remediateUnsafeDeployment(context.Background(), cl, cl, owner); if err == nil { t.Fatal("expected readback failure because fake apply does not materialize") }
 u:=applied.(interface{UnstructuredContent() map[string]any}).UnstructuredContent()
 if _,ok:=u["status"]; ok { t.Fatalf("status must not be applied: %#v",u) }
 spec:=u["spec"].(map[string]any)
 if len(spec)!=1 || spec["replicas"] != int64(1) { t.Fatalf("spec must contain only replicas=1: %#v",spec) }
}
