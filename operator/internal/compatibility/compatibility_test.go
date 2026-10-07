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
			if approvedGeneratedDifference(tc.plural, difference) {
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
	if !approvedGeneratedDifference("vllminstances", exact) {
		t.Fatal("exact named safety constraint was not approved")
	}
	wrongValue := exact
	wrongValue.Generated = float64(9)
	if approvedGeneratedDifference("vllminstances", wrongValue) {
		t.Fatal("allowlist approved a widened maximum")
	}
	if approvedGeneratedDifference("modelpresets", exact) {
		t.Fatal("allowlist approved a constraint on the wrong CRD")
	}

	enableLora := Difference{Path: prefix + ".properties.overrides.properties.enableLora", Generated: map[string]interface{}{"type": "boolean"}}
	if !approvedGeneratedDifference("longcontextinstances", enableLora) {
		t.Fatal("exact additive GitHub enableLora field was not approved")
	}
	enableLora.Generated = map[string]interface{}{"type": "string"}
	if approvedGeneratedDifference("longcontextinstances", enableLora) {
		t.Fatal("allowlist approved altered enableLora type")
	}
	maxLoraRank := Difference{Path: prefix + ".properties.overrides.properties.maxLoraRank", Generated: map[string]interface{}{"format": "int32", "minimum": float64(1), "type": "integer"}}
	if !approvedGeneratedDifference("vllminstances", maxLoraRank) {
		t.Fatal("exact additive GitHub maxLoraRank field was not approved")
	}
	maxLoraRank.Generated = map[string]interface{}{"format": "int32", "minimum": float64(0), "type": "integer"}
	if approvedGeneratedDifference("vllminstances", maxLoraRank) {
		t.Fatal("allowlist approved altered maxLoraRank minimum")
	}

	unknown := Difference{Path: prefix + ".properties.unrelated", Generated: map[string]interface{}{"type": "string"}}
	if approvedGeneratedDifference("vllminstances", unknown) {
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
	spec, ok := model["spec"].(map[string]interface{})
	if !ok {
		t.Fatal("fixture spec is not an object")
	}
	delete(spec, "enableLora")
	delete(spec, "maxLoraRank")
	result, err := modelSchema.Admit(model)
	if err != nil {
		t.Fatal(err)
	}
	gotSpec, ok := result.Object["spec"].(map[string]interface{})
	if !ok {
		t.Fatal("admitted spec is not an object")
	}
	if gotSpec["enableLora"] != false || gotSpec["maxLoraRank"] != int64(64) {
		t.Errorf("Kubernetes defaults not applied: enableLora=%v maxLoraRank=%v (defaulted paths: %v)", gotSpec["enableLora"], gotSpec["maxLoraRank"], result.DefaultedPaths)
	}

	longPresetSchema := mustSchema(t, filepath.Join("..", "..", "config", "crd", "bases", "vllm.aatchison.io_longcontextpresets.yaml"))
	enumMutant := mustResources(t, filepath.Join("testdata", "live-longcontextpresets.json"))[0]
	resourceSpec(t, enumMutant)["imagePullPolicy"] = "Sometimes"
	assertRejected(t, longPresetSchema, enumMutant, "imagePullPolicy", "enum")

	patternMutant := mustResources(t, filepath.Join("testdata", "live-modelpresets.json"))[0]
	resourceSpec(t, patternMutant)["gpuMemoryUtilization"] = "not-a-fraction"
	assertRejected(t, modelSchema, patternMutant, "gpuMemoryUtilization", "pattern")

	longInstanceSchema := mustSchema(t, filepath.Join("..", "..", "config", "crd", "bases", "vllm.aatchison.io_longcontextinstances.yaml"))
	celMutant := mustResources(t, filepath.Join("testdata", "live-longcontextinstances.json"))[0]
	celSpec := resourceSpec(t, celMutant)
	celSpec["replicas"] = float64(3)
	celSpec["sharedStorage"] = true
	assertRejected(t, longInstanceSchema, celMutant, "replicas must be 0, 1, or 2", "CEL")

	fractionalIntegerMutant := mustResources(t, filepath.Join("testdata", "live-longcontextinstances.json"))[0]
	fractionalIntegerMutant["spec"].(map[string]interface{})["replicas"] = float64(1.5)
	assertRejected(t, longInstanceSchema, fractionalIntegerMutant, "replicas", "fractional integer")
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

// approvedGeneratedDifference is deliberately an exact allowlist. It keeps
// two separate classes of additive GitHub compatibility differences: the
// named #177 schema hardening/shared-storage additions, and the three existing
// GitHub LoRA override fields. None changes or removes a live schema field.
func approvedGeneratedDifference(plural string, d Difference) bool {
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
	// These GitHub-only LoRA override fields predate this port. Preserve their
	// additive API and validation without treating them as live ground truth.
	githubLoRA := map[string]interface{}{
		prefix + ".properties.overrides.properties.enableLora":  map[string]interface{}{"type": "boolean"},
		prefix + ".properties.overrides.properties.loraModules": map[string]interface{}{"type": "string"},
		prefix + ".properties.overrides.properties.maxLoraRank": map[string]interface{}{"format": "int32", "minimum": float64(1), "type": "integer"},
	}
	if want, ok := githubLoRA[d.Path]; ok {
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

func resourceSpec(t *testing.T, object map[string]interface{}) map[string]interface{} {
	t.Helper()
	spec, ok := object["spec"].(map[string]interface{})
	if !ok {
		t.Fatal("resource spec is not an object")
	}
	return spec
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
