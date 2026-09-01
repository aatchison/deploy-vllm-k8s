package vllm

import (
	"testing"

	corev1 "k8s.io/api/core/v1"

	vllmv1alpha1 "github.com/aatchison/deploy-vllm-k8s/operator/api/v1alpha1"
)

// TestBuildDeploymentEnableLora asserts that enableLora=true generates --enable-lora flag.
func TestBuildDeploymentEnableLora(t *testing.T) {
	preset := &vllmv1alpha1.ModelPresetSpec{
		ModelID:      "test/model",
		EnableLora:   true,
		MaxLoraRank:  16,
		LoraModules:  "fleetv1=/models/adapters/test/run-20240101T000000Z-pid123/lora_weights",
		MIGResource:  "nvidia.com/mig-2g.48gb",
		MIGResourceCount:  1,
		MaxModelLen:   32768,
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
		vllmv1alpha1.OwnerReference{
			APIVersion: "v1",
			Kind:       "Pod",
			Name:       "test",
			UID:        1,
		},
	)

	containers := dep.Spec.Template.Spec.Containers
	if len(containers) == 0 {
		t.Fatal("expected at least one container")
	}
	args := containers[0].Args

	// Check for exact lora flags
	hasEnableLora := false
	hasMaxLoraRank := false
	hasLoraModules := false
	for _, arg := range args {
		if arg == "--enable-lora" {
			hasEnableLora = true
		}
		if arg == "--max-lora-rank" {
			hasMaxLoraRank = true
		}
		if arg == "--lora-modules" {
			hasLoraModules = true
		}
	}

	t.Logf("Container args: %v", args)
	t.Logf("has --enable-lora: %v", hasEnableLora)
	t.Logf("has --max-lora-rank: %v", hasMaxLoraRank)
	t.Logf("has --lora-modules: %v", hasLoraModules)

	if !hasEnableLora {
		t.Error("missing --enable-lora in container args")
	}
	if !hasMaxLoraRank {
		t.Error("missing --max-lora-rank in container args")
	}
	if !hasLoraModules {
		t.Error("missing --lora-modules in container args")
	}
}

// TestBuildDeploymentEnableLoraFalse asserts that enableLora=false omits the flag.
func TestBuildDeploymentEnableLoraFalse(t *testing.T) {
	preset := &vllmv1alpha1.ModelPresetSpec{
		ModelID:      "test/model",
		EnableLora:   false,
		MaxLoraRank:  16,
		LoraModules:  "fleetv1=/models/adapters/test/run-20240101T000000Z-pid123/lora_weights",
		MIGResource:  "nvidia.com/mig-2g.48gb",
		MIGResourceCount:  1,
		MaxModelLen:   32768,
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
		vllmv1alpha1.OwnerReference{
			APIVersion: "v1",
			Kind:       "Pod",
			Name:       "test",
			UID:        1,
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
		ModelID:      "test/model",
		EnableLora:   true,
		MaxLoraRank:  32,
		LoraModules:  "fleetv1=/models/adapters/test/run-20240101T000000Z-pid123/lora_weights",
		MIGResource:  "nvidia.com/mig-2g.48gb",
		MIGResourceCount:  1,
		MaxModelLen:   32768,
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
		vllmv1alpha1.OwnerReference{
			APIVersion: "v1",
			Kind:       "Pod",
			Name:       "test",
			UID:        1,
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
		ModelID:      "test/model",
		EnableLora:   true,
		MaxLoraRank:  16,
		LoraModules:  "my_model=/models/adapters/test/lora.safetensors",
		MIGResource:  "nvidia.com/mig-2g.48gb",
		MIGResourceCount:  1,
		MaxModelLen:   32768,
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
		vllmv1alpha1.OwnerReference{
			APIVersion: "v1",
			Kind:       "Pod",
			Name:       "test",
			UID:        1,
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
}
