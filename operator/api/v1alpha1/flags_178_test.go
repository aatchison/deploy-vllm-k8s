package v1alpha1

import (
	"strings"
	"testing"
)

func TestFlags178GeneratedValidation(t *testing.T) {
	for _, file := range []string{"vllm.aatchison.io_longcontextpresets.yaml", "vllm.aatchison.io_longcontextinstances.yaml"} {
		t.Run(file, func(t *testing.T) {
			schema := loadGeneratedSchema(t, file)
			spec := schema.Properties["spec"]
			fields := spec.Properties
			instance := strings.Contains(file, "instances")
			if instance {
				fields = fields["overrides"].Properties
			}
			for _, name := range []string{"modelRevision", "codeRevision", "tokenizerRevision", "trustRemoteCode", "engramConfig", "disableCustomAllReduce", "flashinferAutotune", "maxNumSeqs", "reasoningParser", "chatTemplate", "compilationConfig", "speculativeConfig"} {
				if _, ok := fields[name]; !ok {
					t.Errorf("generated schema missing %s", name)
				}
			}
			for _, field := range []string{"modelRevision", "codeRevision", "tokenizerRevision", "engramConfig", "compilationConfig", "speculativeConfig", "maxNumSeqs", "trustRemoteCode", "disableCustomAllReduce", "flashinferAutotune"} {
				var values []interface{}
				var valid []bool
				switch field {
				case "modelRevision", "codeRevision", "tokenizerRevision":
					values = []interface{}{strings.Repeat("a", 40), strings.Repeat("A", 40), "main", "", strings.Repeat("g", 40), strings.Repeat("a", 39), strings.Repeat("a", 41)}
					valid = []bool{true, true, false, false, false, false, false}
				case "maxNumSeqs":
					values = []interface{}{int64(1), int64(32), int64(0), int64(-1)}
					valid = []bool{true, true, false, false}
				case "trustRemoteCode", "disableCustomAllReduce", "flashinferAutotune":
					values = []interface{}{true, false, "false"}
					valid = []bool{true, true, false}
				default:
					values = []interface{}{`{}`, " \n{\"x\":1}\t", `[]`, `null`, `"x"`, ``, `{} trailing`}
					valid = []bool{true, true, false, false, false, true, false}
				}
				for i, value := range values {
					obj := overrideValidationObject(field, value)
					if !instance {
						obj = map[string]interface{}{"spec": map[string]interface{}{
							"modelID": "m", "migResource": "nvidia.com/mig-4g.96gb", "migResourceCount": int64(1), "maxModelLen": int64(4096), "gpuMemoryUtilization": "0.9", "tensorParallelSize": int64(1), "shmSizeLimit": "8Gi", "progressDeadlineSeconds": int64(600), "livenessProbe": map[string]interface{}{"initialDelaySeconds": int64(0), "periodSeconds": int64(30), "failureThreshold": int64(10)}, "readinessProbe": map[string]interface{}{"initialDelaySeconds": int64(0), "periodSeconds": int64(5), "failureThreshold": int64(6)}, "kvCacheDtype": "auto", "kvOffloadBackend": "none", field: value,
						}}
					}
					errs := validateGeneratedSchema(t, schema, obj)
					if valid[i] != (len(errs) == 0) {
						t.Errorf("%s=%v valid=%v errors=%v", field, value, valid[i], errs)
					}
				}
			}
		})
	}
}
