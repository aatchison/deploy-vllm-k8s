package v1alpha1

import "testing"

func TestGeneratedSharedMemoryOverrideValidation(t *testing.T) {
	for _, file := range []string{"vllm.aatchison.io_vllminstances.yaml", "vllm.aatchison.io_longcontextinstances.yaml"} {
		t.Run(file, func(t *testing.T) {
			schema := loadGeneratedSchema(t, file)
			for _, value := range []string{"invalid", "", "-1Gi", "8Gi", "64Mi"} {
				obj := overrideValidationObject("shmSizeLimit", value)
				errs := validateGeneratedSchema(t, schema, obj)
				valid := value == "8Gi" || value == "64Mi"
				if valid != (len(errs) == 0) {
					t.Errorf("shmSizeLimit=%q: valid=%v errors=%v", value, valid, errs)
				}
			}
		})
	}
}

func overrideValidationObject(field string, value interface{}) map[string]interface{} {
	return map[string]interface{}{
		"spec": map[string]interface{}{
			"presetRef": map[string]interface{}{"name": "preset"},
			"pvcName":   "models", "hfToken": map[string]interface{}{"name": "hf", "key": "token"},
			"overrides": map[string]interface{}{field: value},
		},
	}
}

func TestGeneratedMIGOverrideValidation(t *testing.T) {
	for _, file := range []string{"vllm.aatchison.io_vllminstances.yaml", "vllm.aatchison.io_longcontextinstances.yaml"} {
		schema := loadGeneratedSchema(t, file)
		for _, tc := range []struct {
			field string
			value interface{}
			valid bool
		}{
			{"migResource", "nvidia.com/gpu", false}, {"migResource", "example.com/device", false},
			{"migResource", "nvidia.com/mig-2g.48gb", true},
			{"migResourceCount", int64(0), false}, {"migResourceCount", int64(9), false}, {"migResourceCount", int64(8), true},
			{"gpuMemoryUtilization", "garbage", false}, {"gpuMemoryUtilization", "1.1", false}, {"gpuMemoryUtilization", "0.9", true},
		} {
			errs := validateGeneratedSchema(t, schema, overrideValidationObject(tc.field, tc.value))
			if tc.valid != (len(errs) == 0) {
				t.Errorf("%s %s=%v: valid=%v errors=%v", file, tc.field, tc.value, tc.valid, errs)
			}
		}
	}
}
