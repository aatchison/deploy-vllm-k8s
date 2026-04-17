package vllm

import (
	"testing"

	vllmv1alpha1 "github.com/aatchison/deploy-vllm-k8s/operator/api/v1alpha1"
)

func strPtr(s string) *string { return &s }
func int32Ptr(i int32) *int32 { return &i }
func boolPtr(b bool) *bool    { return &b }

func basePreset() *vllmv1alpha1.ModelPresetSpec {
	return &vllmv1alpha1.ModelPresetSpec{
		ModelID:                 "google/gemma-4-E2B-it",
		Image:                   "docker.io/library/vllm-gemma4:local",
		ImagePullPolicy:         "Never",
		MIGResource:             "nvidia.com/mig-2g.48gb",
		MIGResourceCount:        1,
		MaxModelLen:             32768,
		GPUMemoryUtilization:    "0.90",
		TensorParallelSize:      1,
		EnableAutoToolChoice:    true,
		ToolCallParser:          "gemma4",
		SHMSizeLimit:            "8Gi",
		ProgressDeadlineSeconds: 600,
		LivenessProbe:           vllmv1alpha1.ProbeConfig{InitialDelaySeconds: 300, PeriodSeconds: 30, FailureThreshold: 10},
		ReadinessProbe:          vllmv1alpha1.ProbeConfig{InitialDelaySeconds: 60, PeriodSeconds: 10, FailureThreshold: 30},
	}
}

func TestResolvePresetOnly(t *testing.T) {
	p := basePreset()
	e, h, err := Resolve(p, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if e.ModelID != p.ModelID {
		t.Errorf("modelID: got %q, want %q", e.ModelID, p.ModelID)
	}
	if e.MaxModelLen != 32768 {
		t.Errorf("maxModelLen: got %d, want 32768", e.MaxModelLen)
	}
	if h == "" {
		t.Error("hash must be non-empty")
	}
}

func TestResolveOverridesReplace(t *testing.T) {
	p := basePreset()
	o := &vllmv1alpha1.ModelConfigOverrides{
		MaxModelLen:          int32Ptr(65536),
		GPUMemoryUtilization: strPtr("0.95"),
	}
	e, _, err := Resolve(p, o)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if e.MaxModelLen != 65536 {
		t.Errorf("maxModelLen override not applied: %d", e.MaxModelLen)
	}
	if e.GPUMemoryUtilization != "0.95" {
		t.Errorf("gpuMemoryUtilization override not applied: %q", e.GPUMemoryUtilization)
	}
	// Preset fields untouched by overrides should remain.
	if e.ModelID != p.ModelID {
		t.Errorf("modelID changed unexpectedly: %q", e.ModelID)
	}
}

func TestResolveDefaultsWhenOverridesOnly(t *testing.T) {
	o := &vllmv1alpha1.ModelConfigOverrides{
		ModelID:              strPtr("custom/model"),
		MIGResource:          strPtr("nvidia.com/mig-4g.96gb"),
		MIGResourceCount:     int32Ptr(1),
		MaxModelLen:          int32Ptr(16384),
		GPUMemoryUtilization: strPtr("0.80"),
		SHMSizeLimit:         strPtr("4Gi"),
		ProgressDeadlineSeconds: int32Ptr(300),
		TensorParallelSize:   int32Ptr(1),
		LivenessProbe:        &vllmv1alpha1.ProbeConfig{InitialDelaySeconds: 30, PeriodSeconds: 30, FailureThreshold: 10},
		ReadinessProbe:       &vllmv1alpha1.ProbeConfig{InitialDelaySeconds: 10, PeriodSeconds: 5, FailureThreshold: 6},
		EnableAutoToolChoice: boolPtr(false),
	}
	e, _, err := Resolve(nil, o)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if e.Image != DefaultImage {
		t.Errorf("image default not applied: %q", e.Image)
	}
	if e.ImagePullPolicy != DefaultImagePullPolicy {
		t.Errorf("imagePullPolicy default not applied: %q", e.ImagePullPolicy)
	}
}

func TestResolveHashStable(t *testing.T) {
	p := basePreset()
	_, h1, _ := Resolve(p, nil)
	_, h2, _ := Resolve(p, nil)
	if h1 != h2 {
		t.Errorf("hash not stable: %q vs %q", h1, h2)
	}
}

func TestResolveHashChangesOnOverride(t *testing.T) {
	p := basePreset()
	_, base, _ := Resolve(p, nil)
	_, changed, _ := Resolve(p, &vllmv1alpha1.ModelConfigOverrides{MaxModelLen: int32Ptr(65536)})
	if base == changed {
		t.Errorf("hash should change when overrides applied; both %q", base)
	}
}

func TestBuildArgsOrdering(t *testing.T) {
	e := EffectiveConfig{
		ModelID:              "m",
		DType:                "bfloat16",
		MaxModelLen:          32768,
		GPUMemoryUtilization: "0.9",
		TensorParallelSize:   2,
		EnableAutoToolChoice: true,
		ToolCallParser:       "gemma4",
	}
	args := buildArgs(e)
	expect := []string{
		"--model", "m",
		"--dtype", "bfloat16",
		"--port", "8000",
		"--max-model-len", "32768",
		"--gpu-memory-utilization", "0.9",
		"--tensor-parallel-size", "2",
		"--enable-auto-tool-choice",
		"--tool-call-parser", "gemma4",
	}
	if len(args) != len(expect) {
		t.Fatalf("arg count mismatch: got %v want %v", args, expect)
	}
	for i := range expect {
		if args[i] != expect[i] {
			t.Errorf("arg[%d]: got %q want %q", i, args[i], expect[i])
		}
	}
}

func TestBuildArgsOmitOptional(t *testing.T) {
	e := EffectiveConfig{
		ModelID:              "m",
		MaxModelLen:          8192,
		GPUMemoryUtilization: "0.9",
		TensorParallelSize:   1,
	}
	args := buildArgs(e)
	for _, a := range args {
		if a == "--dtype" || a == "--quantization" || a == "--tensor-parallel-size" ||
			a == "--enable-auto-tool-choice" || a == "--tool-call-parser" {
			t.Errorf("unexpected optional arg emitted: %q (all args: %v)", a, args)
		}
	}
}

func TestSanitizeLabel(t *testing.T) {
	cases := map[string]string{
		"google/gemma-4-E2B-it":  "google-gemma-4-E2B-it",
		"nvidia/Gemma-4-31B-IT":  "nvidia-Gemma-4-31B-IT",
		"some@weird!value":       "some-weird-value",
	}
	for in, want := range cases {
		if got := SanitizeLabel(in); got != want {
			t.Errorf("SanitizeLabel(%q) = %q, want %q", in, got, want)
		}
	}
}
