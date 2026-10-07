package compatibility

import (
	"reflect"
	"testing"
)

// #186 adds opt-in host-memory fields. Pin only those generated-only schema
// additions; do not change the saved live schema or rendering oracle.
func memorySchemaAdditions(plural string) map[string]interface{} {
	prefix := "$.properties.spec"
	override := plural == "vllminstances" || plural == "longcontextinstances"
	if !override && plural != "modelpresets" && plural != "longcontextpresets" {
		return nil
	}
	if override {
		prefix += ".properties.overrides"
	}
	pattern := `^([0-9]+(\.[0-9]*)?|\.[0-9]+)([eE][+-]?[0-9]+|[numkKMGTPE]|[KMGTPE]i)?$`
	if override {
		pattern = "^$|" + pattern
	}
	want := map[string]interface{}{}
	rules := map[string]interface{}{}
	for _, field := range []string{"memoryRequest", "memoryLimit"} {
		want[prefix+".properties."+field] = map[string]interface{}{"type": "string", "pattern": pattern}
		rule := "!has(self." + field + ") || "
		if override {
			rule += "size(self." + field + ") == 0 || "
		}
		rule += "(isQuantity(self." + field + ") && quantity(self." + field + ").isGreaterThan(quantity('0')))"
		rules[rule] = map[string]interface{}{"rule": rule, "message": field + " must be a positive Kubernetes quantity"}
	}
	rule := "!has(self.memoryRequest) || !has(self.memoryLimit) || size(self.memoryRequest) == 0 || size(self.memoryLimit) == 0 || (isQuantity(self.memoryRequest) && isQuantity(self.memoryLimit) && !quantity(self.memoryRequest).isGreaterThan(quantity(self.memoryLimit)))"
	rules[rule] = map[string]interface{}{"rule": rule, "message": "memoryLimit must be greater than or equal to memoryRequest"}
	// The differ compares rule keys individually when a validation map already
	// exists, but reports the whole map when all rules are newly introduced.
	if plural == "longcontextpresets" {
		for rule, value := range rules {
			want[prefix+".x-kubernetes-validations[rule="+rule+"]"] = value
		}
	} else if plural != "longcontextinstances" {
		want[prefix+".x-kubernetes-validations"] = rules
	}
	// LongContextInstance now has the security rule plus these three rules.
	// Its whole map is approved only by approvedCombinedOverrideDifference.
	return want
}

func approvedMemoryDifference(plural string, d Difference) bool {
	want, ok := memorySchemaAdditions(plural)[d.Path]
	return ok && d.Live == nil && reflect.DeepEqual(d.Generated, want)
}

func TestMemorySchemaAllowlistIsExact(t *testing.T) {
	for _, plural := range []string{"modelpresets", "longcontextpresets", "vllminstances", "longcontextinstances"} {
		for path, want := range memorySchemaAdditions(plural) {
			d := Difference{Path: path, Generated: want}
			if !approvedGeneratedDifference(plural, d) {
				t.Fatalf("missing approval %s %s", plural, path)
			}
			d.Generated = map[string]interface{}{"type": "integer"}
			if approvedGeneratedDifference(plural, d) {
				t.Fatalf("altered field/rule approved %s %s", plural, path)
			}
			d.Generated = want
			d.Live = want
			if approvedGeneratedDifference(plural, d) {
				t.Fatalf("changed live field approved %s %s", plural, path)
			}
		}
	}
}
