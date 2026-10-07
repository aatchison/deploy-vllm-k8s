package vllm

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	api "github.com/aatchison/deploy-vllm-k8s/operator/api/v1alpha1"
)

func flag178Config(t *testing.T, preset, overrides map[string]interface{}) EffectiveConfig {
	t.Helper()
	p := &api.LongContextPresetSpec{ModelID: "model", SHMSizeLimit: "8Gi", MIGResource: "nvidia.com/mig-4g.96gb", MIGResourceCount: 1}
	b, err := json.Marshal(preset)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, p); err != nil {
		t.Fatal(err)
	}
	var o *api.LongContextOverrides
	if overrides != nil {
		o = &api.LongContextOverrides{}
		b, err = json.Marshal(overrides)
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(b, o); err != nil {
			t.Fatal(err)
		}
	}
	e, _, err := ResolveLongContext(p, o)
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func TestFlags178RenderAndOverride(t *testing.T) {
	sha := strings.Repeat("a", 40)
	tests := []struct {
		field, flag        string
		value, override    interface{}
		want, overrideWant []string
	}{
		{"modelRevision", "--revision", sha, strings.Repeat("b", 40), []string{"--revision", sha}, []string{"--revision", strings.Repeat("b", 40)}},
		{"codeRevision", "--code-revision", sha, strings.Repeat("b", 40), []string{"--code-revision", sha}, []string{"--code-revision", strings.Repeat("b", 40)}},
		{"tokenizerRevision", "--tokenizer-revision", sha, strings.Repeat("b", 40), []string{"--tokenizer-revision", sha}, []string{"--tokenizer-revision", strings.Repeat("b", 40)}},
		{"trustRemoteCode", "--trust-remote-code", true, false, []string{"--trust-remote-code"}, nil},
		{"disableCustomAllReduce", "--disable-custom-all-reduce", true, false, []string{"--disable-custom-all-reduce"}, nil},
		{"flashinferAutotune", "--enable-flashinfer-autotune", true, false, []string{"--enable-flashinfer-autotune"}, []string{"--no-enable-flashinfer-autotune"}},
		{"engramConfig", "--engram-config", ` {"nested":{"a":[1,true,null]},"cpu_offload_gb":32} `, `{}`, []string{"--engram-config", ` {"nested":{"a":[1,true,null]},"cpu_offload_gb":32} `}, []string{"--engram-config", `{}`}},
		{"maxNumSeqs", "--max-num-seqs", 32, 1, []string{"--max-num-seqs", "32"}, []string{"--max-num-seqs", "1"}},
		{"reasoningParser", "--reasoning-parser", "qwen3", "other", []string{"--reasoning-parser", "qwen3"}, []string{"--reasoning-parser", "other"}},
		{"chatTemplate", "--chat-template", "/models/chat.jinja", "/models/other.jinja", []string{"--chat-template", "/models/chat.jinja"}, []string{"--chat-template", "/models/other.jinja"}},
		{"compilationConfig", "--compilation-config", `{"cudagraph_capture_sizes":[1,2,4]}`, `{"cudagraph_capture_sizes":[1]}`, []string{"--compilation-config", `{"cudagraph_capture_sizes":[1,2,4]}`}, []string{"--compilation-config", `{"cudagraph_capture_sizes":[1]}`}},
		{"speculativeConfig", "--speculative-config", ` {"method":"mtp","num_speculative_tokens":3} `, `{"method":"mtp","num_speculative_tokens":1}`, []string{"--speculative-config", ` {"method":"mtp","num_speculative_tokens":3} `}, []string{"--speculative-config", `{"method":"mtp","num_speculative_tokens":1}`}},
	}
	baseline := buildArgs(flag178Config(t, nil, nil))
	for _, tc := range tests {
		t.Run(tc.field, func(t *testing.T) {
			for _, mode := range []string{"preset", "override", "override-only"} {
				t.Run(mode, func(t *testing.T) {
					p := map[string]interface{}{tc.field: tc.value}
					var o map[string]interface{}
					want := tc.want
					if mode != "preset" {
						o = map[string]interface{}{tc.field: tc.override}
						want = tc.overrideWant
					}
					if mode == "override-only" {
						p = nil
					}
					e := flag178Config(t, p, o)
					if err := ValidateEffectiveConfig(e); err != nil {
						t.Fatal(err)
					}
					got := buildArgs(e)
					expected := append(append([]string{}, baseline...), want...)
					if !reflect.DeepEqual(got, expected) {
						t.Fatalf("args = %q; want %q", got, expected)
					}
				})
			}
		})
	}
}

func TestFlags178Validation(t *testing.T) {
	for _, field := range []string{"modelRevision", "codeRevision", "tokenizerRevision"} {
		for _, value := range []string{"main", "", "abc", strings.Repeat("g", 40), strings.Repeat("A", 40), strings.Repeat("a", 41), strings.Repeat("a", 39)} {
			e := flag178Config(t, map[string]interface{}{field: value}, nil)
			err := ValidateEffectiveConfig(e)
			valid := value == "" || value == strings.Repeat("A", 40)
			if valid != (err == nil) {
				t.Errorf("%s=%q valid=%v error=%v", field, value, valid, err)
			}
		}
	}
	for _, field := range []string{"engramConfig"} {
		for _, value := range []string{"", `{}`, " \n{\"x\": [1,true,null,{\"a\":\"b\"}]}\t", `[]`, `null`, `1`, `"object"`, `{bad}`, `{"x":}`, `{} {}`, `{"x":NaN}`} {
			e := flag178Config(t, map[string]interface{}{field: value}, nil)
			err := ValidateEffectiveConfig(e)
			valid := value == "" || value == `{}` || strings.HasPrefix(value, " \n")
			if valid != (err == nil) {
				t.Errorf("%s=%q valid=%v error=%v", field, value, valid, err)
			}
		}
	}

}

func TestFlags178ClearAndHash(t *testing.T) {
	for _, field := range []string{"engramConfig", "compilationConfig", "speculativeConfig", "reasoningParser", "chatTemplate"} {
		e := flag178Config(t, map[string]interface{}{field: `{}`}, map[string]interface{}{field: ""})
		baseline := flag178Config(t, nil, nil)
		if !reflect.DeepEqual(buildArgs(e), buildArgs(baseline)) {
			t.Fatalf("%s override did not clear flag", field)
		}
	}
	baseline := flag178Config(t, nil, nil)
	baseHash, err := HashConfig(baseline)
	if err != nil {
		t.Fatal(err)
	}
	e := flag178Config(t, map[string]interface{}{"flashinferAutotune": false}, nil)
	h, err := HashConfig(e)
	if err != nil {
		t.Fatal(err)
	}
	if h == baseHash {
		t.Fatal("explicit false autotune did not change hash")
	}
	if got := buildArgs(e); got[len(got)-1] != "--no-enable-flashinfer-autotune" {
		t.Fatalf("false autotune args=%q", got)
	}
}

func TestFlags178PointerIsolationAndReverseOverride(t *testing.T) {
	p := &api.LongContextPresetSpec{FlashinferAutotune: boolPtr(false), MaxNumSeqs: 32}
	o := &api.LongContextOverrides{FlashinferAutotune: boolPtr(true)}
	e, _, err := ResolveLongContext(p, o)
	if err != nil {
		t.Fatal(err)
	}
	*o.FlashinferAutotune = false
	if !*e.FlashinferAutotune {
		t.Fatal("resolved pointers alias overrides")
	}
	e, _, err = ResolveLongContext(p, nil)
	if err != nil {
		t.Fatal(err)
	}
	*p.FlashinferAutotune = true
	p.MaxNumSeqs = 8
	if *e.FlashinferAutotune || e.MaxNumSeqs != 32 {
		t.Fatal("resolved pointers alias preset")
	}
	for _, field := range []string{"trustRemoteCode", "disableCustomAllReduce", "flashinferAutotune"} {
		e := flag178Config(t, map[string]interface{}{field: false}, map[string]interface{}{field: true})
		if len(buildArgs(e)) != len(buildArgs(flag178Config(t, nil, nil)))+1 {
			t.Fatalf("%s override true not rendered", field)
		}
	}
}
