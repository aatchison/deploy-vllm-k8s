package vllm

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	v1alpha1 "github.com/aatchison/deploy-vllm-k8s/operator/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	yamlutil "k8s.io/apimachinery/pkg/util/yaml"
)

type securityExample179 struct {
	ID       string
	Kind     string            `json:"kind"`
	Metadata metav1.ObjectMeta `json:"metadata"`
	Spec     json.RawMessage   `json:"spec"`
}

// Scan YAML documents and YAML fences, including examples outside docs/examples.
func securityExamples179(t *testing.T) []securityExample179 {
	t.Helper()
	var examples []securityExample179
	fence := regexp.MustCompile("(?s)```(?:yaml|yml)\\n(.*?)```")
	err := filepath.WalkDir("../../..", func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if entry.Name() == ".git" || entry.Name() == "bin" {
				return filepath.SkipDir
			}
			return nil
		}
		var blocks []string
		switch filepath.Ext(path) {
		case ".yaml", ".yml", ".md":
		default:
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if filepath.Ext(path) == ".md" {
			for _, match := range fence.FindAllStringSubmatch(string(data), -1) {
				blocks = append(blocks, match[1])
			}
		} else {
			blocks = append(blocks, string(data))
		}
		for block, data := range blocks {
			decoder := yamlutil.NewYAMLOrJSONDecoder(strings.NewReader(data), 4096)
			for doc := 0; ; doc++ {
				var raw json.RawMessage
				if err := decoder.Decode(&raw); err == io.EOF {
					break
				} else if err != nil {
					return fmt.Errorf("%s: %w", path, err)
				}
				if len(raw) == 0 || raw[0] != '{' {
					continue
				}
				var ex securityExample179
				if err := json.Unmarshal(raw, &ex); err != nil {
					return err
				}
				switch ex.Kind {
				case "ModelPreset", "LongContextPreset", "VLLMInstance", "LongContextInstance":
				default:
					continue
				}
				ex.ID = fmt.Sprintf("%s#%d:%d", strings.TrimPrefix(filepath.ToSlash(path), "../../../"), block, doc)
				examples = append(examples, ex)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return examples
}

// These 35 baseline records were rendered on a600954 with no new fields set.
// New opt-in examples are tested separately, never used to refresh the baseline.
func TestSecurity179ExistingExamplesGolden(t *testing.T) {
	examples := securityExamples179(t)
	presets := map[string]securityExample179{}
	for _, ex := range examples {
		if strings.HasPrefix(ex.ID, "operator/config/samples/presets/") {
			presets[ex.Kind+"/"+ex.Metadata.Name] = ex
		}
	}
	var baseline map[string]json.RawMessage
	golden := "testdata/security_179_existing_examples.json"
	update := os.Getenv("UPDATE_SECURITY_GOLDEN179") == "1"
	if !update {
		data, err := os.ReadFile(golden)
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(data, &baseline); err != nil {
			t.Fatal(err)
		}
		if len(baseline) != 35 {
			t.Fatalf("baseline has %d examples, want 35", len(baseline))
		}
	}
	rendered := map[string]json.RawMessage{}
	for _, ex := range examples {
		if !update {
			if _, ok := baseline[ex.ID]; !ok {
				continue
			}
		}
		var e EffectiveConfig
		var err error
		pvc := "models"
		hf := corev1.SecretKeySelector{LocalObjectReference: corev1.LocalObjectReference{Name: "hf"}, Key: "token"}
		var apiKey *corev1.SecretKeySelector
		replicas := int32(1)
		switch ex.Kind {
		case "ModelPreset":
			var p v1alpha1.ModelPresetSpec
			if err := json.Unmarshal(ex.Spec, &p); err != nil {
				t.Fatal(err)
			}
			e, _, err = Resolve(&p, nil)
		case "LongContextPreset":
			var p v1alpha1.LongContextPresetSpec
			if err := json.Unmarshal(ex.Spec, &p); err != nil {
				t.Fatal(err)
			}
			e, _, err = ResolveLongContext(&p, nil)
		case "VLLMInstance":
			var s v1alpha1.VLLMInstanceSpec
			if err := json.Unmarshal(ex.Spec, &s); err != nil {
				t.Fatal(err)
			}
			var p *v1alpha1.ModelPresetSpec
			if s.PresetRef != nil {
				match, ok := presets["ModelPreset/"+s.PresetRef.Name]
				if !ok {
					t.Fatalf("%s: missing preset", ex.ID)
				}
				p = &v1alpha1.ModelPresetSpec{}
				if err := json.Unmarshal(match.Spec, p); err != nil {
					t.Fatal(err)
				}
			}
			// The README NodePort fragment is an override to its e2b example.
			if s.PresetRef == nil && s.Overrides == nil {
				match := presets["ModelPreset/gemma-4-e2b"]
				p = &v1alpha1.ModelPresetSpec{}
				if err := json.Unmarshal(match.Spec, p); err != nil {
					t.Fatal(err)
				}
			}
			e, _, err = Resolve(p, s.Overrides)
			if s.PVCReadOnly != nil && (s.Overrides == nil || s.Overrides.PVCReadOnly == nil) {
				e.PVCReadOnly = *s.PVCReadOnly
			}
			pvc, hf, apiKey = s.PVCName, s.HFToken, s.APIKey
			if s.Overrides != nil && s.Overrides.APIKey != nil {
				apiKey = s.Overrides.APIKey
			}
			if s.Replicas != nil {
				replicas = *s.Replicas
			}
		case "LongContextInstance":
			var s v1alpha1.LongContextInstanceSpec
			if err := json.Unmarshal(ex.Spec, &s); err != nil {
				t.Fatal(err)
			}
			var p *v1alpha1.LongContextPresetSpec
			if s.PresetRef != nil {
				match, ok := presets["LongContextPreset/"+s.PresetRef.Name]
				if !ok {
					t.Fatalf("%s: missing preset", ex.ID)
				}
				p = &v1alpha1.LongContextPresetSpec{}
				if err := json.Unmarshal(match.Spec, p); err != nil {
					t.Fatal(err)
				}
			}
			e, _, err = ResolveLongContext(p, s.Overrides)
			if s.PVCReadOnly != nil && (s.Overrides == nil || s.Overrides.PVCReadOnly == nil) {
				e.PVCReadOnly = *s.PVCReadOnly
			}
			pvc, hf, apiKey = s.PVCName, s.HFToken, s.APIKey
			if s.Overrides != nil && s.Overrides.APIKey != nil {
				apiKey = s.Overrides.APIKey
			}
			if s.Replicas != nil {
				replicas = *s.Replicas
			}
		}
		if err != nil {
			t.Fatalf("%s: %v", ex.ID, err)
		}
		dep := BuildDeployment(ex.Metadata.Name, ex.Metadata.Namespace, replicas, e, pvc, hf, apiKey, metav1.OwnerReference{})
		// Comparing the full Deployment also compares the container argv byte-for-byte.
		data, err := json.Marshal(dep)
		if err != nil {
			t.Fatal(err)
		}
		rendered[ex.ID] = data
		var compact bytes.Buffer
		if !update {
			if err := json.Compact(&compact, baseline[ex.ID]); err != nil {
				t.Fatal(err)
			}
		}
		if !update && !bytes.Equal(data, compact.Bytes()) {
			t.Errorf("%s: Deployment differs from a600954", ex.ID)
		}
	}
	if update {
		if err := os.MkdirAll("testdata", 0755); err != nil {
			t.Fatal(err)
		}
		data, err := json.MarshalIndent(rendered, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(golden, append(data, '\n'), 0644); err != nil {
			t.Fatal(err)
		}
	}
	if len(rendered) != 35 {
		t.Fatalf("rendered %d baseline records, want 35", len(rendered))
	}
	t.Logf("verified %d existing examples", len(rendered))
}

func securityConfig179(t *testing.T, profile, engram string) EffectiveConfig {
	t.Helper()
	e := baseEffectiveConfig()
	data, err := json.Marshal(e)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]interface{}
	if err := json.Unmarshal(data, &fields); err != nil {
		t.Fatal(err)
	}
	fields["securityProfile"], fields["engramConfig"] = profile, engram
	data, err = json.Marshal(fields)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &e); err != nil {
		t.Fatal(err)
	}
	return e
}

func TestSecurity179ConfigValidation(t *testing.T) {
	for _, tc := range []struct {
		profile, engram string
		valid           bool
	}{
		{"", "", true}, {"default", "", true}, {"engram-ipc", `{"cpu_offload":true}`, true},
		{"engram-ipc", "", false}, {"engram-ipc", "null", false},
		{"engram-ipc", "[]", false}, {"engram-ipc", "bad", false},
		{"root", `{"cpu_offload":true}`, false},
	} {
		t.Run(tc.profile+"/"+tc.engram, func(t *testing.T) {
			err := ValidateEffectiveConfig(securityConfig179(t, tc.profile, tc.engram))
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%v err=%v", tc.valid, err)
			}
		})
	}
}

func TestSecurity179ResolveProfile(t *testing.T) {
	var preset v1alpha1.LongContextPresetSpec
	if err := json.Unmarshal([]byte(`{"securityProfile":"engram-ipc","engramConfig":"{\"cpu_offload\":true}"}`), &preset); err != nil {
		t.Fatal(err)
	}
	e, h, err := ResolveLongContext(&preset, nil)
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(e)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(data, []byte(`"securityProfile":"engram-ipc"`)) {
		t.Fatalf("profile dropped: %s", data)
	}
	var overrides v1alpha1.LongContextOverrides
	if err := json.Unmarshal([]byte(`{"securityProfile":"default"}`), &overrides); err != nil {
		t.Fatal(err)
	}
	def, dh, err := ResolveLongContext(&preset, &overrides)
	if err != nil {
		t.Fatal(err)
	}
	if h == dh {
		t.Fatal("profile must participate in config hash")
	}
	var empty v1alpha1.LongContextOverrides
	if err := json.Unmarshal([]byte(`{"engramConfig":""}`), &empty); err != nil {
		t.Fatal(err)
	}
	invalid, _, err := ResolveLongContext(&preset, &empty)
	if err != nil {
		t.Fatal(err)
	}
	if ValidateEffectiveConfig(invalid) == nil {
		t.Fatal("clearing inherited engramConfig must refuse engram-ipc")
	}
	data, err = json.Marshal(def)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(data, []byte(`"securityProfile"`)) {
		t.Fatalf("default must preserve config hash shape: %s", data)
	}
}

// vLLM v0.31.0 PLE uses process-local pinned host memory and UVA. No extra
// capabilities, UID change or seccomp relaxation is justified by this path.
func TestSecurity179ExactlyProvenSecuritySet(t *testing.T) {
	baseline := buildTestDeployment(baseEffectiveConfig())
	for _, profile := range []string{"default", "engram-ipc"} {
		e := securityConfig179(t, profile, `{"cpu_offload":true}`)
		dep := buildTestDeployment(e)
		before, _ := json.Marshal(baseline.Spec.Template.Spec.SecurityContext)
		after, _ := json.Marshal(dep.Spec.Template.Spec.SecurityContext)
		if !bytes.Equal(before, after) {
			t.Fatalf("%s: unexpected pod security mutation", profile)
		}
		before, _ = json.Marshal(baseline.Spec.Template.Spec.Containers[0].SecurityContext)
		after, _ = json.Marshal(dep.Spec.Template.Spec.Containers[0].SecurityContext)
		if !bytes.Equal(before, after) {
			t.Fatalf("%s: unexpected container security mutation", profile)
		}
		if dep.Spec.Template.Spec.HostPID || dep.Spec.Template.Spec.HostIPC || dep.Spec.Template.Spec.HostNetwork || dep.Spec.Template.Spec.ShareProcessNamespace != nil {
			t.Fatal("process/network isolation changed")
		}
	}
}

func TestSecurity179ExplicitDefaultByteIdentical(t *testing.T) {
	var preset v1alpha1.LongContextPresetSpec
	if err := json.Unmarshal([]byte(`{"modelID":"m","migResource":"nvidia.com/mig-4g.96gb","shmSizeLimit":"8Gi"}`), &preset); err != nil {
		t.Fatal(err)
	}
	before, bh, err := ResolveLongContext(&preset, nil)
	if err != nil {
		t.Fatal(err)
	}
	preset.SecurityProfile = v1alpha1.SecurityProfileDefault
	after, ah, err := ResolveLongContext(&preset, nil)
	if err != nil {
		t.Fatal(err)
	}
	a, err := json.Marshal(buildTestDeployment(before))
	if err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(buildTestDeployment(after))
	if err != nil {
		t.Fatal(err)
	}
	if bh != ah || !bytes.Equal(a, b) {
		t.Fatal("explicit default changes hash or Deployment")
	}
	def := v1alpha1.SecurityProfileDefault
	after, ah, err = ResolveLongContext(nil, &v1alpha1.LongContextOverrides{ModelID: &preset.ModelID, MIGResource: &preset.MIGResource, SHMSizeLimit: &preset.SHMSizeLimit, SecurityProfile: &def})
	if err != nil {
		t.Fatal(err)
	}
	b, err = json.Marshal(buildTestDeployment(after))
	if err != nil {
		t.Fatal(err)
	}
	if bh != ah || !bytes.Equal(a, b) {
		t.Fatal("default override changes hash or Deployment")
	}
}

func TestSecurity179TypedEngramArgumentPreservesBytes(t *testing.T) {
	config := `{ "cpu_offload": true, "dp_shared_memory": false }`
	e := securityConfig179(t, "engram-ipc", config)
	args := buildArgs(e)
	found := 0
	for i, arg := range args {
		if arg == "--engram-config" {
			found++
			if i+1 >= len(args) || args[i+1] != config {
				t.Fatalf("config bytes changed: %v", args)
			}
		}
	}
	if found != 1 {
		t.Fatalf("engram flag count=%d", found)
	}
}
