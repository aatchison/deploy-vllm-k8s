package vllm

import (
	"encoding/json"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

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
		EnablePrefixCaching:     boolPtr(true),
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
	if e.EnablePrefixCaching == nil || !*e.EnablePrefixCaching {
		t.Errorf("enablePrefixCaching: got %v, want true", e.EnablePrefixCaching)
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
	if e.EnablePrefixCaching == nil || *e.EnablePrefixCaching {
		t.Errorf("enablePrefixCaching override (false) not applied; got %v", e.EnablePrefixCaching)
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
	// EnablePrefixCaching is now *bool; nil = omitted from JSON = no hash impact.
	if e.EnablePrefixCaching != nil {
		t.Errorf("standard path leaked enablePrefixCaching=%v (want nil)", *e.EnablePrefixCaching)
	}
}

func TestBuildArgsLongContextFlags(t *testing.T) {
	e := EffectiveConfig{
		ModelID:              "m",
		MaxModelLen:          262144,
		GPUMemoryUtilization: "0.92",
		TensorParallelSize:   1,
		KVCacheDtype:         "fp8_e5m2",
		EnablePrefixCaching:  boolPtr(true),
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
		EnablePrefixCaching:  boolPtr(true),
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
		EnablePrefixCaching:  boolPtr(true),
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

func TestBuildArgsMaxNumBatchedTokens(t *testing.T) {
	e := EffectiveConfig{
		ModelID:              "m",
		MaxModelLen:          262144,
		GPUMemoryUtilization: "0.92",
		TensorParallelSize:   1,
		MaxNumBatchedTokens:  4096,
	}
	args := buildArgs(e)
	hasFlag := false
	for i, a := range args {
		if a == "--max-num-batched-tokens" && i+1 < len(args) && args[i+1] == "4096" {
			hasFlag = true
		}
	}
	if !hasFlag {
		t.Errorf("expected --max-num-batched-tokens 4096 in args; got %v", args)
	}
}

func TestBuildArgsMaxNumBatchedTokensOmittedWhenZero(t *testing.T) {
	e := EffectiveConfig{
		ModelID:              "m",
		MaxModelLen:          32768,
		GPUMemoryUtilization: "0.9",
		TensorParallelSize:   1,
		MaxNumBatchedTokens:  0,
	}
	args := buildArgs(e)
	for _, a := range args {
		if a == "--max-num-batched-tokens" {
			t.Errorf("zero-valued MaxNumBatchedTokens leaked into args: %v", args)
		}
	}
}

func TestBuildArgsEnableChunkedPrefill(t *testing.T) {
	e := EffectiveConfig{
		ModelID:              "m",
		MaxModelLen:          262144,
		GPUMemoryUtilization: "0.92",
		TensorParallelSize:   1,
		EnableChunkedPrefill: true,
	}
	args := buildArgs(e)
	hasFlag := false
	for _, a := range args {
		if a == "--enable-chunked-prefill" {
			hasFlag = true
		}
	}
	if !hasFlag {
		t.Errorf("expected --enable-chunked-prefill in args; got %v", args)
	}
}

func TestBuildArgsEnableChunkedPrefillOmittedWhenFalse(t *testing.T) {
	e := EffectiveConfig{
		ModelID:              "m",
		MaxModelLen:          32768,
		GPUMemoryUtilization: "0.9",
		TensorParallelSize:   1,
		EnableChunkedPrefill: false,
	}
	args := buildArgs(e)
	for _, a := range args {
		if a == "--enable-chunked-prefill" {
			t.Errorf("false EnableChunkedPrefill leaked into args: %v", args)
		}
	}
}

// TestResolveMaxNumBatchedTokensOverride verifies pointer override for Resolve (standard path).
func TestResolveMaxNumBatchedTokensOverride(t *testing.T) {
	p := basePreset()
	p.MaxNumBatchedTokens = 2048

	o := &vllmv1alpha1.ModelConfigOverrides{
		MaxNumBatchedTokens: int32Ptr(4096),
	}
	e, _, err := Resolve(p, o)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if e.MaxNumBatchedTokens != 4096 {
		t.Errorf("MaxNumBatchedTokens override not applied: got %d, want 4096", e.MaxNumBatchedTokens)
	}

	// No override: preset value carried through.
	e2, _, err := Resolve(p, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if e2.MaxNumBatchedTokens != 2048 {
		t.Errorf("MaxNumBatchedTokens preset not carried: got %d, want 2048", e2.MaxNumBatchedTokens)
	}
}

// TestResolveEnableChunkedPrefillOverride verifies pointer override for Resolve (standard path).
func TestResolveEnableChunkedPrefillOverride(t *testing.T) {
	p := basePreset()
	p.EnableChunkedPrefill = false

	o := &vllmv1alpha1.ModelConfigOverrides{
		EnableChunkedPrefill: boolPtr(true),
	}
	e, _, err := Resolve(p, o)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !e.EnableChunkedPrefill {
		t.Errorf("EnableChunkedPrefill override not applied: got false, want true")
	}
}

// TestResolveLongContextMaxNumBatchedTokensOverride verifies pointer override for ResolveLongContext.
func TestResolveLongContextMaxNumBatchedTokensOverride(t *testing.T) {
	p := baseLongContextPreset()
	p.MaxNumBatchedTokens = 4096

	// Override replaces preset value.
	o := &vllmv1alpha1.LongContextOverrides{
		MaxNumBatchedTokens: int32Ptr(8192),
	}
	e, _, err := ResolveLongContext(p, o)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if e.MaxNumBatchedTokens != 8192 {
		t.Errorf("MaxNumBatchedTokens override not applied: got %d, want 8192", e.MaxNumBatchedTokens)
	}

	// No override: preset value carried through.
	e2, _, err := ResolveLongContext(p, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if e2.MaxNumBatchedTokens != 4096 {
		t.Errorf("MaxNumBatchedTokens preset not carried: got %d, want 4096", e2.MaxNumBatchedTokens)
	}
}

// TestResolveLongContextEnableChunkedPrefillOverride verifies pointer override for ResolveLongContext.
func TestResolveLongContextEnableChunkedPrefillOverride(t *testing.T) {
	p := baseLongContextPreset()
	p.EnableChunkedPrefill = false

	o := &vllmv1alpha1.LongContextOverrides{
		EnableChunkedPrefill: boolPtr(true),
	}
	e, _, err := ResolveLongContext(p, o)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !e.EnableChunkedPrefill {
		t.Errorf("EnableChunkedPrefill override not applied: got false, want true")
	}

	// No override: false stays false.
	e2, _, err := ResolveLongContext(p, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if e2.EnableChunkedPrefill {
		t.Errorf("EnableChunkedPrefill should remain false when not overridden; got true")
	}
}

// TestResolveStandardHashUnchangedByNewFields verifies that the standard (non-long-context)
// resolve path still serializes with MaxNumBatchedTokens=0 and EnableChunkedPrefill=false
// as omitempty — no hash pollution for existing VLLMInstance objects.
func TestResolveStandardHashUnchangedByNewFields(t *testing.T) {
	p := basePreset()
	e, _, err := Resolve(p, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if e.MaxNumBatchedTokens != 0 {
		t.Errorf("standard path leaked MaxNumBatchedTokens=%d (want 0)", e.MaxNumBatchedTokens)
	}
	if e.EnableChunkedPrefill {
		t.Errorf("standard path leaked EnableChunkedPrefill=true (want false)")
	}
}

func TestResolveLongContextKVOffloadBackend(t *testing.T) {
	p := baseLongContextPreset()
	p.KVOffloadBackend = "lmcache"

	// Preset value carried through when no override.
	e, _, err := ResolveLongContext(p, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if e.KVOffloadBackend != "lmcache" {
		t.Errorf("KVOffloadBackend preset not carried: got %q, want lmcache", e.KVOffloadBackend)
	}

	// Override replaces preset value.
	o := &vllmv1alpha1.LongContextOverrides{
		KVOffloadBackend: strPtr("none"),
	}
	e2, _, err := ResolveLongContext(p, o)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if e2.KVOffloadBackend != "none" {
		t.Errorf("KVOffloadBackend override not applied: got %q, want none", e2.KVOffloadBackend)
	}
}

func TestResolveLongContextKVOffloadSize(t *testing.T) {
	p := baseLongContextPreset()
	p.KVOffloadSize = 64

	// Preset value carried through when no override.
	e, _, err := ResolveLongContext(p, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if e.KVOffloadSize != 64 {
		t.Errorf("KVOffloadSize preset not carried: got %d, want 64", e.KVOffloadSize)
	}

	// Override replaces preset value.
	o := &vllmv1alpha1.LongContextOverrides{
		KVOffloadSize: int32Ptr(128),
	}
	e2, _, err := ResolveLongContext(p, o)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if e2.KVOffloadSize != 128 {
		t.Errorf("KVOffloadSize override not applied: got %d, want 128", e2.KVOffloadSize)
	}
}

// TestResolveStandardHashUnchangedByKVOffloadFields verifies that adding
// KVOffloadBackend / KVOffloadSize does NOT pollute the standard Resolve path.
func TestResolveStandardHashUnchangedByKVOffloadFields(t *testing.T) {
	p := basePreset()
	e, _, err := Resolve(p, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if e.KVOffloadBackend != "" {
		t.Errorf("standard path leaked KVOffloadBackend=%q (want empty)", e.KVOffloadBackend)
	}
	if e.KVOffloadSize != 0 {
		t.Errorf("standard path leaked KVOffloadSize=%d (want 0)", e.KVOffloadSize)
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

// ---------------------------------------------------------------------------
// LMCache sidecar tests (issue #21)
// ---------------------------------------------------------------------------

// TestBuildDeploymentLMCacheSidecarPresent verifies that when KVOffloadBackend
// is "lmcache", BuildDeployment produces a two-container pod with the sidecar
// image, the shared lmcache-data volume, and the volume mounted in both
// containers.
func TestBuildDeploymentLMCacheSidecarPresent(t *testing.T) {
	e := baseEffectiveConfig()
	e.KVOffloadBackend = "lmcache"
	e.KVOffloadSize = 64

	dep := buildTestDeployment(e)
	containers := dep.Spec.Template.Spec.Containers
	if len(containers) != 2 {
		t.Fatalf("expected 2 containers; got %d: %v", len(containers), containerNames(containers))
	}

	// First container is vLLM.
	if containers[0].Name != ContainerName {
		t.Errorf("first container name: got %q, want %q", containers[0].Name, ContainerName)
	}
	// Second container is LMCache sidecar.
	if containers[1].Name != LMCacheContainerName {
		t.Errorf("second container name: got %q, want %q", containers[1].Name, LMCacheContainerName)
	}
	if containers[1].Image != LMCacheImage {
		t.Errorf("sidecar image: got %q, want %q", containers[1].Image, LMCacheImage)
	}

	// lmcache-data volume must exist.
	volumes := dep.Spec.Template.Spec.Volumes
	foundVol := false
	for _, v := range volumes {
		if v.Name == LMCacheDataVolume {
			foundVol = true
			if v.EmptyDir == nil {
				t.Errorf("lmcache-data volume must be emptyDir")
			}
		}
	}
	if !foundVol {
		t.Errorf("lmcache-data volume not found; volumes: %v", volumeNames(volumes))
	}

	// vLLM container mounts lmcache-data.
	foundVLLMMount := false
	for _, m := range containers[0].VolumeMounts {
		if m.Name == LMCacheDataVolume && m.MountPath == LMCacheDataMount {
			foundVLLMMount = true
		}
	}
	if !foundVLLMMount {
		t.Errorf("vLLM container missing lmcache-data mount; mounts: %v", containers[0].VolumeMounts)
	}

	// Sidecar mounts lmcache-data.
	foundSidecarMount := false
	for _, m := range containers[1].VolumeMounts {
		if m.Name == LMCacheDataVolume && m.MountPath == LMCacheDataMount {
			foundSidecarMount = true
		}
	}
	if !foundSidecarMount {
		t.Errorf("sidecar container missing lmcache-data mount; mounts: %v", containers[1].VolumeMounts)
	}

	// Sidecar has liveness probe but NO readiness probe.
	if containers[1].LivenessProbe == nil {
		t.Errorf("sidecar must have a liveness probe")
	}
	if containers[1].ReadinessProbe != nil {
		t.Errorf("sidecar must NOT have a readiness probe (pod readiness must not depend on LMCache); got %+v", containers[1].ReadinessProbe)
	}
}

// TestBuildDeploymentLMCacheSidecarLivenessPort verifies that the LMCache
// sidecar's TCP-socket liveness probe targets LMCacheAdminPort and NOT HTTPPort.
// This is the regression guard for the port aliasing bug where LMCacheAdminPort
// was mistakenly set to 8000 (== HTTPPort), causing the sidecar probe to hit
// vLLM instead of LMCache.
func TestBuildDeploymentLMCacheSidecarLivenessPort(t *testing.T) {
	e := baseEffectiveConfig()
	e.KVOffloadBackend = "lmcache"

	dep := buildTestDeployment(e)
	containers := dep.Spec.Template.Spec.Containers
	if len(containers) < 2 {
		t.Fatalf("expected at least 2 containers; got %d", len(containers))
	}

	sidecar := containers[1]
	if sidecar.LivenessProbe == nil {
		t.Fatal("sidecar must have a liveness probe")
	}
	if sidecar.LivenessProbe.TCPSocket == nil {
		t.Fatal("sidecar liveness probe must be a TCPSocket probe")
	}

	probePort := sidecar.LivenessProbe.TCPSocket.Port.IntValue()
	if probePort == HTTPPort {
		t.Errorf("sidecar liveness probe port collides with HTTPPort (%d); must use LMCacheAdminPort (%d)", HTTPPort, LMCacheAdminPort)
	}
	if probePort != LMCacheAdminPort {
		t.Errorf("sidecar liveness probe port: got %d, want LMCacheAdminPort (%d)", probePort, LMCacheAdminPort)
	}
}

// TestBuildDeploymentNoLMCacheWhenNone verifies that when KVOffloadBackend is
// empty or "none", BuildDeployment produces a single-container pod — no sidecar,
// no lmcache-data volume.
func TestBuildDeploymentNoLMCacheWhenNone(t *testing.T) {
	for _, backend := range []string{"", "none"} {
		t.Run("backend="+backend, func(t *testing.T) {
			e := baseEffectiveConfig()
			e.KVOffloadBackend = backend

			dep := buildTestDeployment(e)
			containers := dep.Spec.Template.Spec.Containers
			if len(containers) != 1 {
				t.Errorf("expected 1 container; got %d: %v", len(containers), containerNames(containers))
			}
			for _, v := range dep.Spec.Template.Spec.Volumes {
				if v.Name == LMCacheDataVolume {
					t.Errorf("unexpected lmcache-data volume when backend=%q", backend)
				}
			}
		})
	}
}

// TestBuildArgsKVTransferConfigEmitted verifies:
//   - flag is present when backend == lmcache
//   - JSON parses correctly
//   - kv_buffer_size is in bytes (GiB * 2^30) when KVOffloadSize > 0
//   - kv_buffer_size is absent when KVOffloadSize == 0
func TestBuildArgsKVTransferConfigEmitted(t *testing.T) {
	t.Run("with_size", func(t *testing.T) {
		e := EffectiveConfig{
			ModelID:              "m",
			MaxModelLen:          262144,
			GPUMemoryUtilization: "0.92",
			TensorParallelSize:   1,
			KVOffloadBackend:     "lmcache",
			KVOffloadSize:        64,
		}
		args := buildArgs(e)
		idx := flagIndex(args, "--kv-transfer-config")
		if idx < 0 {
			t.Fatalf("--kv-transfer-config not emitted; args: %v", args)
		}
		if idx+1 >= len(args) {
			t.Fatalf("--kv-transfer-config has no value; args: %v", args)
		}
		raw := args[idx+1]
		var cfg kvTransferConfig
		if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
			t.Fatalf("--kv-transfer-config value is not valid JSON: %v; raw=%q", err, raw)
		}
		if cfg.KVConnector != "LMCacheConnectorV1" {
			t.Errorf("kv_connector: got %q, want LMCacheConnectorV1", cfg.KVConnector)
		}
		if cfg.KVRole != "kv_both" {
			t.Errorf("kv_role: got %q, want kv_both", cfg.KVRole)
		}
		wantBytes := int64(64) * (1 << 30)
		if cfg.KVBufferSize == nil {
			t.Errorf("kv_buffer_size must be present when KVOffloadSize > 0")
		} else if *cfg.KVBufferSize != wantBytes {
			t.Errorf("kv_buffer_size: got %d, want %d (64 GiB)", *cfg.KVBufferSize, wantBytes)
		}
	})

	t.Run("size_zero_omits_buffer_size", func(t *testing.T) {
		e := EffectiveConfig{
			ModelID:              "m",
			MaxModelLen:          262144,
			GPUMemoryUtilization: "0.92",
			TensorParallelSize:   1,
			KVOffloadBackend:     "lmcache",
			KVOffloadSize:        0,
		}
		args := buildArgs(e)
		idx := flagIndex(args, "--kv-transfer-config")
		if idx < 0 {
			t.Fatalf("--kv-transfer-config not emitted; args: %v", args)
		}
		raw := args[idx+1]
		var cfg kvTransferConfig
		if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
			t.Fatalf("--kv-transfer-config value is not valid JSON: %v; raw=%q", err, raw)
		}
		if cfg.KVBufferSize != nil {
			t.Errorf("kv_buffer_size must be absent when KVOffloadSize == 0; got %d", *cfg.KVBufferSize)
		}
	})
}

// TestBuildArgsKVTransferConfigAbsentWhenNoLMCache verifies that
// --kv-transfer-config is NOT emitted when backend is empty or "none".
func TestBuildArgsKVTransferConfigAbsentWhenNoLMCache(t *testing.T) {
	for _, backend := range []string{"", "none"} {
		t.Run("backend="+backend, func(t *testing.T) {
			e := EffectiveConfig{
				ModelID:              "m",
				MaxModelLen:          32768,
				GPUMemoryUtilization: "0.9",
				TensorParallelSize:   1,
				KVOffloadBackend:     backend,
			}
			args := buildArgs(e)
			if flagIndex(args, "--kv-transfer-config") >= 0 {
				t.Errorf("--kv-transfer-config must not be emitted when backend=%q; args: %v", backend, args)
			}
		})
	}
}

// TestBuildDeploymentLMCacheSidecarViaOverride verifies the override path:
// a LongContextInstance with kvOffloadBackend: lmcache in its overrides
// produces a sidecar even when the preset has KVOffloadBackend == "".
func TestBuildDeploymentLMCacheSidecarViaOverride(t *testing.T) {
	p := baseLongContextPreset()
	// Preset has no LMCache backend.
	p.KVOffloadBackend = ""

	o := &vllmv1alpha1.LongContextOverrides{
		KVOffloadBackend: strPtr("lmcache"),
		KVOffloadSize:    int32Ptr(32),
	}
	e, _, err := ResolveLongContext(p, o)
	if err != nil {
		t.Fatalf("ResolveLongContext error: %v", err)
	}
	if e.KVOffloadBackend != "lmcache" {
		t.Fatalf("override did not set KVOffloadBackend; got %q", e.KVOffloadBackend)
	}

	dep := buildTestDeployment(e)
	containers := dep.Spec.Template.Spec.Containers
	if len(containers) != 2 {
		t.Fatalf("expected 2 containers after override; got %d: %v", len(containers), containerNames(containers))
	}
	if containers[1].Name != LMCacheContainerName {
		t.Errorf("second container: got %q, want %q", containers[1].Name, LMCacheContainerName)
	}
}

// ---------------------------------------------------------------------------
// helpers used only by the LMCache tests
// ---------------------------------------------------------------------------

// baseEffectiveConfig returns a minimal EffectiveConfig suitable for
// constructing test Deployments.
func baseEffectiveConfig() EffectiveConfig {
	return EffectiveConfig{
		ModelID:              "test/model",
		Image:                "docker.io/library/vllm-test:local",
		ImagePullPolicy:      "Never",
		MIGResource:          "nvidia.com/mig-2g.48gb",
		MIGResourceCount:     1,
		MaxModelLen:          32768,
		GPUMemoryUtilization: "0.90",
		TensorParallelSize:   1,
		SHMSizeLimit:         "8Gi",
		ProgressDeadlineSeconds: 600,
		LivenessProbe:  vllmv1alpha1.ProbeConfig{InitialDelaySeconds: 60, PeriodSeconds: 30, FailureThreshold: 10},
		ReadinessProbe: vllmv1alpha1.ProbeConfig{InitialDelaySeconds: 30, PeriodSeconds: 10, FailureThreshold: 6},
	}
}

// buildTestDeployment is a thin wrapper around BuildDeployment that supplies
// the boilerplate name/namespace/ownerRef values needed by tests.
func buildTestDeployment(e EffectiveConfig) *appsv1.Deployment {
	return BuildDeployment(
		"test-instance",
		"default",
		1,
		e,
		"test-pvc",
		corev1.SecretKeySelector{
			LocalObjectReference: corev1.LocalObjectReference{Name: "hf-secret"},
			Key:                  "token",
		},
		metav1.OwnerReference{},
	)
}

func containerNames(cs []corev1.Container) []string {
	names := make([]string, len(cs))
	for i, c := range cs {
		names[i] = c.Name
	}
	return names
}

func volumeNames(vs []corev1.Volume) []string {
	names := make([]string, len(vs))
	for i, v := range vs {
		names[i] = v.Name
	}
	return names
}

// flagIndex returns the index of flag in args, or -1 if not present.
func flagIndex(args []string, flag string) int {
	for i, a := range args {
		if a == flag {
			return i
		}
	}
	return -1
}

// ---------------------------------------------------------------------------

// TestEnablePrefixCachingNilVsFalseVsTrue is the regression guard for the
// *bool fix (issue #5). It verifies the three distinct states:
//   - nil preset value with no override -> no --enable-prefix-caching flag
//   - explicit false override -> no flag
//   - explicit true (preset or override) -> flag emitted
func TestEnablePrefixCachingNilVsFalseVsTrue(t *testing.T) {
	p := baseLongContextPreset()

	// 1. nil preset value + no override -> no flag
	p.EnablePrefixCaching = nil
	e, _, err := ResolveLongContext(p, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if e.EnablePrefixCaching != nil {
		t.Errorf("nil preset: expected EnablePrefixCaching==nil in EffectiveConfig; got %v", *e.EnablePrefixCaching)
	}
	for _, a := range buildArgs(e) {
		if a == "--enable-prefix-caching" {
			t.Errorf("nil preset: --enable-prefix-caching must not be emitted; got args %v", buildArgs(e))
		}
	}

	// 2. explicit false override -> no flag
	p.EnablePrefixCaching = boolPtr(true)
	e2, _, err := ResolveLongContext(p, &vllmv1alpha1.LongContextOverrides{
		EnablePrefixCaching: boolPtr(false),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if e2.EnablePrefixCaching == nil || *e2.EnablePrefixCaching {
		t.Errorf("false override: expected EnablePrefixCaching==false; got %v", e2.EnablePrefixCaching)
	}
	for _, a := range buildArgs(e2) {
		if a == "--enable-prefix-caching" {
			t.Errorf("false override: --enable-prefix-caching must not be emitted; got args %v", buildArgs(e2))
		}
	}

	// 3. true preset + no override -> flag emitted
	p.EnablePrefixCaching = boolPtr(true)
	e3, _, err := ResolveLongContext(p, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if e3.EnablePrefixCaching == nil || !*e3.EnablePrefixCaching {
		t.Errorf("true preset: expected EnablePrefixCaching==true; got %v", e3.EnablePrefixCaching)
	}
	hasFlag := false
	for _, a := range buildArgs(e3) {
		if a == "--enable-prefix-caching" {
			hasFlag = true
		}
	}
	if !hasFlag {
		t.Errorf("true preset: --enable-prefix-caching must be emitted; got args %v", buildArgs(e3))
	}
}
