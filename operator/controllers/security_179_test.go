package controllers

import (
	"context"
	"encoding/json"
	"errors"
	v1alpha1 "github.com/aatchison/deploy-vllm-k8s/operator/api/v1alpha1"
	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"strings"
	"testing"

	"github.com/aatchison/deploy-vllm-k8s/operator/internal/vllm"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
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

func TestSecurity179AllowlistParsingAndMatching(t *testing.T) {
	for _, tc := range []struct {
		raw   string
		valid bool
		size  int
	}{
		{"", true, 0}, {"   ", true, 0}, {" ns, other,ns ", true, 2},
		{"*", false, 0}, {"ns,", false, 0}, {",ns", false, 0}, {"ns,,other", false, 0}, {"Bad", false, 0}, {"ns/*", false, 0},
	} {
		got, err := ParseEngramIPCNamespaces(tc.raw)
		if (err == nil) != tc.valid || (tc.valid && len(got) != tc.size) {
			t.Fatalf("raw=%q map=%v err=%v", tc.raw, got, err)
		}
	}
	allowed, err := ParseEngramIPCNamespaces("ns")
	if err != nil {
		t.Fatal(err)
	}
	r := &LongContextInstanceReconciler{EngramIPCNamespaces: allowed}
	e, _, err := vllm.ResolveLongContext(&v1alpha1.LongContextPresetSpec{
		ModelID: "m", MIGResource: "nvidia.com/mig-4g.96gb", SHMSizeLimit: "8Gi", SecurityProfile: v1alpha1.SecurityProfileEngramIPC, EngramConfig: `{"cpu_offload":true}`,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, ns := range []string{"ns", "ns-other", "other", ""} {
		err := r.validateSecurityPolicy179(e, ns)
		if (err == nil) != (ns == "ns") {
			t.Fatalf("namespace=%q err=%v", ns, err)
		}
	}
	e.SecurityProfile = v1alpha1.SecurityProfileDefault
	if err := (&LongContextInstanceReconciler{}).validateSecurityPolicy179(e, "other"); err != nil {
		t.Fatalf("default must not need allowlist: %v", err)
	}
}

func TestSecurity179AllowedReconcileProducesRestrictedPayload(t *testing.T) {
	scheme := fullScheme(t)
	var inst v1alpha1.LongContextInstance
	if err := json.Unmarshal([]byte(`{"metadata":{"name":"engram","namespace":"ns","generation":7},"spec":{"pvcName":"models","hfToken":{"name":"hf","key":"token"},"overrides":{"modelID":"m","migResource":"nvidia.com/mig-4g.96gb","maxModelLen":1024,"kvCacheDtype":"auto","shmSizeLimit":"8Gi","securityProfile":"engram-ipc","engramConfig":"{\"cpu_offload\":true}"}}}`), &inst); err != nil {
		t.Fatal(err)
	}
	pvc := &corev1.PersistentVolumeClaim{ObjectMeta: metav1.ObjectMeta{Name: "models", Namespace: "ns"}}
	sentinel := errors.New("captured deployment apply")
	var payload appsv1.Deployment
	count := 0
	cl := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(&v1alpha1.LongContextInstance{}).WithObjects(&inst, pvc).WithInterceptorFuncs(interceptor.Funcs{
		Apply: func(_ context.Context, _ client.WithWatch, ac runtime.ApplyConfiguration, _ ...client.ApplyOption) error {
			count++
			data, err := json.Marshal(ac)
			if err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(data, &payload); err != nil {
				t.Fatal(err)
			}
			return sentinel
		},
	}).Build()
	r := &LongContextInstanceReconciler{Client: cl, Scheme: scheme, Recorder: record.NewFakeRecorder(8), EngramIPCNamespaces: map[string]struct{}{"ns": {}}}
	_, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(&inst)})
	if !errors.Is(err, sentinel) || count != 1 {
		t.Fatalf("did not reach allowed apply: calls=%d err=%v", count, err)
	}
	psc := payload.Spec.Template.Spec.SecurityContext
	if psc == nil || psc.RunAsUser == nil || *psc.RunAsUser != 1000 || psc.RunAsNonRoot == nil || !*psc.RunAsNonRoot || psc.SeccompProfile == nil || psc.SeccompProfile.Type != corev1.SeccompProfileTypeRuntimeDefault {
		t.Fatalf("pod security=%+v", psc)
	}
	if len(payload.Spec.Template.Spec.Containers) != 1 {
		t.Fatalf("unexpected container count")
	}
	c := payload.Spec.Template.Spec.Containers[0]
	sc := c.SecurityContext
	if sc == nil || sc.AllowPrivilegeEscalation == nil || *sc.AllowPrivilegeEscalation || sc.Capabilities == nil || len(sc.Capabilities.Add) != 0 || len(sc.Capabilities.Drop) != 1 || sc.Capabilities.Drop[0] != "ALL" {
		t.Fatalf("container security=%+v", sc)
	}
	found := false
	for i, arg := range c.Args {
		if arg == "--engram-config" && i+1 < len(c.Args) && c.Args[i+1] == `{"cpu_offload":true}` {
			found = true
		}
	}
	if !found {
		t.Fatalf("missing typed config: %v", c.Args)
	}
}
