package v1alpha1

import (
	"reflect"
	"testing"
)

// The live Forgejo API uses these Go types and JSON field names.
func TestForgejoPortFieldTypes(t *testing.T) {
	cases := []struct {
		typ    reflect.Type
		fields map[string]string
	}{
		{reflect.TypeFor[ModelPresetSpec](), map[string]string{"ReasoningParser": "string", "ChatTemplate": "string", "EnableLora": "*bool", "MaxLoraRank": "*int"}},
		{reflect.TypeFor[ModelConfigOverrides](), map[string]string{"ReasoningParser": "*string", "ChatTemplate": "*string"}},
		{reflect.TypeFor[LongContextPresetSpec](), map[string]string{"ReasoningParser": "string", "ChatTemplate": "string", "SpeculativeConfig": "string", "LimitMmPerPrompt": "string", "MaxNumSeqs": "int32", "KVCacheDtypeSkipLayers": "string", "MambaCacheMode": "string", "MambaBackend": "string", "EnforceEager": "*bool", "CompilationConfig": "*string", "EnableLora": "*bool", "MaxLoraRank": "*int", "Env": "[]v1.EnvVar"}},
		{reflect.TypeFor[LongContextOverrides](), map[string]string{"ReasoningParser": "*string", "ChatTemplate": "*string", "SpeculativeConfig": "*string", "MambaCacheMode": "*string", "MambaBackend": "*string", "EnforceEager": "*bool", "CompilationConfig": "*string"}},
		{reflect.TypeFor[LongContextInstanceSpec](), map[string]string{"Env": "[]v1.EnvVar"}},
	}
	for _, c := range cases {
		t.Run(c.typ.Name(), func(t *testing.T) {
			for name, want := range c.fields {
				f, ok := c.typ.FieldByName(name)
				if !ok {
					t.Errorf("missing %s", name)
					continue
				}
				if f.Type.String() != want {
					t.Errorf("%s type=%s want=%s", name, f.Type, want)
				}
			}
		})
	}
}
