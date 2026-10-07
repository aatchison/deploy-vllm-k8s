package compatibility

import (
	"encoding/json"
	"reflect"
)

// Refs #179. Saved live schemas and render oracle remain unchanged.
// These exact generated-only additions cannot approve modified live fields.
func approvedSecurity179Difference(plural string, d Difference) bool {
	if d.Live != nil {
		return false
	}
	var contracts map[string]map[string]interface{}
	if err := json.Unmarshal([]byte(`{
  "longcontextpresets": {
    "$.properties.spec.properties.securityProfile": {
      "type": "string",
      "default": "default",
      "enum": [
        "default",
        "engram-ipc"
      ]
    },
    "$.properties.spec.x-kubernetes-validations[rule=!has(self.securityProfile) || self.securityProfile != 'engram-ipc' || (has(self.engramConfig) && size(self.engramConfig) > 0)]": {
      "rule": "!has(self.securityProfile) || self.securityProfile != 'engram-ipc' || (has(self.engramConfig) && size(self.engramConfig) > 0)",
      "message": "engram-ipc requires a nonempty engramConfig"
    }
  },
  "longcontextinstances": {
    "$.properties.spec.properties.overrides.properties.securityProfile": {
      "type": "string",
      "enum": [
        "default",
        "engram-ipc"
      ]
    }
  }
}`), &contracts); err != nil {
		return false
	}
	want, ok := contracts[plural][d.Path]
	return ok && reflect.DeepEqual(d.Generated, want)
}
