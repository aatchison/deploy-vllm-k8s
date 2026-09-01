package controllers

import (
	"context"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
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
			inter := interceptor.Funcs{Patch: func(ctx context.Context, c client.WithWatch, obj client.Object, patch client.Patch, opts ...client.PatchOption) error {
				if patch.Type() == types.ApplyPatchType {
					applies++
					return nil
				}
				return c.Patch(ctx, obj, patch, opts...)
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
