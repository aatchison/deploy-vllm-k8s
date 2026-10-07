package vllm

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"testing"

	api "github.com/aatchison/deploy-vllm-k8s/operator/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	yamlutil "k8s.io/apimachinery/pkg/util/yaml"
)

// TestFlags178ExamplesGolden compares complete Deployments (including argv)
// and resolved hashes with snapshots recorded before implementation at a600954.
// It scans YAML files and YAML fences in documentation, not a hand-picked list.
func TestFlags178ExamplesGolden(t *testing.T) {
	type example struct {
		key, kind, name, namespace string
		data                       []byte
	}
	root := filepath.Join("..", "..", "..")
	var examples []example
	fences := regexp.MustCompile("(?ms)^```ya?ml[^\\n]*\\n(.*?)^```")
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == ".git" || d.Name() == "bin" {
				return filepath.SkipDir
			}
			return nil
		}
		ext := filepath.Ext(path)
		if ext != ".yaml" && ext != ".yml" && ext != ".md" {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		blocks := [][]byte{b}
		if ext == ".md" {
			blocks = nil
			for _, m := range fences.FindAllSubmatch(b, -1) {
				blocks = append(blocks, m[1])
			}
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		for bi, block := range blocks {
			decoder := yamlutil.NewYAMLOrJSONDecoder(bytes.NewReader(block), 4096)
			for di := 0; ; di++ {
				var raw json.RawMessage
				err := decoder.Decode(&raw)
				if err == io.EOF {
					break
				}
				if err != nil {
					return fmt.Errorf("%s block %d: %w", rel, bi, err)
				}
				if len(raw) == 0 || raw[0] != '{' {
					continue // Non-resource YAML, such as CI lists and scalar docs.
				}
				var obj struct {
					Kind     string
					Metadata metav1.ObjectMeta
					Spec     json.RawMessage
				}
				if err := json.Unmarshal(raw, &obj); err != nil {
					return err
				}
				switch obj.Kind {
				case "ModelPreset", "LongContextPreset", "VLLMInstance", "LongContextInstance":
					examples = append(examples, example{fmt.Sprintf("%s#%d:%d", filepath.ToSlash(rel), bi, di), obj.Kind, obj.Metadata.Name, obj.Metadata.Namespace, raw})
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(examples) == 0 {
		t.Fatal("no examples found")
	}
	presets := map[string]api.ModelPresetSpec{}
	longPresets := map[string]api.LongContextPresetSpec{}
	for _, x := range examples {
		key := x.namespace + "/" + x.name
		if x.kind == "ModelPreset" {
			var p api.ModelPreset
			if err := json.Unmarshal(x.data, &p); err != nil {
				t.Fatal(err)
			}
			presets[key] = p.Spec
		}
		if x.kind == "LongContextPreset" {
			var p api.LongContextPreset
			if err := json.Unmarshal(x.data, &p); err != nil {
				t.Fatal(err)
			}
			longPresets[key] = p.Spec
		}
	}
	actual := map[string]json.RawMessage{}
	for _, x := range examples {
		// Newly documented opt-in examples are not part of the legacy corpus.
		var obj map[string]interface{}
		if err := json.Unmarshal(x.data, &obj); err != nil {
			t.Fatal(err)
		}
		spec, ok := obj["spec"].(map[string]interface{})
		if !ok {
			t.Fatalf("%s has no spec object", x.key)
		}
		optIn := spec
		if o, ok := spec["overrides"].(map[string]interface{}); ok {
			optIn = o
		}
		hasNew := false
		for _, f := range []string{"modelRevision", "codeRevision", "tokenizerRevision", "trustRemoteCode", "engramConfig", "disableCustomAllReduce", "flashinferAutotune", "maxNumSeqs", "reasoningParser", "chatTemplate", "compilationConfig", "speculativeConfig"} {
			if _, ok := optIn[f]; ok {
				hasNew = true
			}
		}
		if hasNew {
			continue
		}
		var e EffectiveConfig
		var hash string
		var err error
		pvc := "models"
		token := corev1.SecretKeySelector{}
		var apiKey *corev1.SecretKeySelector
		replicas := int32(1)
		switch x.kind {
		case "ModelPreset":
			p := presets[x.namespace+"/"+x.name]
			e, hash, err = Resolve(&p, nil)
		case "LongContextPreset":
			p := longPresets[x.namespace+"/"+x.name]
			e, hash, err = ResolveLongContext(&p, nil)
		case "VLLMInstance":
			var i api.VLLMInstance
			if err := json.Unmarshal(x.data, &i); err != nil {
				t.Fatal(err)
			}
			var p *api.ModelPresetSpec
			if i.Spec.PresetRef != nil {
				v, ok := presets[x.namespace+"/"+i.Spec.PresetRef.Name]
				// Tenant docs assume the shipped preset is copied into the tenant namespace.
				if !ok {
					v, ok = presets["vllm/"+i.Spec.PresetRef.Name]
				}
				if !ok {
					t.Fatalf("%s missing preset %s", x.key, i.Spec.PresetRef.Name)
				}
				p = &v
			}
			e, hash, err = Resolve(p, i.Spec.Overrides)
			pvc = i.Spec.PVCName
			token = i.Spec.HFToken
			apiKey = i.Spec.APIKey
			if i.Spec.Overrides != nil && i.Spec.Overrides.APIKey != nil {
				apiKey = i.Spec.Overrides.APIKey
			}
			if i.Spec.Replicas != nil {
				replicas = *i.Spec.Replicas
			}
			if i.Spec.PVCReadOnly != nil && (i.Spec.Overrides == nil || i.Spec.Overrides.PVCReadOnly == nil) {
				e.PVCReadOnly = *i.Spec.PVCReadOnly
				hash, err = HashConfig(e)
			}
		case "LongContextInstance":
			var i api.LongContextInstance
			if err := json.Unmarshal(x.data, &i); err != nil {
				t.Fatal(err)
			}
			var p *api.LongContextPresetSpec
			if i.Spec.PresetRef != nil {
				v, ok := longPresets[x.namespace+"/"+i.Spec.PresetRef.Name]
				// Tenant docs assume the shipped preset is copied into the tenant namespace.
				if !ok {
					v, ok = longPresets["vllm/"+i.Spec.PresetRef.Name]
				}
				if !ok {
					t.Fatalf("%s missing preset %s", x.key, i.Spec.PresetRef.Name)
				}
				p = &v
			}
			e, hash, err = ResolveLongContext(p, i.Spec.Overrides)
			pvc = i.Spec.PVCName
			token = i.Spec.HFToken
			apiKey = i.Spec.APIKey
			if i.Spec.Overrides != nil && i.Spec.Overrides.APIKey != nil {
				apiKey = i.Spec.Overrides.APIKey
			}
			if i.Spec.Replicas != nil {
				replicas = *i.Spec.Replicas
			}
			if i.Spec.PVCReadOnly != nil && (i.Spec.Overrides == nil || i.Spec.Overrides.PVCReadOnly == nil) {
				e.PVCReadOnly = *i.Spec.PVCReadOnly
				hash, err = HashConfig(e)
			}
		}
		if err != nil {
			t.Fatal(err)
		}
		if err := ValidateEffectiveConfig(e); err != nil {
			t.Fatalf("%s: %v", x.key, err)
		}
		dep := BuildDeployment(x.name, x.namespace, replicas, e, pvc, token, apiKey, metav1.OwnerReference{})
		b, err := json.Marshal(struct {
			Hash       string
			Deployment interface{}
		}{hash, dep})
		if err != nil {
			t.Fatal(err)
		}
		actual[x.key] = b
	}
	golden := filepath.Join("testdata", "flags_178_examples.json")
	if os.Getenv("UPDATE_FLAGS_178_GOLDEN") == "1" {
		b, err := json.MarshalIndent(actual, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Dir(golden), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(golden, append(b, '\n'), 0644); err != nil {
			t.Fatal(err)
		}
	}
	b, err := os.ReadFile(golden)
	if err != nil {
		t.Fatal(err)
	}
	var expected map[string]json.RawMessage
	if err := json.Unmarshal(b, &expected); err != nil {
		t.Fatal(err)
	}
	if len(actual) != len(expected) {
		t.Fatalf("example count changed: got %d want %d", len(actual), len(expected))
	}
	for key, want := range expected {
		got, ok := actual[key]
		if !ok {
			t.Errorf("missing legacy example %s", key)
			continue
		}
		var compact bytes.Buffer
		if err := json.Compact(&compact, want); err != nil {
			t.Fatal(err)
		}
		if string(got) != compact.String() {
			t.Errorf("deployment/hash changed for %s", key)
		}
	}
	t.Logf("%d existing preset/instance examples unchanged", len(actual))
}
