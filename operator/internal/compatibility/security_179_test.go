package compatibility

import (
	"encoding/json"
	"testing"
)

// Refs #179. Approve only the additive security contract, never a live-field change.
func TestSecurity179SchemaAllowlistIsExact(t *testing.T) {
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
		t.Fatal(err)
	}
	for plural, entries := range contracts {
		for path, value := range entries {
			t.Run(plural+"/"+path, func(t *testing.T) {
				d := Difference{Path: path, Generated: value}
				if !approvedGeneratedDifference(plural, d) {
					t.Fatal("exact additive security schema rejected")
				}
				d.Live = value
				if approvedGeneratedDifference(plural, d) {
					t.Fatal("changed live contract approved")
				}
				d.Live = nil
				if approvedGeneratedDifference("modelpresets", d) {
					t.Fatal("wrong CRD approved")
				}
				d.Path += ".unknown"
				if approvedGeneratedDifference(plural, d) {
					t.Fatal("wrong path approved")
				}
				d.Path = path
				d.Generated = map[string]interface{}{"type": "string"}
				if approvedGeneratedDifference(plural, d) {
					t.Fatal("altered schema approved")
				}
			})
		}
	}
}
