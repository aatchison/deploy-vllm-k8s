package vllm

import (
	"encoding/json"
	"os"
	"reflect"
	"testing"

	vllmv1alpha1 "github.com/aatchison/deploy-vllm-k8s/operator/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestEnvBaseline(t *testing.T) {
	e, _, err := ResolveLongContext(baseLongContextPreset(), nil)
	if err != nil {
		t.Fatal(err)
	}
	dep := BuildDeployment("env", "test", 1, e, "models", corev1.SecretKeySelector{}, nil, metav1.OwnerReference{})
	got, err := json.Marshal(dep.Spec.Template.Spec.Containers[0].Env)
	if err != nil {
		t.Fatal(err)
	}
	// Captured by execution on forgejo/main c42f97aebd9074871934922c8189f2568ce8e2dd.
	want, err := os.ReadFile("testdata/env-before.json")
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(want) {
		t.Fatalf("env changed: %s != %s", got, want)
	}
}

func TestEnvPassthrough(t *testing.T) {
	kernel := corev1.EnvVar{Name: "VLLM_DISABLED_KERNELS", Value: "FlashInferFP8ScaledMMLinearKernel"}
	ref := corev1.EnvVar{Name: "REF", ValueFrom: &corev1.EnvVarSource{ConfigMapKeyRef: &corev1.ConfigMapKeySelector{LocalObjectReference: corev1.LocalObjectReference{Name: "config"}, Key: "key"}}}
	for _, tc := range []struct {
		name                   string
		preset, instance, want []corev1.EnvVar
	}{
		{name: "neither"},
		{name: "preset", preset: []corev1.EnvVar{kernel}, want: []corev1.EnvVar{kernel}},
		{name: "instance only", instance: []corev1.EnvVar{kernel}, want: []corev1.EnvVar{kernel}},
		{name: "override and stable order", preset: []corev1.EnvVar{{Name: "A", Value: "old"}, kernel}, instance: []corev1.EnvVar{ref, {Name: "A", Value: "new"}}, want: []corev1.EnvVar{{Name: "A", Value: "new"}, kernel, ref}},
		{name: "valueFrom replaced", preset: []corev1.EnvVar{ref}, instance: []corev1.EnvVar{{Name: "REF", Value: "literal"}}, want: []corev1.EnvVar{{Name: "REF", Value: "literal"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := baseLongContextPreset()
			p.Env = tc.preset
			spec := vllmv1alpha1.LongContextInstanceSpec{Env: tc.instance}
			for i := 0; i < 20; i++ {
				e, hash, err := ResolveLongContextInstance(p, spec)
				if err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(e.Env, tc.want) {
					t.Fatalf("got %#v want %#v", e.Env, tc.want)
				}
				wantHash, err := HashConfig(e)
				if err != nil || hash != wantHash {
					t.Fatal("hash does not cover resolved env")
				}
				dep := BuildDeployment("env", "test", 1, e, "models", corev1.SecretKeySelector{}, nil, metav1.OwnerReference{})
				baseline, err := os.ReadFile("testdata/env-before.json")
				if err != nil {
					t.Fatal(err)
				}
				var want []corev1.EnvVar
				if err := json.Unmarshal(baseline, &want); err != nil {
					t.Fatal(err)
				}
				want = append(want, tc.want...)
				if !reflect.DeepEqual(dep.Spec.Template.Spec.Containers[0].Env, want) {
					t.Fatalf("container env: %#v", dep.Spec.Template.Spec.Containers[0].Env)
				}
			}
		})
	}
}

func TestEnvNoAliasingAndHash(t *testing.T) {
	p := baseLongContextPreset()
	p.Env = []corev1.EnvVar{{Name: "REF", ValueFrom: &corev1.EnvVarSource{FieldRef: &corev1.ObjectFieldSelector{FieldPath: "metadata.name"}}}}
	e, hash, err := ResolveLongContextInstance(p, vllmv1alpha1.LongContextInstanceSpec{})
	if err != nil {
		t.Fatal(err)
	}
	p.Env[0].ValueFrom.FieldRef.FieldPath = "metadata.namespace"
	if e.Env[0].ValueFrom.FieldRef.FieldPath != "metadata.name" {
		t.Fatal("preset env aliased")
	}
	_, changed, err := ResolveLongContextInstance(p, vllmv1alpha1.LongContextInstanceSpec{})
	if err != nil || changed == hash {
		t.Fatal("env change must change hash")
	}
	dep := BuildDeployment("env", "test", 1, e, "models", corev1.SecretKeySelector{}, nil, metav1.OwnerReference{})
	dep.Spec.Template.Spec.Containers[0].Env[5].ValueFrom.FieldRef.FieldPath = "metadata.uid"
	if e.Env[0].ValueFrom.FieldRef.FieldPath != "metadata.name" {
		t.Fatal("rendered env aliased")
	}
}

func TestEnvDefaultsAndStandalone(t *testing.T) {
	spec := vllmv1alpha1.LongContextInstanceSpec{
		Env:       []corev1.EnvVar{{Name: "HOME", Value: "/custom"}},
		Overrides: &vllmv1alpha1.LongContextOverrides{SHMSizeLimit: strPtr("1Gi")},
	}
	e, _, err := ResolveLongContextInstance(nil, spec)
	if err != nil {
		t.Fatal(err)
	}
	dep := BuildDeployment("env", "test", 1, e, "models", corev1.SecretKeySelector{}, nil, metav1.OwnerReference{})
	env := dep.Spec.Template.Spec.Containers[0].Env
	if len(env) != 5 || env[4].Name != "HOME" || env[4].Value != "/custom" {
		t.Fatalf("default override: %#v", env)
	}
	p := baseLongContextPreset()
	_, before, err := ResolveLongContext(p, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, after, err := ResolveLongContextInstance(p, vllmv1alpha1.LongContextInstanceSpec{Env: []corev1.EnvVar{}})
	if err != nil || before != after {
		t.Fatal("empty env changed hash")
	}
}

func TestEnvDeepCopy(t *testing.T) {
	env := []corev1.EnvVar{
		{Name: "SECRET", ValueFrom: &corev1.EnvVarSource{SecretKeyRef: &corev1.SecretKeySelector{LocalObjectReference: corev1.LocalObjectReference{Name: "secret"}, Key: "key"}}},
		{Name: "RESOURCE", ValueFrom: &corev1.EnvVarSource{ResourceFieldRef: &corev1.ResourceFieldSelector{Resource: "limits.cpu"}}},
	}
	p := &vllmv1alpha1.LongContextPreset{Spec: vllmv1alpha1.LongContextPresetSpec{Env: env}}
	i := &vllmv1alpha1.LongContextInstance{Spec: vllmv1alpha1.LongContextInstanceSpec{Env: env}}
	pc, ic := p.DeepCopy(), i.DeepCopy()
	merged := MergeEnv(env)
	env[0].ValueFrom.SecretKeyRef.Key = "changed"
	env[1].ValueFrom.ResourceFieldRef.Resource = "limits.memory"
	for _, got := range [][]corev1.EnvVar{pc.Spec.Env, ic.Spec.Env, merged} {
		if got[0].ValueFrom.SecretKeyRef.Key != "key" || got[1].ValueFrom.ResourceFieldRef.Resource != "limits.cpu" {
			t.Fatal("env selectors aliased")
		}
	}
}
