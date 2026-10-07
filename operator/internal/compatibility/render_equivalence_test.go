// Package compatibility contains offline render-equivalence tests.
package compatibility

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"

	vllmv1alpha1 "github.com/aatchison/deploy-vllm-k8s/operator/api/v1alpha1"
	"github.com/aatchison/deploy-vllm-k8s/operator/internal/vllm"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// renderArtifact is deliberately the complete desired object.  The companion
// projection test below names the fields whose equivalence is required by #182;
// retaining the full JSON makes an unexpected extra field visible in review.
type renderArtifact struct {
	Deployment any `json:"deployment"`
	Service    any `json:"service"`
}

type renderSet map[string]renderArtifact

func TestRenderLiveLongContextInstances(t *testing.T) {
	got := renderLiveLongContextInstances(t)
	if len(got) != 9 {
		t.Fatalf("rendered %d live LongContextInstance/preset pairs; want 9", len(got))
	}
	dumpRenderSet(t, got)

	// Normal test runs are strict: the saved Forgejo render is the oracle.
	// The dump is merely a reproducible capture aid; it never weakens this gate.
	want := readRenderSet(t, filepath.Join("testdata", "render-forgejo-aeaf88a4fe4fa6fba886d6c2f89e8f5e01ba5cbd.json"))
	assertRenderEquivalent(t, want, got)
}

func TestRenderLiveVLLMInstancesFixtureIsEmpty(t *testing.T) {
	var instances []vllmv1alpha1.VLLMInstance
	readJSON(t, filepath.Join("testdata", "live-vllminstances.json"), &instances)
	if len(instances) != 0 {
		t.Fatalf("live VLLMInstance fixture has %d item(s): add them to the render harness", len(instances))
	}
	t.Log("live VLLMInstance fixture is empty; ModelPreset fixtures are schema-only coverage")
}

func renderLiveLongContextInstances(t *testing.T) renderSet {
	t.Helper()
	var instances []vllmv1alpha1.LongContextInstance
	var presets []vllmv1alpha1.LongContextPreset
	readJSON(t, filepath.Join("testdata", "live-longcontextinstances.json"), &instances)
	readJSON(t, filepath.Join("testdata", "live-longcontextpresets.json"), &presets)

	presetByKey := make(map[string]*vllmv1alpha1.LongContextPreset, len(presets))
	for i := range presets {
		p := &presets[i]
		presetByKey[p.Namespace+"/"+p.Name] = p
	}
	rendered := make(renderSet, len(instances))
	for i := range instances {
		instance := &instances[i]
		if instance.Spec.PresetRef == nil {
			t.Fatalf("%s/%s has no presetRef", instance.Namespace, instance.Name)
		}
		key := instance.Namespace + "/" + instance.Spec.PresetRef.Name
		preset := presetByKey[key]
		if preset == nil {
			t.Fatalf("%s/%s refers to missing LongContextPreset %q", instance.Namespace, instance.Name, key)
		}
		rendered[instance.Namespace+"/"+instance.Name] = renderLongContextInstance(t, instance, preset)
	}
	return rendered
}

func renderLongContextInstance(t *testing.T, instance *vllmv1alpha1.LongContextInstance, preset *vllmv1alpha1.LongContextPreset) renderArtifact {
	t.Helper()
	effective, _, err := vllm.ResolveLongContextInstance(&preset.Spec, instance.Spec)
	if err != nil {
		t.Fatalf("resolve %s/%s: %v", instance.Namespace, instance.Name, err)
	}
	if instance.Spec.PVCReadOnly != nil && (instance.Spec.Overrides == nil || instance.Spec.Overrides.PVCReadOnly == nil) {
		effective.PVCReadOnly = *instance.Spec.PVCReadOnly
	}
	// This harness renders exactly what the controller would render after
	// resolving the preset. Admission validation belongs to CRD tests; it must
	// not prevent an offline render comparison of a saved live object.
	replicas := int32(1)
	if instance.Spec.Replicas != nil {
		replicas = *instance.Spec.Replicas
	}
	apiKey := instance.Spec.APIKey
	if instance.Spec.Overrides != nil && instance.Spec.Overrides.APIKey != nil {
		apiKey = instance.Spec.Overrides.APIKey
	}
	owner := metav1.OwnerReference{
		APIVersion: vllmv1alpha1.GroupVersion.String(), Kind: "LongContextInstance",
		Name: instance.Name, UID: instance.UID, Controller: boolPtr(true), BlockOwnerDeletion: boolPtr(true),
	}
	return renderArtifact{
		Deployment: jsonObject(t, vllm.BuildDeployment(instance.Name, instance.Namespace, replicas, effective, instance.Spec.PVCName, instance.Spec.HFToken, apiKey, owner)),
		Service:    jsonObject(t, vllm.BuildService(instance.Name, instance.Namespace, instance.Spec.ServiceType, instance.Spec.NodePort, owner)),
	}
}

func jsonObject(t *testing.T, object any) map[string]any {
	t.Helper()
	b, err := json.Marshal(object)
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func boolPtr(v bool) *bool { return &v }

func dumpRenderSet(t *testing.T, got renderSet) {
	t.Helper()
	dir := os.Getenv("VLLM_RENDER_DUMP_DIR")
	if dir == "" {
		return
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	b, err := json.MarshalIndent(got, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "longcontext-render.json"), append(b, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readRenderSet(t *testing.T, path string) renderSet {
	t.Helper()
	var got renderSet
	readJSON(t, path, &got)
	return got
}

func readJSON(t *testing.T, path string, dst any) {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if err := json.Unmarshal(b, dst); err != nil {
		t.Fatalf("decode %s: %v", path, err)
	}
}

func assertRenderEquivalent(t *testing.T, want, got renderSet) {
	t.Helper()
	if !reflect.DeepEqual(want, got) {
		wantJSON, _ := json.MarshalIndent(want, "", "  ")
		gotJSON, _ := json.MarshalIndent(got, "", "  ")
		t.Fatalf("render outputs differ\n--- expected\n%s\n--- got\n%s", wantJSON, gotJSON)
	}
	// Full-object equality above is intentional.  These explicit projections
	// document #182's required compatibility contract and fail independently.
	for _, key := range sortedRenderKeys(got) {
		assertRequiredRenderFields(t, key, want[key], got[key])
	}
}

func assertRequiredRenderFields(t *testing.T, key string, want, got renderArtifact) {
	t.Helper()
	// The JSON equality is already stronger than these contract checks. Keep
	// the named check so a future narrowed comparator cannot omit them.
	for _, field := range []string{"args", "env", "resources", "volumes", "securityContext", "service"} {
		if !reflect.DeepEqual(renderField(t, want, field), renderField(t, got, field)) {
			t.Fatalf("%s %s differs\nwant=%#v\ngot=%#v", key, field, renderField(t, want, field), renderField(t, got, field))
		}
	}
}

func renderField(t *testing.T, a renderArtifact, field string) any {
	t.Helper()
	x := jsonObject(t, a)
	if field == "service" {
		return x["service"]
	}
	podSpec := renderObjectAt(t, x, "deployment", "spec", "template", "spec")
	switch field {
	case "volumes":
		return podSpec["volumes"]
	case "securityContext":
		contexts := map[string]any{"pod": podSpec["securityContext"]}
		for _, container := range renderContainers(t, podSpec) {
			name, ok := container["name"].(string)
			if !ok {
				t.Fatal("container name is not a string")
			}
			contexts[name] = container["securityContext"]
		}
		return contexts
	default:
		containers := renderContainers(t, podSpec)
		if len(containers) == 0 {
			t.Fatal("deployment has no containers")
		}
		return containers[0][field]
	}
}

func renderObjectAt(t *testing.T, object map[string]any, path ...string) map[string]any {
	t.Helper()
	for _, key := range path {
		next, ok := object[key].(map[string]any)
		if !ok {
			t.Fatalf("render path %v: %s is not an object", path, key)
		}
		object = next
	}
	return object
}

func renderContainers(t *testing.T, podSpec map[string]any) []map[string]any {
	t.Helper()
	raw, ok := podSpec["containers"].([]any)
	if !ok {
		t.Fatal("containers is not an array")
	}
	containers := make([]map[string]any, 0, len(raw))
	for _, item := range raw {
		container, ok := item.(map[string]any)
		if !ok {
			t.Fatal("container is not an object")
		}
		containers = append(containers, container)
	}
	return containers
}

func sortedRenderKeys(set renderSet) []string {
	keys := make([]string, 0, len(set))
	for key := range set {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func Example_renderDump() {
	fmt.Println("VLLM_RENDER_DUMP_DIR=/tmp/render go test ./internal/compatibility -run TestRenderLiveLongContextInstances")
	// Output:
	// VLLM_RENDER_DUMP_DIR=/tmp/render go test ./internal/compatibility -run TestRenderLiveLongContextInstances
}
