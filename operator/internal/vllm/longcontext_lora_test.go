package vllm

import (
	v1alpha1 "github.com/aatchison/deploy-vllm-k8s/operator/api/v1alpha1"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestResolveLongContextLoraAndRenderArgs(t *testing.T) {
	enabled := true
	modules := "adapter=/models/adapters/lora"
	rank := int32(16)
	e, _, err := ResolveLongContext(&v1alpha1.LongContextPresetSpec{ModelID: "m", LoraModules: modules, EnableLora: true, MaxLoraRank: rank}, &v1alpha1.LongContextOverrides{EnableLora: &enabled, LoraModules: &modules, MaxLoraRank: &rank})
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateEffectiveConfig(e); err != nil {
		t.Fatal(err)
	}
	args := buildArgs(e)
	got := strings.Join(args, " ")
	want := "--enable-lora --max-lora-rank 16 --lora-modules adapter=/models/adapters/lora"
	if !strings.Contains(got, want) {
		t.Fatalf("args %q missing exact composed LoRA args %q", got, want)
	}
}

func TestResolveLongContextRejectsInvalidLoraModules(t *testing.T) {
	e, _, err := ResolveLongContext(&v1alpha1.LongContextPresetSpec{ModelID: "m", LoraModules: "adapter=/models/../../etc/passwd"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateEffectiveConfig(e); err == nil || !strings.Contains(err.Error(), "invalid loraModules") {
		t.Fatalf("got %v", err)
	}
	for _, a := range buildArgs(e) {
		if a == "--lora-modules" {
			t.Fatal("invalid config rendered --lora-modules")
		}
	}
}

func TestLoraModulesMalformedFixtures(t *testing.T) {
	for _, input := range []string{"", "adapter", "adapter=", "= /models/a", "adapter=relative", "adapter=/models/foo/../bar", "adapter=/etc/a", "adapter=/models/a,,b=/models/b"} {
		e, _, err := ResolveLongContext(&v1alpha1.LongContextPresetSpec{ModelID: "m", LoraModules: input}, nil)
		if err != nil {
			t.Fatal(err)
		}
		if input == "" {
			if err := ValidateEffectiveConfig(e); err != nil {
				t.Fatal(err)
			}
			continue
		}
		if err := ValidateEffectiveConfig(e); err == nil {
			t.Fatalf("%q accepted", input)
		}
		// Invalid modules are rejected before controllers apply; direct rendering
		// preserves the historical deployment shape while omitting the flag.
		e.SHMSizeLimit = "8Gi"
		dep := BuildDeployment("x", "ns", 1, e, "pvc", corev1.SecretKeySelector{}, nil, metav1.OwnerReference{})
		if dep == nil {
			t.Fatalf("%q unexpectedly returned nil deployment", input)
		}
		for _, arg := range dep.Spec.Template.Spec.Containers[0].Args {
			if arg == "--lora-modules" {
				t.Fatalf("%q rendered --lora-modules", input)
			}
		}
	}
}

func TestLongContextLoraOverridePrecedence(t *testing.T) {
	presetModules := "preset=/models/preset"
	overrideModules := "override=/models/override"
	e, _, err := ResolveLongContext(&v1alpha1.LongContextPresetSpec{ModelID: "m", LoraModules: presetModules, MaxLoraRank: 8}, &v1alpha1.LongContextOverrides{LoraModules: &overrideModules})
	if err != nil {
		t.Fatal(err)
	}
	if e.LoraModules != overrideModules || e.MaxLoraRank != 8 {
		t.Fatalf("override precedence: %+v", e)
	}
}
