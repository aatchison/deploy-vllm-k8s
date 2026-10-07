package v1alpha1

import (
	"encoding/json"
	"os"
	"reflect"
	"testing"

	"sigs.k8s.io/yaml"
)

// loraSchemaObject checks each object on a schema path before descending.
func loraSchemaObject(t *testing.T, object map[string]interface{}, path ...string) map[string]interface{} {
	t.Helper()
	for i, key := range path {
		value, ok := object[key].(map[string]interface{})
		if !ok {
			t.Fatalf("schema path %v: expected object, got %T", path[:i+1], object[key])
		}
		object = value
	}
	return object
}

// Snapshot of the six LoRA field schemas in forgejo-experiments adoption CRDs
// at 4d89435459b9e46f9ac046a16364a1563e950d36. Compare every OpenAPI key.
func TestLoraSchemaParity(t *testing.T) {
	raw, err := os.ReadFile("testdata/lora-schema.json")
	if err != nil {
		t.Fatal(err)
	}
	var want map[string]map[string]interface{}
	if err := json.Unmarshal(raw, &want); err != nil {
		t.Fatal(err)
	}
	for kind, fields := range want {
		t.Run(kind, func(t *testing.T) {
			raw, err := os.ReadFile("../../config/crd/bases/vllm.aatchison.io_" + kind + ".yaml")
			if err != nil {
				t.Fatal(err)
			}
			var crd map[string]interface{}
			if err := yaml.Unmarshal(raw, &crd); err != nil {
				t.Fatal(err)
			}
			spec := loraSchemaObject(t, crd, "spec")
			versions, ok := spec["versions"].([]interface{})
			if !ok {
				t.Fatalf("spec.versions: expected array, got %T", spec["versions"])
			}
			found := false
			for _, item := range versions {
				v, ok := item.(map[string]interface{})
				if !ok {
					t.Fatalf("spec.versions entry: expected object, got %T", item)
				}
				if v["name"] != "v1alpha1" {
					continue
				}
				found = true
				p := loraSchemaObject(t, v, "schema", "openAPIV3Schema", "properties", "spec", "properties")
				for field, expected := range fields {
					if !reflect.DeepEqual(p[field], expected) {
						t.Errorf("%s: got %#v; want %#v", field, p[field], expected)
					}
				}
			}
			if !found {
				t.Fatal("v1alpha1 schema missing")
			}
		})
	}
}
