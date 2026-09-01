package controllers

import (
	"context"
	"fmt"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	vllmv1alpha1 "github.com/aatchison/deploy-vllm-k8s/operator/api/v1alpha1"
	"github.com/aatchison/deploy-vllm-k8s/operator/internal/vllm"
)

func TestValidateReplicaStorage(t *testing.T) {
	tests := []struct {
		name     string
		mode     corev1.PersistentVolumeAccessMode
		replicas int32
		readOnly bool
		wantErr  bool
	}{
		{"single RWO allowed", corev1.ReadWriteOnce, 1, false, false},
		{"two RWX allowed", corev1.ReadWriteMany, 2, false, false},
		{"two ROX read-only allowed", corev1.ReadOnlyMany, 2, true, false},
		{"two ROX writable rejected", corev1.ReadOnlyMany, 2, false, true},
		{"two RWO rejected", corev1.ReadWriteOnce, 2, false, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pvc := &corev1.PersistentVolumeClaim{Spec: corev1.PersistentVolumeClaimSpec{AccessModes: []corev1.PersistentVolumeAccessMode{tt.mode}}}
			if gotErr := validateReplicaStorage(pvc, tt.replicas, tt.readOnly); (gotErr != nil) != tt.wantErr {
				t.Fatalf("error=%v, wantErr=%v", gotErr, tt.wantErr)
			}
		})
	}
}

func TestValidateReplicaStorageUnknownMixedAndBoundaries(t *testing.T) {
	unknown := corev1.PersistentVolumeAccessMode("ReadWriteOncePod")
	mixed := []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce, corev1.ReadWriteMany}
	cases := []struct {
		name     string
		modes    []corev1.PersistentVolumeAccessMode
		replicas int32
		readOnly bool
		wantErr  bool
	}{
		{"zero replicas with unknown mode", []corev1.PersistentVolumeAccessMode{unknown}, 0, false, false},
		{"one replica with unknown mode", []corev1.PersistentVolumeAccessMode{unknown}, 1, false, false},
		{"two replicas unknown mode", []corev1.PersistentVolumeAccessMode{unknown}, 2, false, true},
		{"mixed mode RWX wins", mixed, 2, false, false},
		{"mixed mode without RWX rejects", []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce, unknown}, 2, false, true},
		{"negative replicas are harmless to gate", []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce}, -1, false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pvc := &corev1.PersistentVolumeClaim{Spec: corev1.PersistentVolumeClaimSpec{AccessModes: tc.modes}}
			if got := validateReplicaStorage(pvc, tc.replicas, tc.readOnly); (got != nil) != tc.wantErr {
				t.Fatalf("error=%v, wantErr=%v", got, tc.wantErr)
			}
		})
	}
}

func TestReplicaStorageGateRejectsBeforeApplyForBothKinds(t *testing.T) {
	tests := []struct {
		name           string
		object         client.Object
		preset         client.Object
		makeReconciler func(client.Client) reconcileRunner
		getCondition   func(client.Object) *metav1.Condition
	}{
		{"VLLMInstance", &vllmv1alpha1.VLLMInstance{ObjectMeta: metav1.ObjectMeta{Name: "vi", Namespace: "ns", Generation: 1}, Spec: vllmv1alpha1.VLLMInstanceSpec{PresetRef: &vllmv1alpha1.PresetReference{Name: "p"}, PVCName: "pvc", HFToken: corev1.SecretKeySelector{LocalObjectReference: corev1.LocalObjectReference{Name: "hf"}, Key: "token"}, Replicas: ptr(int32(2))}}, &vllmv1alpha1.ModelPreset{ObjectMeta: metav1.ObjectMeta{Name: "p", Namespace: "ns"}, Spec: presetSpec()}, func(c client.Client) reconcileRunner { return &VLLMInstanceReconciler{Client: c} }, func(o client.Object) *metav1.Condition {
			instance, ok := o.(*vllmv1alpha1.VLLMInstance)
			if !ok {
				return nil
			}
			return findStorageCondition(instance.Status.Conditions)
		}},
		{"LongContextInstance", &vllmv1alpha1.LongContextInstance{ObjectMeta: metav1.ObjectMeta{Name: "lci", Namespace: "ns", Generation: 1}, Spec: vllmv1alpha1.LongContextInstanceSpec{PresetRef: &vllmv1alpha1.LongContextPresetReference{Name: "p"}, PVCName: "pvc", HFToken: corev1.SecretKeySelector{LocalObjectReference: corev1.LocalObjectReference{Name: "hf"}, Key: "token"}, Replicas: ptr(int32(2))}}, &vllmv1alpha1.LongContextPreset{ObjectMeta: metav1.ObjectMeta{Name: "p", Namespace: "ns"}, Spec: longContextPresetSpec()}, func(c client.Client) reconcileRunner { return &LongContextInstanceReconciler{Client: c} }, func(o client.Object) *metav1.Condition {
			instance, ok := o.(*vllmv1alpha1.LongContextInstance)
			if !ok {
				return nil
			}
			return findStorageCondition(instance.Status.Conditions)
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var applies int
			inter := interceptor.Funcs{Apply: func(ctx context.Context, c client.WithWatch, obj runtime.ApplyConfiguration, opts ...client.ApplyOption) error {
				applies++
				return nil
			}}
			pvc := &corev1.PersistentVolumeClaim{ObjectMeta: metav1.ObjectMeta{Name: "pvc", Namespace: "ns"}, Spec: corev1.PersistentVolumeClaimSpec{AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce}}}
			cl := fake.NewClientBuilder().WithScheme(fullScheme(t)).WithStatusSubresource(tc.object).WithObjects(tc.object, tc.preset, pvc).WithInterceptorFuncs(inter).Build()
			if _, err := tc.makeReconciler(cl).Reconcile(context.Background(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(tc.object)}); err != nil {
				t.Fatalf("Reconcile: %v", err)
			}
			if applies != 0 {
				t.Fatalf("apply calls=%d, want 0", applies)
			}
			var got client.Object
			if tc.name == "VLLMInstance" {
				got = &vllmv1alpha1.VLLMInstance{}
			} else {
				got = &vllmv1alpha1.LongContextInstance{}
			}
			if err := cl.Get(context.Background(), client.ObjectKeyFromObject(tc.object), got); err != nil {
				t.Fatal(err)
			}
			cond := tc.getCondition(got)
			if cond == nil {
				t.Fatal("StorageReady condition not found")
			}
			if cond.Status != metav1.ConditionFalse || cond.Reason != vllmv1alpha1.ReasonReplicaStorageUnsafe {
				t.Fatalf("StorageReady=%+v", cond)
			}
			if cond.Message != "replicas=2 requires PVC access mode ReadWriteMany (or ReadOnlyMany with pvcReadOnly=true); got [ReadWriteOnce]" {
				t.Fatalf("message=%q", cond.Message)
			}
			if err := cl.Get(context.Background(), client.ObjectKey{Namespace: "ns", Name: tc.object.GetName()}, &appsv1.Deployment{}); err == nil {
				t.Fatal("Deployment unexpectedly exists")
			}
		})
	}
}

func findStorageCondition(conditions []metav1.Condition) *metav1.Condition {
	for i := range conditions {
		if conditions[i].Type == vllmv1alpha1.ConditionStorageReady {
			return &conditions[i]
		}
	}
	return nil
}

type reconcileRunner interface {
	Reconcile(context.Context, ctrl.Request) (ctrl.Result, error)
}

func TestReplicaStorageScaleDownRecovery(t *testing.T) {
	for _, kind := range []string{"VLLMInstance", "LongContextInstance"} {
		t.Run(kind, func(t *testing.T) {
			pvc := &corev1.PersistentVolumeClaim{Spec: corev1.PersistentVolumeClaimSpec{AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce}}}
			if err := validateReplicaStorage(pvc, 2, false); err == nil {
				t.Fatal("replicas=2 on RWO must be rejected")
			}
			for _, replicas := range []int32{0, 1} {
				if err := validateReplicaStorage(pvc, replicas, false); err != nil {
					t.Fatalf("replicas=%d after scale-down: %v", replicas, err)
				}
			}
		})
	}
}

func TestReplicaStoragePositiveMountModesForBothKinds(t *testing.T) {
	cfg := presetSpec()
	effective, _, err := vllm.Resolve(&cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"VLLMInstance", "LongContextInstance"} {
		for _, tc := range []struct {
			name         string
			mode         corev1.PersistentVolumeAccessMode
			readOnly     bool
			wantReadOnly bool
		}{
			{"RWX", corev1.ReadWriteMany, false, false},
			{"ROX-read-only", corev1.ReadOnlyMany, true, true},
		} {
			t.Run(kind+"/"+tc.name, func(t *testing.T) {
				pvc := &corev1.PersistentVolumeClaim{Spec: corev1.PersistentVolumeClaimSpec{AccessModes: []corev1.PersistentVolumeAccessMode{tc.mode}}}
				if err := validateReplicaStorage(pvc, 2, tc.readOnly); err != nil {
					t.Fatalf("storage gate: %v", err)
				}
				effective.PVCReadOnly = tc.readOnly
				dep := vllm.BuildDeployment("instance", "ns", 2, effective, "models", corev1.SecretKeySelector{}, nil, metav1.OwnerReference{})
				var got *corev1.VolumeMount
				for _, m := range dep.Spec.Template.Spec.Containers[0].VolumeMounts {
					if m.Name == "models" {
						got = &m
						break
					}
				}
				if got == nil {
					t.Fatal("/models mount missing")
				}
				if got.ReadOnly != tc.wantReadOnly {
					t.Fatalf("/models readOnly=%v, want %v", got.ReadOnly, tc.wantReadOnly)
				}
			})
		}
	}
}

// TestReplicaStorageControllerRejectsEveryUnsafeMode verifies both controllers
// stop before either SSA apply and publish the exact storage-gate diagnosis.
func TestReplicaStorageControllerRejectsEveryUnsafeMode(t *testing.T) {
	cases := []struct {
		name  string
		modes []corev1.PersistentVolumeAccessMode
	}{
		{"RWO", []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce}},
		{"RWO-Pod/unknown", []corev1.PersistentVolumeAccessMode{"ReadWriteOncePod"}},
		{"empty", nil},
		{"ROX-writable", []corev1.PersistentVolumeAccessMode{corev1.ReadOnlyMany}},
	}
	for _, kind := range []string{"VLLMInstance", "LongContextInstance"} {
		for _, tc := range cases {
			t.Run(kind+"/"+tc.name, func(t *testing.T) {
				obj, preset := storageTestObjects(t, kind, ptr(int32(2)), nil, nil)
				pvc := &corev1.PersistentVolumeClaim{ObjectMeta: metav1.ObjectMeta{Name: "pvc", Namespace: "ns"}, Spec: corev1.PersistentVolumeClaimSpec{AccessModes: tc.modes}}
				var applies int
				inter := interceptor.Funcs{Apply: func(ctx context.Context, c client.WithWatch, obj runtime.ApplyConfiguration, opts ...client.ApplyOption) error {
					applies++
					return nil
				}}
				cl := fake.NewClientBuilder().WithScheme(fullScheme(t)).WithStatusSubresource(obj).WithObjects(obj, preset, pvc).WithInterceptorFuncs(inter).Build()
				if _, err := reconcileStorageTestObject(cl, kind, obj); err != nil {
					t.Fatalf("Reconcile: %v", err)
				}
				if applies != 0 {
					t.Fatalf("apply calls=%d, want 0", applies)
				}
				got := newStorageTestObject(kind)
				if err := cl.Get(context.Background(), client.ObjectKeyFromObject(obj), got); err != nil {
					t.Fatal(err)
				}
				wantMsg := fmt.Sprintf("replicas=2 requires PVC access mode ReadWriteMany (or ReadOnlyMany with pvcReadOnly=true); got %v", tc.modes)
				storage, ready := storageAndReady(got)
				for _, cond := range []*metav1.Condition{storage, ready} {
					if cond == nil {
						t.Fatal("expected StorageReady and Ready conditions")
					}
					if cond.Status != metav1.ConditionFalse || cond.Reason != vllmv1alpha1.ReasonReplicaStorageUnsafe || cond.Message != wantMsg {
						t.Fatalf("condition=%+v, want False/%s/%q", cond, vllmv1alpha1.ReasonReplicaStorageUnsafe, wantMsg)
					}
				}
				for _, child := range []client.Object{&appsv1.Deployment{}, &corev1.Service{}} {
					if err := cl.Get(context.Background(), client.ObjectKey{Namespace: "ns", Name: obj.GetName()}, child); err == nil {
						t.Fatalf("%T unexpectedly exists", child)
					}
				}
			})
		}
	}
}

// TestReplicaStorageControllerRendersMountAndHonorsOverride covers the
// controller path for both CR kinds. The spec-level true is overridden by the
// explicit effective-config override false; ROX read-only is checked separately.
func TestReplicaStorageControllerRendersMountAndHonorsOverride(t *testing.T) {
	for _, kind := range []string{"VLLMInstance", "LongContextInstance"} {
		for _, tc := range []struct {
			name                       string
			mode                       corev1.PersistentVolumeAccessMode
			specRO, overrideRO, wantRO bool
		}{
			{"RWX-writable", corev1.ReadWriteMany, true, false, false},
			{"ROX-read-only", corev1.ReadOnlyMany, false, true, true},
		} {
			t.Run(kind+"/"+tc.name, func(t *testing.T) {
				obj, preset := storageTestObjects(t, kind, ptr(int32(2)), ptr(tc.specRO), ptr(tc.overrideRO))
				pvc := &corev1.PersistentVolumeClaim{ObjectMeta: metav1.ObjectMeta{Name: "pvc", Namespace: "ns"}, Spec: corev1.PersistentVolumeClaimSpec{AccessModes: []corev1.PersistentVolumeAccessMode{tc.mode}}}
				var applied runtime.ApplyConfiguration
				var applyCalls int
				inter := interceptor.Funcs{Apply: func(ctx context.Context, c client.WithWatch, obj runtime.ApplyConfiguration, opts ...client.ApplyOption) error {
					applyCalls++
					if applied == nil {
						applied = obj
					}
					return nil
				}}
				cl := fake.NewClientBuilder().WithScheme(fullScheme(t)).WithStatusSubresource(obj).WithObjects(obj, preset, pvc,
					&appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: obj.GetName(), Namespace: "ns"}},
					&corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: "svc-" + obj.GetName(), Namespace: "ns"}, Spec: corev1.ServiceSpec{Ports: []corev1.ServicePort{{NodePort: 32000}}}},
				).WithInterceptorFuncs(inter).Build()
				if _, err := reconcileStorageTestObject(cl, kind, obj); err != nil {
					t.Fatalf("Reconcile: %v", err)
				}
				if applied == nil {
					t.Fatalf("Deployment apply was not captured (apply calls=%d)", applyCalls)
				}
				uProvider, ok := applied.(interface{ UnstructuredContent() map[string]any })
				if !ok {
					t.Fatalf("unexpected apply config type %T", applied)
				}
				u := uProvider.UnstructuredContent()
				mount := findModelsMount(u)
				if mount == nil {
					t.Fatal("/models mount missing from applied Deployment")
				}
				if got, _ := mount["readOnly"].(bool); got != tc.wantRO {
					t.Fatalf("mount readOnly=%v, want %v", got, tc.wantRO)
				}
			})
		}
	}
}

// TestReplicaStorageControllerRecoversAfterScaleDown proves an unsafe RWO
// multi-replica state becomes reconcilable at one replica for both controllers.
func TestReplicaStorageControllerRecoversAfterScaleDown(t *testing.T) {
	for _, kind := range []string{"VLLMInstance", "LongContextInstance"} {
		t.Run(kind, func(t *testing.T) {
			obj, preset := storageTestObjects(t, kind, ptr(int32(2)), nil, nil)
			pvc := &corev1.PersistentVolumeClaim{ObjectMeta: metav1.ObjectMeta{Name: "pvc", Namespace: "ns"}, Spec: corev1.PersistentVolumeClaimSpec{AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce}}}
			var applies int
			inter := interceptor.Funcs{Apply: func(ctx context.Context, c client.WithWatch, obj runtime.ApplyConfiguration, opts ...client.ApplyOption) error {
				applies++
				return nil
			}}
			cl := fake.NewClientBuilder().WithScheme(fullScheme(t)).WithStatusSubresource(obj).WithObjects(obj, preset, pvc,
				&appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: obj.GetName(), Namespace: "ns"}},
				&corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: "svc-" + obj.GetName(), Namespace: "ns"}, Spec: corev1.ServiceSpec{Ports: []corev1.ServicePort{{NodePort: 32000}}}},
			).WithInterceptorFuncs(inter).Build()
			if _, err := reconcileStorageTestObject(cl, kind, obj); err != nil {
				t.Fatal(err)
			}
			if applies != 0 {
				t.Fatalf("unsafe state applied %d resources", applies)
			}
			fresh := newStorageTestObject(kind)
			if err := cl.Get(context.Background(), client.ObjectKeyFromObject(obj), fresh); err != nil {
				t.Fatal(err)
			}
			setReplicas(fresh, 1)
			if err := cl.Update(context.Background(), fresh); err != nil {
				t.Fatal(err)
			}
			if _, err := reconcileStorageTestObject(cl, kind, obj); err != nil {
				t.Fatal(err)
			}
			if applies != 2 {
				t.Fatalf("recovered state apply calls=%d, want Deployment and Service", applies)
			}
		})
	}
}

func newStorageTestObject(kind string) client.Object {
	if kind == "VLLMInstance" {
		return &vllmv1alpha1.VLLMInstance{}
	}
	return &vllmv1alpha1.LongContextInstance{}
}
func storageAndReady(obj client.Object) (*metav1.Condition, *metav1.Condition) {
	if x, ok := obj.(*vllmv1alpha1.VLLMInstance); ok {
		return findStorageCondition(x.Status.Conditions), findCond(x.Status.Conditions, vllmv1alpha1.ConditionReady)
	}
	x, ok := obj.(*vllmv1alpha1.LongContextInstance)
	if !ok {
		panic(fmt.Sprintf("unexpected object type %T", obj))
	}
	return findStorageCondition(x.Status.Conditions), findCond(x.Status.Conditions, vllmv1alpha1.ConditionReady)
}
func findModelsMount(u map[string]interface{}) map[string]interface{} {
	spec, _ := u["spec"].(map[string]interface{})
	tpl, _ := spec["template"].(map[string]interface{})
	ps, _ := tpl["spec"].(map[string]interface{})
	cs, _ := ps["containers"].([]interface{})
	if len(cs) == 0 {
		return nil
	}
	c, _ := cs[0].(map[string]interface{})
	ms, _ := c["volumeMounts"].([]interface{})
	for _, v := range ms {
		m, _ := v.(map[string]interface{})
		if m["name"] == "models" {
			return m
		}
	}
	return nil
}

func storageTestObjects(t *testing.T, kind string, replicas *int32, specRO, overrideRO *bool) (client.Object, client.Object) {
	count := int32(2)
	if replicas != nil {
		count = *replicas
	}
	if kind == "VLLMInstance" {
		o := &vllmv1alpha1.VLLMInstance{ObjectMeta: metav1.ObjectMeta{Name: "vi", Namespace: "ns", Generation: 1}, Spec: vllmv1alpha1.VLLMInstanceSpec{PresetRef: &vllmv1alpha1.PresetReference{Name: "p"}, PVCName: "pvc", Replicas: &count}}
		if specRO != nil {
			o.Spec.PVCReadOnly = specRO
		}
		if overrideRO != nil {
			o.Spec.Overrides = &vllmv1alpha1.ModelConfigOverrides{PVCReadOnly: overrideRO}
		}
		return o, &vllmv1alpha1.ModelPreset{ObjectMeta: metav1.ObjectMeta{Name: "p", Namespace: "ns"}, Spec: presetSpec()}
	}
	o := &vllmv1alpha1.LongContextInstance{ObjectMeta: metav1.ObjectMeta{Name: "lci", Namespace: "ns", Generation: 1}, Spec: vllmv1alpha1.LongContextInstanceSpec{PresetRef: &vllmv1alpha1.LongContextPresetReference{Name: "p"}, PVCName: "pvc", Replicas: &count}}
	if specRO != nil {
		o.Spec.PVCReadOnly = specRO
	}
	if overrideRO != nil {
		o.Spec.Overrides = &vllmv1alpha1.LongContextOverrides{PVCReadOnly: overrideRO}
	}
	return o, &vllmv1alpha1.LongContextPreset{ObjectMeta: metav1.ObjectMeta{Name: "p", Namespace: "ns"}, Spec: longContextPresetSpec()}
}

func reconcileStorageTestObject(cl client.Client, kind string, obj client.Object) (ctrl.Result, error) {
	key := ctrl.Request{NamespacedName: client.ObjectKeyFromObject(obj)}
	if kind == "VLLMInstance" {
		return (&VLLMInstanceReconciler{Client: cl}).Reconcile(context.Background(), key)
	}
	return (&LongContextInstanceReconciler{Client: cl}).Reconcile(context.Background(), key)
}

func setReplicas(obj client.Object, replicas int32) {
	if x, ok := obj.(*vllmv1alpha1.VLLMInstance); ok {
		x.Spec.Replicas = &replicas
		return
	}
	x, ok := obj.(*vllmv1alpha1.LongContextInstance)
	if !ok {
		panic(fmt.Sprintf("unexpected object type %T", obj))
	}
	x.Spec.Replicas = &replicas
}
