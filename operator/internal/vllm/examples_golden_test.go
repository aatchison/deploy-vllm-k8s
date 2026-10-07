package vllm

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"testing"

	v1 "github.com/aatchison/deploy-vllm-k8s/operator/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	yamlutil "k8s.io/apimachinery/pkg/util/yaml"
)

// Capture full renders and hashes for every sample, including each document in
// multi-instance YAML files. Presets also render independently of their consumers.
func TestExistingExamplesGolden(t *testing.T) {
	paths, err := filepath.Glob("../../config/samples/*/*.yaml")
	if err != nil || len(paths) == 0 {
		t.Fatalf("samples: %v", err)
	}
	presets := map[string]v1.ModelPreset{}
	longPresets := map[string]v1.LongContextPreset{}
	documents := map[string]json.RawMessage{}
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		decoder := yamlutil.NewYAMLOrJSONDecoder(bytes.NewReader(data), 4096)
		for i := 0; ; i++ {
			var raw json.RawMessage
			err := decoder.Decode(&raw)
			if err == io.EOF {
				break
			}
			if err != nil {
				t.Fatal(err)
			}
			if len(raw) == 0 || string(raw) == "null" {
				continue
			}
			documents[filepath.ToSlash(path)+"#"+string(rune('0'+i))] = raw
			var meta metav1.TypeMeta
			if err := json.Unmarshal(raw, &meta); err != nil {
				t.Fatal(err)
			}
			switch meta.Kind {
			case "ModelPreset":
				var p v1.ModelPreset
				if err := json.Unmarshal(raw, &p); err != nil {
					t.Fatal(err)
				}
				presets[p.Namespace+"/"+p.Name] = p
			case "LongContextPreset":
				var p v1.LongContextPreset
				if err := json.Unmarshal(raw, &p); err != nil {
					t.Fatal(err)
				}
				longPresets[p.Namespace+"/"+p.Name] = p
			}
		}
	}
	got := map[string]any{}
	for key, raw := range documents {
		var meta metav1.TypeMeta
		if err := json.Unmarshal(raw, &meta); err != nil {
			t.Fatal(err)
		}
		var e EffectiveConfig
		var hash string
		var err error
		name, namespace, pvc := "preset", "vllm", "models"
		token := corev1.SecretKeySelector{LocalObjectReference: corev1.LocalObjectReference{Name: "hf"}, Key: "token"}
		var apiKey *corev1.SecretKeySelector
		var nodePort *int32
		var serviceType corev1.ServiceType
		replicas := int32(1)
		switch meta.Kind {
		case "ModelPreset":
			var p v1.ModelPreset
			if err := json.Unmarshal(raw, &p); err != nil {
				t.Fatal(err)
			}
			e, hash, err = Resolve(&p.Spec, nil)
		case "LongContextPreset":
			var p v1.LongContextPreset
			if err := json.Unmarshal(raw, &p); err != nil {
				t.Fatal(err)
			}
			e, hash, err = ResolveLongContext(&p.Spec, nil)
		case "VLLMInstance":
			var inst v1.VLLMInstance
			if err := json.Unmarshal(raw, &inst); err != nil {
				t.Fatal(err)
			}
			p, ok := presets[inst.Namespace+"/"+inst.Spec.PresetRef.Name]
			if !ok {
				t.Fatalf("missing preset for %s", key)
			}
			e, hash, err = Resolve(&p.Spec, inst.Spec.Overrides)
			name, namespace, pvc, token = inst.Name, inst.Namespace, inst.Spec.PVCName, inst.Spec.HFToken
			apiKey, nodePort, serviceType = inst.Spec.APIKey, inst.Spec.NodePort, inst.Spec.ServiceType
			if inst.Spec.Replicas != nil {
				replicas = *inst.Spec.Replicas
			}
			if inst.Spec.PVCReadOnly != nil {
				e.PVCReadOnly = *inst.Spec.PVCReadOnly
			}
			if inst.Spec.Overrides != nil {
				if inst.Spec.Overrides.APIKey != nil {
					apiKey = inst.Spec.Overrides.APIKey
				}
				if inst.Spec.Overrides.PVCReadOnly != nil {
					e.PVCReadOnly = *inst.Spec.Overrides.PVCReadOnly
				}
			}
		case "LongContextInstance":
			var inst v1.LongContextInstance
			if err := json.Unmarshal(raw, &inst); err != nil {
				t.Fatal(err)
			}
			p, ok := longPresets[inst.Namespace+"/"+inst.Spec.PresetRef.Name]
			if !ok {
				t.Fatalf("missing preset for %s", key)
			}
			e, hash, err = ResolveLongContextInstance(&p.Spec, inst.Spec)
			name, namespace, pvc, token = inst.Name, inst.Namespace, inst.Spec.PVCName, inst.Spec.HFToken
			apiKey, nodePort, serviceType = inst.Spec.APIKey, inst.Spec.NodePort, inst.Spec.ServiceType
			if inst.Spec.Replicas != nil {
				replicas = *inst.Spec.Replicas
			}
			if inst.Spec.PVCReadOnly != nil {
				e.PVCReadOnly = *inst.Spec.PVCReadOnly
			}
			if inst.Spec.Overrides != nil {
				if inst.Spec.Overrides.APIKey != nil {
					apiKey = inst.Spec.Overrides.APIKey
				}
				if inst.Spec.Overrides.PVCReadOnly != nil {
					e.PVCReadOnly = *inst.Spec.Overrides.PVCReadOnly
				}
			}
		default:
			t.Fatalf("uncovered kind %s", meta.Kind)
		}
		if err != nil {
			t.Fatal(err)
		}
		got[key] = map[string]any{"hash": hash, "deployment": BuildDeployment(name, namespace, replicas, e, pvc, token, apiKey, metav1.OwnerReference{}), "service": BuildService(name, namespace, serviceType, nodePort, metav1.OwnerReference{})}
	}
	encoded, err := json.MarshalIndent(got, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	encoded = append(encoded, '\n')
	path := "testdata/existing-examples.json"
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(encoded, want) {
		t.Fatal("existing example render/hash changed; compare testdata/existing-examples.json")
	}
	t.Logf("checked %d YAML documents from %d example files", len(documents), len(paths))
}
