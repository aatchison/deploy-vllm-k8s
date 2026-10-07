package vllm

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestLoraPresetArgs(t *testing.T) {
	const modules = "adapter=/models/lora adapter"
	for _, kind := range []string{"model", "long-context"} {
		for _, state := range []string{"enabled", "enabled-only", "unset", "unset-with-rank", "false", "disabled-with-options"} {
			t.Run(kind+"/"+state, func(t *testing.T) {
				raw := `{}`
				switch state {
				case "enabled":
					raw = `{"enableLora":true,"loraModules":"` + modules + `","maxLoraRank":32}`
				case "enabled-only":
					raw = `{"enableLora":true}`
				case "unset-with-rank":
					raw = `{"maxLoraRank":64}`
				case "false":
					raw = `{"enableLora":false}`
				case "disabled-with-options":
					raw = `{"enableLora":false,"loraModules":"` + modules + `","maxLoraRank":64}`
				}
				var e EffectiveConfig
				var err error
				if kind == "model" {
					p := basePreset()
					if err := json.Unmarshal([]byte(raw), p); err != nil {
						t.Fatal(err)
					}
					e, _, err = Resolve(p, nil)
				} else {
					p := baseLongContextPreset()
					if err := json.Unmarshal([]byte(raw), p); err != nil {
						t.Fatal(err)
					}
					e, _, err = ResolveLongContext(p, nil)
				}
				if err != nil {
					t.Fatal(err)
				}
				args := buildArgs(e)
				var got []string
				for i, a := range args {
					switch a {
					case "--enable-lora":
						got = append(got, a)
						if i+1 < len(args) && !strings.HasPrefix(args[i+1], "--") {
							t.Errorf("bare flag has a value: %v", args)
						}
					case "--lora-modules", "--max-lora-rank":
						if i+1 >= len(args) {
							t.Fatalf("missing value: %v", args)
						}
						got = append(got, a, args[i+1])
					}
				}
				var want []string
				if state == "enabled" {
					want = []string{"--enable-lora", "--lora-modules", modules, "--max-lora-rank", "32"}
				}
				if state == "enabled-only" {
					want = []string{"--enable-lora"}
				}
				if !reflect.DeepEqual(got, want) {
					t.Fatalf("LoRA args = %q, want %q; all args: %q", got, want, args)
				}
				dep := buildTestDeployment(e)
				for _, c := range dep.Spec.Template.Spec.Containers {
					if c.Name == ContainerName && !reflect.DeepEqual(c.Args, args) {
						t.Errorf("deployment args differ: %q", c.Args)
					}
				}
			})
		}
	}
}

func TestLoraPresetBoolNoAliasing(t *testing.T) {
	for _, kind := range []string{"model", "long-context"} {
		t.Run(kind, func(t *testing.T) {
			enabled := true
			var e EffectiveConfig
			var err error
			if kind == "model" {
				p := basePreset()
				p.EnableLora = &enabled
				e, _, err = Resolve(p, nil)
			} else {
				p := baseLongContextPreset()
				p.EnableLora = &enabled
				e, _, err = ResolveLongContext(p, nil)
			}
			if err != nil {
				t.Fatal(err)
			}
			enabled = false
			if e.EnableLora == nil || !*e.EnableLora {
				t.Fatal("resolved LoRA flag aliases preset")
			}
		})
	}
}
