package compatibility

import (
	"path/filepath"
	"reflect"
	"testing"
)

const combinedOverridePath = "$.properties.spec.properties.overrides.x-kubernetes-validations"

// Approve only the exact #179 + #186 whole-map addition on LongContextInstance.
// Saved live schemas and the render oracle are not changed.
func combinedOverrideRules() map[string]interface{} {
	rules := map[string]interface{}{}
	for _, field := range []string{"memoryRequest", "memoryLimit"} {
		rule := "!has(self." + field + ") || size(self." + field + ") == 0 || (isQuantity(self." + field + ") && quantity(self." + field + ").isGreaterThan(quantity('0')))"
		rules[rule] = map[string]interface{}{"rule": rule, "message": field + " must be a positive Kubernetes quantity"}
	}
	pair := "!has(self.memoryRequest) || !has(self.memoryLimit) || size(self.memoryRequest) == 0 || size(self.memoryLimit) == 0 || (isQuantity(self.memoryRequest) && isQuantity(self.memoryLimit) && !quantity(self.memoryRequest).isGreaterThan(quantity(self.memoryLimit)))"
	rules[pair] = map[string]interface{}{"rule": pair, "message": "memoryLimit must be greater than or equal to memoryRequest"}
	security := "!has(self.securityProfile) || self.securityProfile != 'engram-ipc' || (has(self.engramConfig) && size(self.engramConfig) > 0)"
	rules[security] = map[string]interface{}{"rule": security, "message": "an engram-ipc override requires a nonempty overrides.engramConfig"}
	return rules
}

func approvedCombinedOverrideDifference(plural string, d Difference) bool {
	return plural == "longcontextinstances" && d.Path == combinedOverridePath && d.Live == nil && reflect.DeepEqual(d.Generated, combinedOverrideRules())
}

func TestCombinedOverrideAllowlistIsExact(t *testing.T) {
	live := mustSchema(t, filepath.Join("testdata", "live-crd-longcontextinstances.json"))
	generated := mustSchema(t, filepath.Join("..", "..", "config", "crd", "bases", "vllm.aatchison.io_longcontextinstances.yaml"))
	diffs, err := Differences(live, generated)
	if err != nil {
		t.Fatal(err)
	}
	var exact *Difference
	for i := range diffs {
		if diffs[i].Path == combinedOverridePath {
			exact = &diffs[i]
		}
	}
	if exact == nil {
		t.Fatal("actual differ did not report the combined override map")
	}
	rules, ok := exact.Generated.(map[string]interface{})
	if !ok || len(rules) != 4 {
		t.Fatalf("combined map must have exactly four rules: %v", exact.Generated)
	}
	if !approvedGeneratedDifference("longcontextinstances", *exact) || !approvedCombinedOverrideDifference("longcontextinstances", *exact) {
		t.Fatal("exact combined map rejected")
	}
	for _, mutation := range []string{"changed rule", "extra rule", "missing rule", "changed message"} {
		t.Run(mutation, func(t *testing.T) {
			mutant := combinedOverrideRules()
			key := "!has(self.securityProfile) || self.securityProfile != 'engram-ipc' || (has(self.engramConfig) && size(self.engramConfig) > 0)"
			switch mutation {
			case "changed rule":
				mutant[key] = map[string]interface{}{"rule": "true", "message": "an engram-ipc override requires a nonempty overrides.engramConfig"}
			case "extra rule":
				mutant["true"] = map[string]interface{}{"rule": "true", "message": "extra"}
			case "missing rule":
				delete(mutant, key)
			case "changed message":
				mutant[key] = map[string]interface{}{"rule": key, "message": "changed"}
			}
			d := *exact
			d.Generated = mutant
			if approvedGeneratedDifference("longcontextinstances", d) {
				t.Fatal("altered combined map approved")
			}
		})
	}
	d := *exact
	d.Live = d.Generated
	if approvedGeneratedDifference("longcontextinstances", d) {
		t.Fatal("changed live map approved")
	}
	d = *exact
	if approvedCombinedOverrideDifference("vllminstances", d) {
		t.Fatal("wrong CRD approved")
	}
	d.Path += ".unknown"
	if approvedGeneratedDifference("longcontextinstances", d) {
		t.Fatal("wrong path approved")
	}
}
