package vllm

import (
	"testing"

	corev1 "k8s.io/api/core/v1"

	vllmv1alpha1 "github.com/aatchison/deploy-vllm-k8s/operator/api/v1alpha1"
)

// findModelsMount returns the /models VolumeMount from the vLLM container, or
// nil if not present.
func findModelsMount(c corev1.Container) *corev1.VolumeMount {
	for i := range c.VolumeMounts {
		m := &c.VolumeMounts[i]
		if m.Name == "models" && m.MountPath == "/models" {
			return m
		}
	}
	return nil
}

// TestBuildDeploymentPVCReadOnlyDefault is the regression guard for issue #76:
// the default PVCReadOnly behavior (false / unset) must keep the /models mount
// writable so existing single-tenant deployments continue to populate the HF
// weight cache. Any change that flips the default to true breaks every
// existing single-tenant cluster on next reconcile.
func TestBuildDeploymentPVCReadOnlyDefault(t *testing.T) {
	dep := buildTestDeployment(baseEffectiveConfig())
	containers := dep.Spec.Template.Spec.Containers
	if len(containers) == 0 {
		t.Fatal("expected at least one container")
	}
	mount := findModelsMount(containers[0])
	if mount == nil {
		t.Fatal("/models mount not found on vLLM container")
	}
	if mount.ReadOnly {
		t.Errorf("/models mount.ReadOnly: got true, want false (default must preserve write-cache behavior for existing deployments)")
	}
}

// TestBuildDeploymentPVCReadOnlyOptIn is the headline guard for issue #76: a
// VLLMInstance with PVCReadOnly=true must produce a /models VolumeMount with
// ReadOnly=true. This is the cross-tenant model-poisoning defense — tenant B
// must not be able to mutate weights that tenant A loads from a shared cache.
func TestBuildDeploymentPVCReadOnlyOptIn(t *testing.T) {
	e := baseEffectiveConfig()
	e.PVCReadOnly = true

	dep := buildTestDeployment(e)
	containers := dep.Spec.Template.Spec.Containers
	if len(containers) == 0 {
		t.Fatal("expected at least one container")
	}
	mount := findModelsMount(containers[0])
	if mount == nil {
		t.Fatal("/models mount not found on vLLM container")
	}
	if !mount.ReadOnly {
		t.Errorf("/models mount.ReadOnly: got false, want true when EffectiveConfig.PVCReadOnly=true")
	}
}

// TestResolvePVCReadOnlyOverride asserts that ModelConfigOverrides.PVCReadOnly
// flows through Resolve into EffectiveConfig.PVCReadOnly. This is the path the
// vllminstance_controller uses when overrides are present.
func TestResolvePVCReadOnlyOverride(t *testing.T) {
	preset := &vllmv1alpha1.ModelPresetSpec{
		ModelID:              "test/model",
		MIGResource:          "nvidia.com/mig-2g.48gb",
		MIGResourceCount:     1,
		MaxModelLen:          32768,
		GPUMemoryUtilization: "0.90",
		TensorParallelSize:   1,
		SHMSizeLimit:         "8Gi",
	}

	t.Run("override_true_flips_on", func(t *testing.T) {
		ro := true
		e, _, err := Resolve(preset, &vllmv1alpha1.ModelConfigOverrides{PVCReadOnly: &ro})
		if err != nil {
			t.Fatalf("Resolve: %v", err)
		}
		if !e.PVCReadOnly {
			t.Errorf("EffectiveConfig.PVCReadOnly: got false, want true when override is true")
		}
	})

	t.Run("override_nil_keeps_default_false", func(t *testing.T) {
		e, _, err := Resolve(preset, nil)
		if err != nil {
			t.Fatalf("Resolve: %v", err)
		}
		if e.PVCReadOnly {
			t.Errorf("EffectiveConfig.PVCReadOnly: got true, want false when override is nil")
		}
	})
}

// TestResolveLongContextPVCReadOnlyOverride asserts the parallel path through
// ResolveLongContext for LongContextInstance.
func TestResolveLongContextPVCReadOnlyOverride(t *testing.T) {
	preset := &vllmv1alpha1.LongContextPresetSpec{
		ModelID:              "test/model",
		MIGResource:          "nvidia.com/mig-4g.96gb",
		MIGResourceCount:     1,
		MaxModelLen:          131072,
		GPUMemoryUtilization: "0.90",
		TensorParallelSize:   1,
		SHMSizeLimit:         "8Gi",
		KVCacheDtype:         "fp8_e4m3",
	}

	ro := true
	e, _, err := ResolveLongContext(preset, &vllmv1alpha1.LongContextOverrides{PVCReadOnly: &ro})
	if err != nil {
		t.Fatalf("ResolveLongContext: %v", err)
	}
	if !e.PVCReadOnly {
		t.Errorf("EffectiveConfig.PVCReadOnly via LongContextOverrides: got false, want true")
	}
}

// TestPVCReadOnlyHashStability verifies omitempty on the JSON tag — adding the
// field must not change the resolved-config hash for instances that don't opt
// in (PVCReadOnly stays false). Otherwise every existing instance would see a
// hash change on operator upgrade and trigger a no-op rollout.
func TestPVCReadOnlyHashStability(t *testing.T) {
	preset := &vllmv1alpha1.ModelPresetSpec{
		ModelID:              "test/model",
		MIGResource:          "nvidia.com/mig-2g.48gb",
		MIGResourceCount:     1,
		MaxModelLen:          32768,
		GPUMemoryUtilization: "0.90",
		TensorParallelSize:   1,
		SHMSizeLimit:         "8Gi",
	}
	_, hashWithout, err := Resolve(preset, nil)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	// Set PVCReadOnly to its zero value (false) explicitly via override.
	ro := false
	_, hashWithFalse, err := Resolve(preset, &vllmv1alpha1.ModelConfigOverrides{PVCReadOnly: &ro})
	if err != nil {
		t.Fatalf("Resolve with explicit false: %v", err)
	}

	if hashWithout != hashWithFalse {
		t.Errorf("hash drifted on adding explicit PVCReadOnly=false override: %s vs %s — omitempty broken, existing instances would re-roll on upgrade",
			hashWithout, hashWithFalse)
	}
}
