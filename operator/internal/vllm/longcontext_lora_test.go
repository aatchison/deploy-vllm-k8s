package vllm

import (
	v1alpha1 "github.com/aatchison/deploy-vllm-k8s/operator/api/v1alpha1"
	"strings"
	"testing"
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
