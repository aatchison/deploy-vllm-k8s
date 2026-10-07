package v1alpha1

import "testing"

func TestSecurity179GeneratedSchema(t *testing.T) {
	for _, file := range []string{"vllm.aatchison.io_longcontextpresets.yaml", "vllm.aatchison.io_longcontextinstances.yaml"} {
		schema := loadGeneratedSchema(t, file)
		for _, tc := range []struct {
			profile string
			engram  interface{}
			valid   bool
		}{
			{"default", nil, true}, {"engram-ipc", `{"cpu_offload":true}`, true},
			{"engram-ipc", nil, false}, {"engram-ipc", "", false}, {"root", `{}`, false},
		} {
			t.Run(file+"/"+tc.profile, func(t *testing.T) {
				fields := map[string]interface{}{"securityProfile": tc.profile}
				if tc.engram != nil {
					fields["engramConfig"] = tc.engram
				}
				var obj map[string]interface{}
				if file == "vllm.aatchison.io_longcontextinstances.yaml" {
					obj = overrideValidationObject("securityProfile", tc.profile)
					obj["spec"].(map[string]interface{})["overrides"] = fields
				} else {
					fields["modelID"] = "m"
					fields["migResource"] = "nvidia.com/mig-4g.96gb"
					fields["migResourceCount"] = int64(1)
					fields["tensorParallelSize"] = int64(1)
					fields["maxModelLen"] = int64(1024)
					fields["gpuMemoryUtilization"] = "0.9"
					fields["shmSizeLimit"] = "8Gi"
					fields["progressDeadlineSeconds"] = int64(600)
					fields["kvCacheDtype"] = "auto"
					fields["kvOffloadBackend"] = "none"
					fields["livenessProbe"] = map[string]interface{}{"initialDelaySeconds": int64(0), "periodSeconds": int64(10), "failureThreshold": int64(3)}
					fields["readinessProbe"] = fields["livenessProbe"]
					obj = map[string]interface{}{"spec": fields}
				}
				errs := validateGeneratedSchema(t, schema, obj)
				if (len(errs) == 0) != tc.valid {
					t.Fatalf("valid=%v errors=%v", tc.valid, errs)
				}
			})
		}
	}
}
