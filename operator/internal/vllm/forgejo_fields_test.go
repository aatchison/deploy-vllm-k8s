package vllm

import (
	"encoding/json"
	"testing"

	api "github.com/aatchison/deploy-vllm-k8s/operator/api/v1alpha1"
)

func TestPortedStringFieldWiring(t *testing.T) {
	for _, tc := range []struct {
		key, flag       string
		model, override bool
	}{
		{"reasoningParser", "--reasoning-parser", true, true},
		{"chatTemplate", "--chat-template", true, true},
		{"speculativeConfig", "--speculative-config", false, true},
		{"limitMmPerPrompt", "--limit-mm-per-prompt", false, false},
		{"kvCacheDtypeSkipLayers", "--kv-cache-dtype-skip-layers", false, false},
		{"mambaCacheMode", "--mamba-cache-mode", false, true},
		{"mambaBackend", "--mamba-backend", false, true},
		{"compilationConfig", "--compilation-config", false, true},
	} {
		t.Run(tc.key, func(t *testing.T) {
			raw, err := json.Marshal(map[string]string{tc.key: "verbatim value"})
			if err != nil {
				t.Fatal(err)
			}
			var p api.LongContextPresetSpec
			if err := json.Unmarshal(raw, &p); err != nil {
				t.Fatal(err)
			}
			e, _, err := ResolveLongContext(&p, nil)
			if err != nil {
				t.Fatal(err)
			}
			assertPortFlag(t, buildArgs(e), tc.flag, "verbatim value", true)
			if tc.override {
				var o api.LongContextOverrides
				if err := json.Unmarshal([]byte(`{"`+tc.key+`":""}`), &o); err != nil {
					t.Fatal(err)
				}
				e, _, err = ResolveLongContext(&p, &o)
				if err != nil {
					t.Fatal(err)
				}
				assertPortFlag(t, buildArgs(e), tc.flag, "", false)
			}
			if tc.model {
				var p api.ModelPresetSpec
				if err := json.Unmarshal(raw, &p); err != nil {
					t.Fatal(err)
				}
				e, _, err := Resolve(&p, nil)
				if err != nil {
					t.Fatal(err)
				}
				assertPortFlag(t, buildArgs(e), tc.flag, "verbatim value", true)
				var o api.ModelConfigOverrides
				if err := json.Unmarshal([]byte(`{"`+tc.key+`":""}`), &o); err != nil {
					t.Fatal(err)
				}
				e, _, err = Resolve(&p, &o)
				if err != nil {
					t.Fatal(err)
				}
				assertPortFlag(t, buildArgs(e), tc.flag, "", false)
			}
		})
	}
}

func assertPortFlag(t *testing.T, args []string, flag, value string, present bool) {
	t.Helper()
	count := 0
	for i, arg := range args {
		if arg == flag {
			count++
			if !present {
				t.Fatalf("cleared %s still rendered: %q", flag, args)
			}
			if i+1 == len(args) || args[i+1] != value {
				t.Fatalf("%s value not preserved: %q", flag, args)
			}
		}
	}
	if present && count != 1 {
		t.Fatalf("%s occurred %d times: %q", flag, count, args)
	}
}
