package v1alpha1

import (
	"k8s.io/apimachinery/pkg/runtime"
	"os"
	"path/filepath"
	"sigs.k8s.io/yaml"
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
			for _, field := range []string{"modelRevision", "codeRevision", "tokenizerRevision", "engramConfig", "trustRemoteCode", "disableCustomAllReduce", "flashinferAutotune"} {
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

func TestFlags178DeepCopy(t *testing.T) {
	f := false
	s := "value"
	p := &LongContextPresetSpec{FlashinferAutotune: &f}
	pc := p.DeepCopy()
	if pc.FlashinferAutotune == p.FlashinferAutotune {
		t.Fatal("preset pointers alias")
	}
	o := &LongContextOverrides{ModelRevision: &s, CodeRevision: &s, TokenizerRevision: &s, TrustRemoteCode: &f, DisableCustomAllReduce: &f, FlashinferAutotune: &f, EngramConfig: &s, ReasoningParser: &s, ChatTemplate: &s, CompilationConfig: &s, SpeculativeConfig: &s}
	c := o.DeepCopy()
	for _, pair := range [][2]*string{{o.ModelRevision, c.ModelRevision}, {o.CodeRevision, c.CodeRevision}, {o.TokenizerRevision, c.TokenizerRevision}, {o.EngramConfig, c.EngramConfig}, {o.ReasoningParser, c.ReasoningParser}, {o.ChatTemplate, c.ChatTemplate}, {o.CompilationConfig, c.CompilationConfig}, {o.SpeculativeConfig, c.SpeculativeConfig}} {
		if pair[0] == pair[1] || *pair[0] != *pair[1] {
			t.Fatal("string deepcopy alias/value")
		}
	}
	for _, pair := range [][2]*bool{{o.TrustRemoteCode, c.TrustRemoteCode}, {o.DisableCustomAllReduce, c.DisableCustomAllReduce}, {o.FlashinferAutotune, c.FlashinferAutotune}} {
		if pair[0] == pair[1] || *pair[0] != *pair[1] {
			t.Fatal("bool deepcopy alias/value")
		}
	}

}

func TestFlags178DocumentedPresetValidates(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "..", "docs", "examples", "qwen3.8-flash-next.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	var preset LongContextPreset
	if err := yaml.UnmarshalStrict(data, &preset); err != nil {
		t.Fatal(err)
	}
	obj, err := runtime.DefaultUnstructuredConverter.ToUnstructured(&preset)
	if err != nil {
		t.Fatal(err)
	}
	if errs := validateGeneratedSchema(t, loadGeneratedSchema(t, "vllm.aatchison.io_longcontextpresets.yaml"), obj); len(errs) != 0 {
		t.Fatalf("documented preset rejected: %v", errs)
	}
}
