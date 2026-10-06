package vllm

import (
	"strings"
	"testing"

	v1 "github.com/aatchison/deploy-vllm-k8s/operator/api/v1alpha1"
)

func TestValidateSharedMemory(t *testing.T) {
	for _, value := range []string{"", "invalid", "-1Gi", "0Gi"} {
		t.Run(value, func(t *testing.T) {
			err := ValidateEffectiveConfig(EffectiveConfig{SHMSizeLimit: value})
			if err == nil || !strings.Contains(err.Error(), "shmSizeLimit") {
				t.Fatalf("want shmSizeLimit validation error, got %v", err)
			}
		})
	}
	for _, value := range []string{"1Gi", "8G", "64Mi"} {
		t.Run(value, func(t *testing.T) {
			if err := ValidateEffectiveConfig(EffectiveConfig{SHMSizeLimit: value}); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestPresetlessSharedMemoryValidation(t *testing.T) {
	for _, longContext := range []bool{false, true} {
		var e EffectiveConfig
		var err error
		model, mig := "test/model", "nvidia.com/mig-1g.5gb"
		length := int32(1024)
		if longContext {
			e, _, err = ResolveLongContext(nil, &v1.LongContextOverrides{ModelID: &model, MIGResource: &mig, MaxModelLen: &length})
		} else {
			e, _, err = Resolve(nil, &v1.ModelConfigOverrides{ModelID: &model, MIGResource: &mig, MaxModelLen: &length})
		}
		if err != nil {
			t.Fatal(err)
		}
		if err := ValidateEffectiveConfig(e); err == nil || !strings.Contains(err.Error(), "shmSizeLimit") {
			t.Fatalf("longContext=%v: want shmSizeLimit error, got %v", longContext, err)
		}
	}
}
