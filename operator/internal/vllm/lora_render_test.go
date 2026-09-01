package vllm

import (
	"reflect"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	types "k8s.io/apimachinery/pkg/types"

	vllmv1alpha1 "github.com/aatchison/deploy-vllm-k8s/operator/api/v1alpha1"
)

// TestBuildDeploymentEnableLora asserts that enableLora=true generates --enable-lora flag.
func TestBuildDeploymentEnableLora(t *testing.T) {
	preset := &vllmv1alpha1.ModelPresetSpec{
		ModelID:              "test/model",
		EnableLora:           true,
		MaxLoraRank:          16,
		LoraModules:          "fleetv1=/models/adapters/test/run-20240101T000000Z-pid123/lora_weights",
		MIGResource:          "nvidia.com/mig-2g.48gb",
		MIGResourceCount:     1,
		MaxModelLen:          32768,
		GPUMemoryUtilization: "0.90",
		TensorParallelSize:   1,
		SHMSizeLimit:         "8Gi",
	}

	e, _, err := Resolve(preset, nil)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	if !e.EnableLora {
		t.Errorf("EffectiveConfig.EnableLora: got false, want true")
	}
	if e.MaxLoraRank != 16 {
		t.Errorf("EffectiveConfig.MaxLoraRank: got %d, want 16", e.MaxLoraRank)
	}
	if e.LoraModules != "fleetv1=/models/adapters/test/run-20240101T000000Z-pid123/lora_weights" {
		t.Errorf("EffectiveConfig.LoraModules: got %s, want %s",
			e.LoraModules, "fleetv1=/models/adapters/test/run-20240101T000000Z-pid123/lora_weights")
	}

	dep := BuildDeployment(
		"test-instance",
		"default",
		1,
		e,
		"test-pvc",
		corev1.SecretKeySelector{
			LocalObjectReference: corev1.LocalObjectReference{Name: "test-hf-token"},
		},
		nil,
		metav1.OwnerReference{
			APIVersion: "vllm.aatchison.io/v1alpha1",
			Kind:       "VLLMInstance",
			Name:       "test-instance",
			UID:        types.UID("1"),
		},
	)

	containers := dep.Spec.Template.Spec.Containers
	if len(containers) == 0 {
		t.Fatal("expected at least one container")
	}
	args := containers[0].Args

	wantArgs := []string{
		"--model", "test/model",
		"--port", "8000",
		"--max-model-len", "32768",
		"--gpu-memory-utilization", "0.90",
		"--enable-lora",
		"--max-lora-rank", "16",
		"--lora-modules", "fleetv1=/models/adapters/test/run-20240101T000000Z-pid123/lora_weights",
	}
	if !reflect.DeepEqual(args, wantArgs) {
		t.Fatalf("container args = %#v, want exact vector %#v", args, wantArgs)
	}

}

// TestBuildDeploymentEnableLoraFalse asserts that enableLora=false omits the flag.
func TestBuildDeploymentEnableLoraFalse(t *testing.T) {
	preset := &vllmv1alpha1.ModelPresetSpec{
		ModelID:              "test/model",
		EnableLora:           false,
		MaxLoraRank:          16,
		LoraModules:          "fleetv1=/models/adapters/test/run-20240101T000000Z-pid123/lora_weights",
		MIGResource:          "nvidia.com/mig-2g.48gb",
		MIGResourceCount:     1,
		MaxModelLen:          32768,
		GPUMemoryUtilization: "0.90",
		TensorParallelSize:   1,
		SHMSizeLimit:         "8Gi",
	}

	e, _, err := Resolve(preset, nil)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	if e.EnableLora {
		t.Errorf("EffectiveConfig.EnableLora: got true, want false when EnableLora=false")
	}

	dep := BuildDeployment(
		"test-instance",
		"default",
		1,
		e,
		"test-pvc",
		corev1.SecretKeySelector{
			LocalObjectReference: corev1.LocalObjectReference{Name: "test-hf-token"},
		},
		nil,
		metav1.OwnerReference{
			APIVersion: "vllm.aatchison.io/v1alpha1",
			Kind:       "VLLMInstance",
			Name:       "test-instance",
			UID:        types.UID("1"),
		},
	)

	containers := dep.Spec.Template.Spec.Containers
	if len(containers) == 0 {
		t.Fatal("expected at least one container")
	}
	args := containers[0].Args

	hasEnableLora := false
	for _, arg := range args {
		if arg == "--enable-lora" {
			hasEnableLora = true
		}
	}

	if hasEnableLora {
		t.Error("--enable-lora should not be present when EnableLora=false")
	}
}

// TestBuildDeploymentMaxLoraRank asserts that maxLoraRank generates --max-lora-rank flag.
func TestBuildDeploymentMaxLoraRank(t *testing.T) {
	preset := &vllmv1alpha1.ModelPresetSpec{
		ModelID:              "test/model",
		EnableLora:           true,
		MaxLoraRank:          32,
		LoraModules:          "fleetv1=/models/adapters/test/run-20240101T000000Z-pid123/lora_weights",
		MIGResource:          "nvidia.com/mig-2g.48gb",
		MIGResourceCount:     1,
		MaxModelLen:          32768,
		GPUMemoryUtilization: "0.90",
		TensorParallelSize:   1,
		SHMSizeLimit:         "8Gi",
	}

	e, _, err := Resolve(preset, nil)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	if e.MaxLoraRank != 32 {
		t.Errorf("EffectiveConfig.MaxLoraRank: got %d, want 32", e.MaxLoraRank)
	}

	dep := BuildDeployment(
		"test-instance",
		"default",
		1,
		e,
		"test-pvc",
		corev1.SecretKeySelector{
			LocalObjectReference: corev1.LocalObjectReference{Name: "test-hf-token"},
		},
		nil,
		metav1.OwnerReference{
			APIVersion: "vllm.aatchison.io/v1alpha1",
			Kind:       "VLLMInstance",
			Name:       "test-instance",
			UID:        types.UID("1"),
		},
	)

	containers := dep.Spec.Template.Spec.Containers
	if len(containers) == 0 {
		t.Fatal("expected at least one container")
	}
	args := containers[0].Args

	hasMaxLoraRank := false
	for _, arg := range args {
		if arg == "--max-lora-rank" {
			hasMaxLoraRank = true
		}
	}

	if !hasMaxLoraRank {
		t.Error("missing --max-lora-rank in container args")
	}
}

// TestBuildDeploymentLoraModules asserts that loraModules generates --lora-modules flag.
func TestBuildDeploymentLoraModules(t *testing.T) {
	preset := &vllmv1alpha1.ModelPresetSpec{
		ModelID:              "test/model",
		EnableLora:           true,
		MaxLoraRank:          16,
		LoraModules:          "my_model=/models/adapters/test/lora.safetensors",
		MIGResource:          "nvidia.com/mig-2g.48gb",
		MIGResourceCount:     1,
		MaxModelLen:          32768,
		GPUMemoryUtilization: "0.90",
		TensorParallelSize:   1,
		SHMSizeLimit:         "8Gi",
	}

	e, _, err := Resolve(preset, nil)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	if e.LoraModules != "my_model=/models/adapters/test/lora.safetensors" {
		t.Errorf("EffectiveConfig.LoraModules: got %s, want %s",
			e.LoraModules, "my_model=/models/adapters/test/lora.safetensors")
	}

	dep := BuildDeployment(
		"test-instance",
		"default",
		1,
		e,
		"test-pvc",
		corev1.SecretKeySelector{
			LocalObjectReference: corev1.LocalObjectReference{Name: "test-hf-token"},
		},
		nil,
		metav1.OwnerReference{
			APIVersion: "vllm.aatchison.io/v1alpha1",
			Kind:       "VLLMInstance",
			Name:       "test-instance",
			UID:        types.UID("1"),
		},
	)

	containers := dep.Spec.Template.Spec.Containers
	if len(containers) == 0 {
		t.Fatal("expected at least one container")
	}
	args := containers[0].Args

	hasLoraModules := false
	for _, arg := range args {
		if arg == "--lora-modules" {
			hasLoraModules = true
		}
	}

	if !hasLoraModules {
		t.Error("missing --lora-modules in container args")
	}
	want := "my_model=/models/adapters/test/lora.safetensors"
	for i, arg := range args {
		if arg == "--lora-modules" {
			if i+1 >= len(args) {
				t.Fatal("--lora-modules has no value")
			}
			if args[i+1] != want {
				t.Fatalf("--lora-modules value: got %q, want %q", args[i+1], want)
			}
			return
		}
	}
	t.Fatal("--lora-modules flag not found")
}

// TestValidateLoraModulesTraversal asserts that path traversal outside /models/ is rejected.
func TestValidateLoraModulesTraversal(t *testing.T) {
	tests := []struct {
		input     string
		wantValid bool
	}{
		{"fleetv1=/models/../../etc/passwd", false},
		{"fleetv1=/models/../etc/passwd", false},
		{"fleetv1=../../../etc/passwd", false},
		{"name=../../etc", false},
		{"name=/abs/outside", false},
		{"fleetv1=/tmp/secrets", false},
		{"fleetv1=/etc/shadow", false},
	}

	for _, tt := range tests {
		valid, errMsg := validateLoraModules(tt.input)
		if valid != tt.wantValid {
			t.Errorf("validateLoraModules(%q): got valid=%v, want %v, errMsg=%s", tt.input, valid, tt.wantValid, errMsg)
		}
	}
}

// TestValidateLoraModulesRelativePath asserts that relative paths are rejected.
func TestValidateLoraModulesRelativePath(t *testing.T) {
	tests := []struct {
		input     string
		wantValid bool
	}{
		{"my_model=adapters/test.lora", false}, // relative path (no leading /)
		{"model=lora.safetensors", false},      // relative path
		{"name=../other", false},               // traversal via ..
	}

	for _, tt := range tests {
		valid, errMsg := validateLoraModules(tt.input)
		if valid != tt.wantValid {
			t.Errorf("validateLoraModules(%q): got valid=%v, want %v, errMsg=%s", tt.input, valid, tt.wantValid, errMsg)
		}
	}
}

// TestValidateLoraModulesMissingAssert asserts that entries without '=' are rejected.
func TestValidateLoraModulesMissingAssert(t *testing.T) {
	tests := []struct {
		input     string
		wantValid bool
	}{
		{"fleetv1", false}, // missing '='
		{"name", false},    // missing '='
		{"", true},         // empty string means no modules
		{"name=", false},   // empty path
		{"=path", false},   // empty name
	}

	for _, tt := range tests {
		valid, errMsg := validateLoraModules(tt.input)
		if valid != tt.wantValid {
			t.Errorf("validateLoraModules(%q): got valid=%v, want %v, errMsg=%s", tt.input, valid, tt.wantValid, errMsg)
		}
	}
}

// TestValidateLoraModulesEmptyEntry asserts that empty entries are rejected.
func TestValidateLoraModulesEmptyEntry(t *testing.T) {
	tests := []string{
		"fleetv1=/models/a/,",             // trailing empty entry
		",name=path",                      // leading empty entry
		"name=/models/a,,name2=/models/b", // middle empty entry
	}

	for _, tt := range tests {
		valid, errMsg := validateLoraModules(tt)
		if valid {
			t.Errorf("validateLoraModules(%q): got valid=true, want false", tt)
		} else {
			t.Logf("validateLoraModules(%q): correctly rejected, errMsg=%s", tt, errMsg)
		}
	}
}

// TestValidateLoraModulesValidCase asserts that valid name=path entries under /models/ are accepted.
func TestValidateLoraModulesValidCase(t *testing.T) {
	tests := []struct {
		input     string
		wantValid bool
	}{
		{"fleetv1=/models/adapters/test/run-20240101T000000Z-pid123/lora_weights", true},
		{"my_model=/models/adapters/test/lora.safetensors", true},
		{"adapter1=/models/adapter1/weights.safetensors,adapter2=/models/adapter2/weights.safetensors", true},
	}

	for _, tt := range tests {
		valid, errMsg := validateLoraModules(tt.input)
		if valid != tt.wantValid {
			t.Errorf("validateLoraModules(%q): got valid=%v, want %v, errMsg=%s", tt.input, valid, tt.wantValid, errMsg)
		}
	}
}

// TestBuildDeploymentLoraModulesValidation asserts that invalid loraModules does NOT render --lora-modules flag.
func TestBuildDeploymentLoraModulesValidation(t *testing.T) {
	// Test 1: traversal path should omit --lora-modules
	preset := &vllmv1alpha1.ModelPresetSpec{
		ModelID:              "test/model",
		EnableLora:           true,
		MaxLoraRank:          16,
		LoraModules:          "fleetv1=/models/../../etc/passwd",
		MIGResource:          "nvidia.com/mig-2g.48gb",
		MIGResourceCount:     1,
		MaxModelLen:          32768,
		GPUMemoryUtilization: "0.90",
		TensorParallelSize:   1,
		SHMSizeLimit:         "8Gi",
	}

	e, _, err := Resolve(preset, nil)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	dep := BuildDeployment(
		"test-instance",
		"default",
		1,
		e,
		"test-pvc",
		corev1.SecretKeySelector{
			LocalObjectReference: corev1.LocalObjectReference{Name: "test-hf-token"},
		},
		nil,
		metav1.OwnerReference{
			APIVersion: "vllm.aatchison.io/v1alpha1",
			Kind:       "VLLMInstance",
			Name:       "test-instance",
			UID:        types.UID("1"),
		},
	)

	containers := dep.Spec.Template.Spec.Containers
	if len(containers) == 0 {
		t.Fatal("expected at least one container")
	}
	args := containers[0].Args

	hasLoraModules := false
	for _, arg := range args {
		if arg == "--lora-modules" {
			hasLoraModules = true
		}
	}

	if hasLoraModules {
		t.Error("invalid loraModules '/models/../../etc/passwd' should not render --lora-modules flag")
	}

	// Test 2: relative path should omit --lora-modules
	preset2 := &vllmv1alpha1.ModelPresetSpec{
		ModelID:              "test/model",
		EnableLora:           true,
		MaxLoraRank:          16,
		LoraModules:          "my_model=relative/path/lora.safetensors",
		MIGResource:          "nvidia.com/mig-2g.48gb",
		MIGResourceCount:     1,
		MaxModelLen:          32768,
		GPUMemoryUtilization: "0.90",
		TensorParallelSize:   1,
		SHMSizeLimit:         "8Gi",
	}

	e2, _, err := Resolve(preset2, nil)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	dep2 := BuildDeployment(
		"test-instance",
		"default",
		1,
		e2,
		"test-pvc",
		corev1.SecretKeySelector{
			LocalObjectReference: corev1.LocalObjectReference{Name: "test-hf-token"},
		},
		nil,
		metav1.OwnerReference{
			APIVersion: "vllm.aatchison.io/v1alpha1",
			Kind:       "VLLMInstance",
			Name:       "test-instance",
			UID:        types.UID("1"),
		},
	)

	containers2 := dep2.Spec.Template.Spec.Containers
	if len(containers2) == 0 {
		t.Fatal("expected at least one container")
	}
	args2 := containers2[0].Args

	hasLoraModules2 := false
	for _, arg := range args2 {
		if arg == "--lora-modules" {
			hasLoraModules2 = true
		}
	}

	if hasLoraModules2 {
		t.Error("relative path loraModules should not render --lora-modules flag")
	}

	// Test 3: valid loraModules should render --lora-modules flag
	preset3 := &vllmv1alpha1.ModelPresetSpec{
		ModelID:              "test/model",
		EnableLora:           true,
		MaxLoraRank:          16,
		LoraModules:          "fleetv1=/models/adapters/test/lora_weights",
		MIGResource:          "nvidia.com/mig-2g.48gb",
		MIGResourceCount:     1,
		MaxModelLen:          32768,
		GPUMemoryUtilization: "0.90",
		TensorParallelSize:   1,
		SHMSizeLimit:         "8Gi",
	}

	e3, _, err := Resolve(preset3, nil)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	dep3 := BuildDeployment(
		"test-instance",
		"default",
		1,
		e3,
		"test-pvc",
		corev1.SecretKeySelector{
			LocalObjectReference: corev1.LocalObjectReference{Name: "test-hf-token"},
		},
		nil,
		metav1.OwnerReference{
			APIVersion: "vllm.aatchison.io/v1alpha1",
			Kind:       "VLLMInstance",
			Name:       "test-instance",
			UID:        types.UID("1"),
		},
	)

	containers3 := dep3.Spec.Template.Spec.Containers
	if len(containers3) == 0 {
		t.Fatal("expected at least one container")
	}
	args3 := containers3[0].Args

	hasLoraModules3 := false
	for _, arg := range args3 {
		if arg == "--lora-modules" {
			hasLoraModules3 = true
		}
	}

	if !hasLoraModules3 {
		t.Error("valid loraModules should render --lora-modules flag")
	}
}
