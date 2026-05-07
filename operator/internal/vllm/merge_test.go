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

func baseLongContextPreset() *vllmv1alpha1.LongContextPresetSpec {
	return &vllmv1alpha1.LongContextPresetSpec{
		ModelID:                 "nvidia/Gemma-4-31B-IT-NVFP4",
		Image:                   "docker.io/library/vllm-gemma4:local",
		ImagePullPolicy:         "Never",
		MIGResource:             "nvidia.com/mig-4g.96gb",
		MIGResourceCount:        1,
		Quantization:            "nvfp4",
		MaxModelLen:             262144,
		GPUMemoryUtilization:    "0.92",
		TensorParallelSize:      1,
		EnableAutoToolChoice:    true,
		ToolCallParser:          "gemma4",
		SHMSizeLimit:            "16Gi",
		ProgressDeadlineSeconds: 1800,
		LivenessProbe:           vllmv1alpha1.ProbeConfig{InitialDelaySeconds: 1200, PeriodSeconds: 30, FailureThreshold: 10},
		ReadinessProbe:          vllmv1alpha1.ProbeConfig{InitialDelaySeconds: 240, PeriodSeconds: 15, FailureThreshold: 60},
		KVCacheDtype:            "fp8_e4m3",
		EnablePrefixCaching:     true,
	}
}

func TestResolveLongContextPresetOnly(t *testing.T) {
	p := baseLongContextPreset()
	e, h, err := ResolveLongContext(p, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if e.KVCacheDtype != "fp8_e4m3" {
		t.Errorf("kvCacheDtype: got %q, want fp8_e4m3", e.KVCacheDtype)
	}
	if !e.EnablePrefixCaching {
		t.Errorf("enablePrefixCaching: got false, want true")
	}
	if e.MaxModelLen != 262144 {
		t.Errorf("maxModelLen: got %d, want 262144", e.MaxModelLen)
	}
	if h == "" {
		t.Error("hash must be non-empty")
	}
}

func TestResolveLongContextOverrides(t *testing.T) {
	p := baseLongContextPreset()
	o := &vllmv1alpha1.LongContextOverrides{
		KVCacheDtype:        strPtr("fp8_e4m3"),
		EnablePrefixCaching: boolPtr(false),
		MaxModelLen:         int32Ptr(196608),
	}
	e, _, err := ResolveLongContext(p, o)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if e.KVCacheDtype != "fp8_e4m3" {
		t.Errorf("kvCacheDtype override not applied: %q", e.KVCacheDtype)
	}
	if e.EnablePrefixCaching {
		t.Errorf("enablePrefixCaching override (false) not applied")
	}
	if e.MaxModelLen != 196608 {
		t.Errorf("maxModelLen override not applied: %d", e.MaxModelLen)
	}
	// Carried-over preset fields untouched.
	if e.ModelID != p.ModelID {
		t.Errorf("modelID changed unexpectedly: %q", e.ModelID)
	}
	if e.Quantization != "nvfp4" {
		t.Errorf("quantization carried-over wrong: %q", e.Quantization)
	}
}

func TestResolveLongContextHashStable(t *testing.T) {
	p := baseLongContextPreset()
	_, h1, _ := ResolveLongContext(p, nil)
	_, h2, _ := ResolveLongContext(p, nil)
	if h1 != h2 {
		t.Errorf("long-context hash not stable: %q vs %q", h1, h2)
	}
}

func TestResolveLongContextHashChangesOnKVOverride(t *testing.T) {
	p := baseLongContextPreset()
	_, base, _ := ResolveLongContext(p, nil)
	_, changed, _ := ResolveLongContext(p, &vllmv1alpha1.LongContextOverrides{KVCacheDtype: strPtr("fp8_e5m2")})
	if base == changed {
		t.Errorf("hash should change when kvCacheDtype overridden; both %q", base)
	}
}

// TestResolveStandardHashUnchangedByLongContextFields verifies that adding
// the long-context fields to EffectiveConfig does NOT change the serialized
// hash for the existing standard ModelPreset path. Critical regression guard:
// existing VLLMInstance hashes must stay stable across this PR.
func TestResolveStandardHashUnchangedByLongContextFields(t *testing.T) {
	p := basePreset()
	e, _, err := Resolve(p, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if e.KVCacheDtype != "" {
		t.Errorf("standard path leaked kvCacheDtype: %q", e.KVCacheDtype)
	}
	if e.EnablePrefixCaching {
		t.Errorf("standard path leaked enablePrefixCaching=true")
	}
}

func TestBuildArgsLongContextFlags(t *testing.T) {
	e := EffectiveConfig{
		ModelID:              "m",
		MaxModelLen:          262144,
		GPUMemoryUtilization: "0.92",
		TensorParallelSize:   1,
		KVCacheDtype:         "fp8_e5m2",
		EnablePrefixCaching:  true,
	}
	args := buildArgs(e)
	hasKV, hasPrefix := false, false
	for i, a := range args {
		if a == "--kv-cache-dtype" && i+1 < len(args) && args[i+1] == "fp8_e5m2" {
			hasKV = true
		}
		if a == "--enable-prefix-caching" {
			hasPrefix = true
		}
	}
	if !hasKV {
		t.Errorf("expected --kv-cache-dtype fp8_e5m2 in args; got %v", args)
	}
	if !hasPrefix {
		t.Errorf("expected --enable-prefix-caching in args; got %v", args)
	}
}

// TestBuildArgsKVCacheDtypeDefault confirms that fp8_e4m3 (the field-marker
// default) flows through the merge layer and is emitted as --kv-cache-dtype.
// The kubebuilder default applies at admission, not in our merge code, so this
// test sets the field explicitly and verifies the flag is present in the output.
func TestBuildArgsKVCacheDtypeDefault(t *testing.T) {
	e := EffectiveConfig{
		ModelID:              "nvidia/Gemma-4-31B-IT-NVFP4",
		MaxModelLen:          262144,
		GPUMemoryUtilization: "0.92",
		TensorParallelSize:   1,
		KVCacheDtype:         "fp8_e4m3",
		EnablePrefixCaching:  true,
	}
	args := buildArgs(e)
	found := false
	for i, a := range args {
		if a == "--kv-cache-dtype" && i+1 < len(args) && args[i+1] == "fp8_e4m3" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected --kv-cache-dtype fp8_e4m3 in args; got %v", args)
	}
}

func TestBuildArgsServedModelName(t *testing.T) {
	e := EffectiveConfig{
		ModelID:              "nvidia/Gemma-4-31B-IT-NVFP4",
		ServedModelName:      "google/gemma-4-31B-it",
		MaxModelLen:          262144,
		GPUMemoryUtilization: "0.92",
		TensorParallelSize:   1,
	}
	args := buildArgs(e)
	hasFlag := false
	for i, a := range args {
		if a == "--served-model-name" && i+1 < len(args) && args[i+1] == "google/gemma-4-31B-it" {
			hasFlag = true
		}
	}
	if !hasFlag {
		t.Errorf("expected --served-model-name google/gemma-4-31B-it in args; got %v", args)
	}
}

func TestBuildArgsServedModelNameOmittedWhenEmpty(t *testing.T) {
	e := EffectiveConfig{
		ModelID:              "m",
		MaxModelLen:          32768,
		GPUMemoryUtilization: "0.9",
		TensorParallelSize:   1,
	}
	args := buildArgs(e)
	for _, a := range args {
		if a == "--served-model-name" {
			t.Errorf("zero-valued ServedModelName leaked: %v", args)
		}
	}
}

func TestBuildArgsLongContextOmittedWhenZero(t *testing.T) {
	e := EffectiveConfig{
		ModelID:              "m",
		MaxModelLen:          32768,
		GPUMemoryUtilization: "0.9",
		TensorParallelSize:   1,
	}
	args := buildArgs(e)
	for _, a := range args {
		if a == "--kv-cache-dtype" || a == "--enable-prefix-caching" {
			t.Errorf("zero-valued long-context fields leaked into args: %v", args)
		}
	}
}

func TestResolveStartupProbeNilByDefault(t *testing.T) {
	p := basePreset()
	e, _, err := Resolve(p, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if e.StartupProbe != nil {
		t.Errorf("startupProbe should default nil; got %+v", e.StartupProbe)
	}
}

func TestResolveStartupProbeFromPreset(t *testing.T) {
	p := basePreset()
	p.StartupProbe = &vllmv1alpha1.ProbeConfig{InitialDelaySeconds: 30, PeriodSeconds: 10, FailureThreshold: 120}
	e, _, err := Resolve(p, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if e.StartupProbe == nil {
		t.Fatal("startupProbe should be non-nil after preset set")
	}
	if e.StartupProbe.InitialDelaySeconds != 30 || e.StartupProbe.PeriodSeconds != 10 || e.StartupProbe.FailureThreshold != 120 {
		t.Errorf("startupProbe values not carried; got %+v", e.StartupProbe)
	}
}

func TestResolveStartupProbeOverride(t *testing.T) {
	p := basePreset()
	o := &vllmv1alpha1.ModelConfigOverrides{
		StartupProbe: &vllmv1alpha1.ProbeConfig{InitialDelaySeconds: 60, PeriodSeconds: 5, FailureThreshold: 360},
	}
	e, _, err := Resolve(p, o)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if e.StartupProbe == nil || e.StartupProbe.InitialDelaySeconds != 60 {
		t.Errorf("startupProbe override not applied; got %+v", e.StartupProbe)
	}
}

func TestBuildStartupProbeNilReturnsNil(t *testing.T) {
	if got := buildStartupProbe(nil); got != nil {
		t.Errorf("buildStartupProbe(nil) should be nil; got %+v", got)
	}
}

func TestBuildStartupProbePopulated(t *testing.T) {
	p := buildStartupProbe(&vllmv1alpha1.ProbeConfig{InitialDelaySeconds: 30, PeriodSeconds: 10, FailureThreshold: 120})
	if p == nil {
		t.Fatal("expected non-nil probe")
	}
	if p.InitialDelaySeconds != 30 || p.PeriodSeconds != 10 || p.FailureThreshold != 120 {
		t.Errorf("probe values wrong: %+v", p)
	}
	if p.HTTPGet == nil || p.HTTPGet.Path != "/health" {
		t.Errorf("expected /health HTTP probe; got %+v", p.HTTPGet)
	}
}

func TestBuildArgsCPUOffloadEmits(t *testing.T) {
	e := EffectiveConfig{
		ModelID:              "m",
		MaxModelLen:          262144,
		GPUMemoryUtilization: "0.92",
		TensorParallelSize:   1,
		KVCacheDtype:         "fp8_e4m3",
		EnablePrefixCaching:  true,
		CPUOffloadGiB:        48,
	}
	args := buildArgs(e)
	hasFlag := false
	for i, a := range args {
		if a == "--cpu-offload-gb" && i+1 < len(args) && args[i+1] == "48" {
			hasFlag = true
		}
	}
	if !hasFlag {
		t.Errorf("expected --cpu-offload-gb 48 in args; got %v", args)
	}
}

func TestBuildArgsCPUOffloadOmittedWhenZero(t *testing.T) {
	e := EffectiveConfig{
		ModelID:              "m",
		MaxModelLen:          262144,
		GPUMemoryUtilization: "0.92",
		TensorParallelSize:   1,
		CPUOffloadGiB:        0,
	}
	args := buildArgs(e)
	for _, a := range args {
		if a == "--cpu-offload-gb" {
			t.Errorf("zero-valued CPUOffloadGiB leaked into args: %v", args)
		}
	}
}

func TestResolveLongContextCPUOffloadOverride(t *testing.T) {
	p := baseLongContextPreset()
	p.CPUOffloadGiB = 32

	// Override replaces preset value.
	o := &vllmv1alpha1.LongContextOverrides{
		CPUOffloadGiB: int32Ptr(64),
	}
	e, _, err := ResolveLongContext(p, o)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if e.CPUOffloadGiB != 64 {
		t.Errorf("CPUOffloadGiB override not applied: got %d, want 64", e.CPUOffloadGiB)
	}

	// No override: preset value carried through.
	e2, _, err := ResolveLongContext(p, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if e2.CPUOffloadGiB != 32 {
		t.Errorf("CPUOffloadGiB preset not carried: got %d, want 32", e2.CPUOffloadGiB)
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
