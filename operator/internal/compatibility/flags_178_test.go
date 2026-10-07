package compatibility

import (
	"reflect"
	"testing"
)

func TestFlags178SchemaAllowancesAreExact(t *testing.T) {
	for _, plural := range []string{"longcontextpresets", "longcontextinstances"} {
		prefix := "$.properties.spec.properties."
		if plural == "longcontextinstances" {
			prefix += "overrides.properties."
		}
		fields := map[string]interface{}{
			"modelRevision":          map[string]interface{}{"type": "string", "pattern": `^[0-9a-fA-F]{40}$`},
			"codeRevision":           map[string]interface{}{"type": "string", "pattern": `^[0-9a-fA-F]{40}$`},
			"tokenizerRevision":      map[string]interface{}{"type": "string", "pattern": `^[0-9a-fA-F]{40}$`},
			"trustRemoteCode":        map[string]interface{}{"type": "boolean"},
			"engramConfig":           map[string]interface{}{"type": "string", "pattern": `^$|^[ \t\r\n]*\{[\s\S]*\}[ \t\r\n]*$`},
			"disableCustomAllReduce": map[string]interface{}{"type": "boolean"},
			"flashinferAutotune":     map[string]interface{}{"type": "boolean"},
		}
		if plural == "longcontextpresets" {
			fields["trustRemoteCode"] = map[string]interface{}{"type": "boolean", "default": false}
		} else {
			fields["maxNumSeqs"] = map[string]interface{}{"type": "integer", "format": "int32", "minimum": float64(0)}
		}
		for name, schema := range fields {
			t.Run(plural+"/"+name, func(t *testing.T) {
				exact := Difference{Path: prefix + name, Generated: schema}
				if !approvedGeneratedDifference(plural, exact) {
					t.Fatal("exact additive field rejected")
				}
				for _, wrong := range []string{"modelpresets", "vllminstances"} {
					if approvedGeneratedDifference(wrong, exact) {
						t.Fatal("wrong CRD approved")
					}
				}
				modified, ok := schema.(map[string]interface{})
				if !ok {
					t.Fatal("expected object schema")
				}
				for key, value := range modified {
					mutant := make(map[string]interface{})
					for k, v := range modified {
						mutant[k] = v
					}
					mutant[key] = "changed"
					if reflect.DeepEqual(value, mutant[key]) {
						t.Fatal("control did not change schema")
					}
					if approvedGeneratedDifference(plural, Difference{Path: exact.Path, Generated: mutant}) {
						t.Fatalf("altered %s approved", key)
					}
				}
				if approvedGeneratedDifference(plural, Difference{Path: exact.Path, Live: schema, Generated: schema}) {
					t.Fatal("live-field change approved")
				}
				if approvedGeneratedDifference(plural, Difference{Path: exact.Path + "Other", Generated: schema}) {
					t.Fatal("wrong field approved")
				}
			})
		}
	}
}
