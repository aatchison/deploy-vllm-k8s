package compatibility

import "reflect"

// approvedFlags178Difference permits only the opt-in additive fields from #178.
// The saved live schemas and render oracle remain ground truth.
func approvedFlags178Difference(plural string, d Difference) bool {
	if d.Live != nil || (plural != "longcontextpresets" && plural != "longcontextinstances") {
		return false
	}
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
		if d.Path == prefix+name {
			return reflect.DeepEqual(d.Generated, schema)
		}
	}
	return false
}
