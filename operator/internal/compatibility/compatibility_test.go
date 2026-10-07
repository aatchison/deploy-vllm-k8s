package compatibility

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
)

const version = "v1alpha1"

type crdCase struct{ plural, resources string }

var crdCases = []crdCase{
	{"longcontextinstances", "live-longcontextinstances.json"},
	{"longcontextpresets", "live-longcontextpresets.json"},
	{"modelpresets", "live-modelpresets.json"},
	{"vllminstances", "live-vllminstances.json"},
}

func TestGeneratedSchemasMatchLiveContracts(t *testing.T) {
	reports := map[string][]Difference{}
	var failures []string
	for _, tc := range crdCases {
		live := mustSchema(t, filepath.Join("testdata", "live-crd-"+tc.plural+".json"))
		generated := mustSchema(t, filepath.Join("..", "..", "config", "crd", "bases", "vllm.aatchison.io_"+tc.plural+".yaml"))
		differences, err := Differences(live, generated)
		if err != nil {
			t.Fatalf("compare %s schemas: %v", tc.plural, err)
		}
		reports[tc.plural] = differences
		t.Logf("%s: %d field-level schema differences", tc.plural, len(differences))
		for _, difference := range differences {
			status := "UNAPPROVED"
			if approvedSafetyDifference(tc.plural, difference) {
				status = "approved"
			} else {
				failures = append(failures, tc.plural+": "+difference.String())
			}
			t.Logf("  %s: %s", status, difference.String())
		}
	}
	// This assertion makes accidental omission of any ground-truth fixture visible.
	if len(reports) != 4 {
		t.Fatalf("compared %d CRDs, want all four", len(reports))
	}
	if len(failures) != 0 {
		sort.Strings(failures)
		t.Fatalf("unapproved live/generated schema differences:\n  %s", strings.Join(failures, "\n  "))
	}
}

func TestSafetyAllowlistIsExact(t *testing.T) {
	prefix := "$.properties.spec"
	exact := Difference{Path: prefix + ".properties.overrides.properties.migResourceCount.maximum", Generated: float64(8)}
	if !approvedSafetyDifference("vllminstances", exact) {
		t.Fatal("exact named safety constraint was not approved")
	}
	wrongValue := exact
	wrongValue.Generated = float64(9)
	if approvedSafetyDifference("vllminstances", wrongValue) {
		t.Fatal("allowlist approved a widened maximum")
	}
	if approvedSafetyDifference("modelpresets", exact) {
		t.Fatal("allowlist approved a constraint on the wrong CRD")
	}
	unknown := Difference{Path: prefix + ".properties.unrelated", Generated: map[string]interface{}{"type": "string"}}
	if approvedSafetyDifference("vllminstances", unknown) {
		t.Fatal("allowlist approved an unnamed addition")
	}
}

func TestSavedResourcesSurviveGeneratedAdmission(t *testing.T) {
	for _, tc := range crdCases {
		t.Run(tc.plural, func(t *testing.T) {
			schema := mustSchema(t, filepath.Join("..", "..", "config", "crd", "bases", "vllm.aatchison.io_"+tc.plural+".yaml"))
			resources := mustResources(t, filepath.Join("testdata", tc.resources))
			for i, resource := range resources {
				name := fmt.Sprintf("item-%d", i)
				if metadata, ok := resource["metadata"].(map[string]interface{}); ok {
					if n, ok := metadata["name"].(string); ok {
						name = n
					}
				}
				t.Run(name, func(t *testing.T) {
					result, err := schema.Admit(resource)
					if err != nil {
						t.Fatal(err)
					}
					if len(result.PrunedPaths) != 0 {
						t.Errorf("structural pruning would lose fields: %s", strings.Join(result.PrunedPaths, ", "))
					}
					if len(result.Errors) != 0 {
						t.Errorf("Kubernetes OpenAPI/CEL rejection: %v", result.Errors.ToAggregate())
					}
				})
			}
		})
	}
}

func TestAdmissionExercisesDefaultsEnumsPatternsAndCEL(t *testing.T) {
	modelSchema := mustSchema(t, filepath.Join("..", "..", "config", "crd", "bases", "vllm.aatchison.io_modelpresets.yaml"))
	model := mustResources(t, filepath.Join("testdata", "live-modelpresets.json"))[0]
	spec := model["spec"].(map[string]interface{})
	delete(spec, "enableLora")
	delete(spec, "maxLoraRank")
	result, err := modelSchema.Admit(model)
	if err != nil {
		t.Fatal(err)
	}
	gotSpec := result.Object["spec"].(map[string]interface{})
	if gotSpec["enableLora"] != false || gotSpec["maxLoraRank"] != float64(64) {
		t.Errorf("Kubernetes defaults not applied: enableLora=%v maxLoraRank=%v (defaulted paths: %v)", gotSpec["enableLora"], gotSpec["maxLoraRank"], result.DefaultedPaths)
	}

	longPresetSchema := mustSchema(t, filepath.Join("..", "..", "config", "crd", "bases", "vllm.aatchison.io_longcontextpresets.yaml"))
	enumMutant := mustResources(t, filepath.Join("testdata", "live-longcontextpresets.json"))[0]
	enumMutant["spec"].(map[string]interface{})["imagePullPolicy"] = "Sometimes"
	assertRejected(t, longPresetSchema, enumMutant, "imagePullPolicy", "enum")

	patternMutant := mustResources(t, filepath.Join("testdata", "live-modelpresets.json"))[0]
	patternMutant["spec"].(map[string]interface{})["gpuMemoryUtilization"] = "not-a-fraction"
	assertRejected(t, modelSchema, patternMutant, "gpuMemoryUtilization", "pattern")

	longInstanceSchema := mustSchema(t, filepath.Join("..", "..", "config", "crd", "bases", "vllm.aatchison.io_longcontextinstances.yaml"))
	celMutant := mustResources(t, filepath.Join("testdata", "live-longcontextinstances.json"))[0]
	celSpec := celMutant["spec"].(map[string]interface{})
	celSpec["replicas"] = float64(3)
	celSpec["sharedStorage"] = true
	assertRejected(t, longInstanceSchema, celMutant, "replicas must be 0, 1, or 2", "CEL")
}

func assertRejected(t *testing.T, schema *Schema, resource map[string]interface{}, want, mechanism string) {
	t.Helper()
	result, err := schema.Admit(resource)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Errors) == 0 {
		t.Fatalf("invalid %s mutant was accepted", mechanism)
	}
	if got := result.Errors.ToAggregate().Error(); !strings.Contains(got, want) {
		t.Fatalf("%s mutant rejected for unexpected reason: %s", mechanism, got)
	}
}

// approvedSafetyDifference is deliberately an exact allowlist. These are the
// named #177 schema hardening changes plus the shared-storage safety addition.
func approvedSafetyDifference(plural string, d Difference) bool {
	if plural != "vllminstances" && plural != "longcontextinstances" {
		return false
	}
	prefix := "$.properties.spec"
	constraints := map[string]interface{}{
		prefix + ".properties.overrides.properties.gpuMemoryUtilization.pattern": `^0?\.[0-9]+$|^1\.0$`,
		prefix + ".properties.overrides.properties.migResource.pattern":          `^nvidia\.com/mig-[0-9]+g\.[0-9]+gb$`,
		prefix + ".properties.overrides.properties.migResourceCount.minimum":     float64(1),
		prefix + ".properties.overrides.properties.migResourceCount.maximum":     float64(8),
		prefix + ".properties.overrides.properties.shmSizeLimit.pattern":         `^[0-9]+[KMGT]i?$`,
	}
	if want, ok := constraints[d.Path]; ok {
		return d.Live == nil && reflect.DeepEqual(d.Generated, want)
	}
	if d.Path == prefix+".properties.sharedStorage" {
		return d.Live == nil && reflect.DeepEqual(d.Generated, map[string]interface{}{"type": "boolean"})
	}
	rule := "!has(self.replicas) || self.replicas <= 1 || self.sharedStorage == true"
	if d.Path == prefix+".x-kubernetes-validations[rule="+rule+"]" {
		want := map[string]interface{}{"message": "replicas > 1 requires sharedStorage=true (PVC must support multi-node access)", "rule": rule}
		return d.Live == nil && reflect.DeepEqual(d.Generated, want)
	}
	return false
}

func mustSchema(t *testing.T, path string) *Schema {
	t.Helper()
	s, err := LoadSchema(path, version)
	if err != nil {
		t.Fatal(err)
	}
	return s
}
func mustResources(t *testing.T, path string) []map[string]interface{} {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var resources []map[string]interface{}
	if err := json.Unmarshal(data, &resources); err != nil {
		t.Fatal(err)
	}
	return resources
}
