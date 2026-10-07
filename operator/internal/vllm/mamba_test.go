package vllm

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	vllmv1alpha1 "github.com/aatchison/deploy-vllm-k8s/operator/api/v1alpha1"
)

// Issue #404: add optional MambaCacheMode / MambaBackend to LongContextPreset
// so the proven `--mamba-cache-mode align` fix (DSpark speculative decoding,
// 81% draft acceptance) can be expressed through GitOps. The overriding
// requirement: with both fields unset, the rendered arg list and the resolved
// config hash must be BYTE-IDENTICAL to pre-#404 behavior so every existing
// preset is unaffected.

// TestBuildArgsMambaUnsetByteIdentical is the headline zero-blast-radius guard.
// The base long-context preset leaves both mamba fields unset; the rendered
// arg list must exactly equal the pre-#404 golden and contain neither new flag.
func TestBuildArgsMambaUnsetByteIdentical(t *testing.T) {
	p := baseLongContextPreset() // MambaCacheMode / MambaBackend unset
	e, _, err := ResolveLongContext(p, nil)
	if err != nil {
		t.Fatalf("ResolveLongContext: %v", err)
	}
	got := buildArgs(e)

	// Golden = the exact args the base preset rendered before #404 existed.
	want := []string{
		"--model", "nvidia/Gemma-4-31B-IT-NVFP4",
		"--quantization", "nvfp4",
		"--port", "8000",
		"--max-model-len", "262144",
		"--gpu-memory-utilization", "0.92",
		"--enable-auto-tool-choice",
		"--tool-call-parser", "gemma4",
		"--kv-cache-dtype", "fp8_e4m3",
		"--enable-prefix-caching",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unset mamba fields changed the rendered args (blast radius!):\n got=%v\nwant=%v", got, want)
	}
	if flagIndex(got, "--mamba-cache-mode") != -1 {
		t.Errorf("--mamba-cache-mode must not be emitted when MambaCacheMode is unset; args=%v", got)
	}
	if flagIndex(got, "--mamba-backend") != -1 {
		t.Errorf("--mamba-backend must not be emitted when MambaBackend is unset; args=%v", got)
	}
}

// TestBuildArgsMambaCacheModeAlign: MambaCacheMode=align emits exactly
// `--mamba-cache-mode align` (flag immediately followed by its value, once).
func TestBuildArgsMambaCacheModeAlign(t *testing.T) {
	e := baseEffectiveConfig()
	e.MambaCacheMode = "align"
	args := buildArgs(e)

	idx := flagIndex(args, "--mamba-cache-mode")
	if idx == -1 {
		t.Fatalf("--mamba-cache-mode not emitted when MambaCacheMode=align; args=%v", args)
	}
	if idx+1 >= len(args) || args[idx+1] != "align" {
		t.Fatalf("--mamba-cache-mode must be immediately followed by \"align\"; args=%v", args)
	}
	// Exactly once.
	count := 0
	for _, a := range args {
		if a == "--mamba-cache-mode" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("--mamba-cache-mode emitted %d times, want 1; args=%v", count, args)
	}
}

// TestBuildArgsMambaBackendFlashinfer: MambaBackend=flashinfer emits exactly
// `--mamba-backend flashinfer`.
func TestBuildArgsMambaBackendFlashinfer(t *testing.T) {
	e := baseEffectiveConfig()
	e.MambaBackend = "flashinfer"
	args := buildArgs(e)

	idx := flagIndex(args, "--mamba-backend")
	if idx == -1 {
		t.Fatalf("--mamba-backend not emitted when MambaBackend=flashinfer; args=%v", args)
	}
	if idx+1 >= len(args) || args[idx+1] != "flashinfer" {
		t.Fatalf("--mamba-backend must be immediately followed by \"flashinfer\"; args=%v", args)
	}
}

// TestBuildArgsMambaDoesNotDisturbNeighbours: setting the new flags must not
// change the presence, value, or relative order of any pre-existing flag. We
// diff the base-preset args against the same args with only the mamba flags
// added, and assert the non-mamba subsequence is unchanged.
func TestBuildArgsMambaDoesNotDisturbNeighbours(t *testing.T) {
	p := baseLongContextPreset()
	eBase, _, err := ResolveLongContext(p, nil)
	if err != nil {
		t.Fatalf("ResolveLongContext base: %v", err)
	}
	baseArgs := buildArgs(eBase)

	eBoth, _, err := ResolveLongContext(p, nil)
	if err != nil {
		t.Fatalf("ResolveLongContext both: %v", err)
	}
	eBoth.MambaCacheMode = "align"
	eBoth.MambaBackend = "flashinfer"
	bothArgs := buildArgs(eBoth)

	// Strip the mamba flags + their values from bothArgs; the remainder must
	// equal baseArgs exactly (same flags, same values, same order).
	var stripped []string
	for i := 0; i < len(bothArgs); i++ {
		if bothArgs[i] == "--mamba-cache-mode" || bothArgs[i] == "--mamba-backend" {
			i++ // skip the value too
			continue
		}
		stripped = append(stripped, bothArgs[i])
	}
	if !reflect.DeepEqual(stripped, baseArgs) {
		t.Errorf("adding mamba flags disturbed existing flags:\n base=%v\n stripped=%v", baseArgs, stripped)
	}
}

// TestResolveLongContextMambaFields: both fields must flow preset->EffectiveConfig.
func TestResolveLongContextMambaFields(t *testing.T) {
	p := baseLongContextPreset()
	p.MambaCacheMode = "align"
	p.MambaBackend = "flashinfer"

	e, _, err := ResolveLongContext(p, nil)
	if err != nil {
		t.Fatalf("ResolveLongContext: %v", err)
	}
	if e.MambaCacheMode != "align" {
		t.Errorf("EffectiveConfig.MambaCacheMode: got %q, want align", e.MambaCacheMode)
	}
	if e.MambaBackend != "flashinfer" {
		t.Errorf("EffectiveConfig.MambaBackend: got %q, want flashinfer", e.MambaBackend)
	}
}

// TestResolveLongContextMambaOverrides: overrides must win over preset values.
func TestResolveLongContextMambaOverrides(t *testing.T) {
	p := baseLongContextPreset()
	e, _, err := ResolveLongContext(p, &vllmv1alpha1.LongContextOverrides{
		MambaCacheMode: strPtr("align"),
		MambaBackend:   strPtr("flashinfer"),
	})
	if err != nil {
		t.Fatalf("ResolveLongContext: %v", err)
	}
	if e.MambaCacheMode != "align" {
		t.Errorf("override MambaCacheMode: got %q, want align", e.MambaCacheMode)
	}
	if e.MambaBackend != "flashinfer" {
		t.Errorf("override MambaBackend: got %q, want flashinfer", e.MambaBackend)
	}
}

// TestMambaOmitemptyHashStability is the omitempty proof: an EffectiveConfig
// with the mamba fields unset must marshal to JSON WITHOUT the new keys, so the
// resolved-config hash is identical to the pre-#404 shape and no existing
// instance re-rolls on operator upgrade.
func TestMambaOmitemptyHashStability(t *testing.T) {
	e := baseEffectiveConfig() // mamba fields unset (zero value "")
	buf, err := json.Marshal(e)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	js := string(buf)
	if strings.Contains(js, "mambaCacheMode") {
		t.Errorf("unset MambaCacheMode leaked into JSON (omitempty broken): %s", js)
	}
	if strings.Contains(js, "mambaBackend") {
		t.Errorf("unset MambaBackend leaked into JSON (omitempty broken): %s", js)
	}

	// Hash with unset fields must equal hash after setting them to their zero
	// value explicitly (proves the zero value never widens the JSON).
	h1, err := HashConfig(e)
	if err != nil {
		t.Fatalf("HashConfig: %v", err)
	}
	e.MambaCacheMode = ""
	e.MambaBackend = ""
	h2, err := HashConfig(e)
	if err != nil {
		t.Fatalf("HashConfig: %v", err)
	}
	if h1 != h2 {
		t.Errorf("hash drifted setting mamba fields to zero value: %s vs %s", h1, h2)
	}
}

// TestMambaCoexistsWithMainFields is the post-rebase guard (issue #404 rebase
// onto true main). It proves the #404 change does not disturb the fields main
// added between the stale base and true main — SpeculativeConfig, ChatTemplate,
// LimitMmPerPrompt, MaxNumSeqs, KVCacheDtypeSkipLayers. With all of main's
// fields AND both mamba fields set, every one of main's flags must still emit
// with its exact value, and stripping the two mamba flags must reproduce the
// arg list main renders without #404 (same flags, values, and order).
func TestMambaCoexistsWithMainFields(t *testing.T) {
	// Config carrying every field main added, mamba unset.
	mainOnly := baseEffectiveConfig()
	mainOnly.ChatTemplate = "/tmpl/chat.jinja"
	mainOnly.SpeculativeConfig = `{"method":"dspark","num_speculative_tokens":3}`
	mainOnly.LimitMmPerPrompt = `{"image":0,"audio":0}`
	mainOnly.MaxNumSeqs = 3
	mainOnly.KVCacheDtypeSkipLayers = "sliding_window"
	mainArgs := buildArgs(mainOnly)

	// Same config, now with the #404 fields also set.
	both := mainOnly
	both.MambaCacheMode = "align"
	both.MambaBackend = "flashinfer"
	bothArgs := buildArgs(both)

	// 1. Each of main's flags is present with the correct value in both renders.
	checks := []struct{ flag, val string }{
		{"--chat-template", "/tmpl/chat.jinja"},
		{"--speculative-config", `{"method":"dspark","num_speculative_tokens":3}`},
		{"--limit-mm-per-prompt", `{"image":0,"audio":0}`},
		{"--max-num-seqs", "3"},
		{"--kv-cache-dtype-skip-layers", "sliding_window"},
	}
	for _, c := range checks {
		for name, args := range map[string][]string{"mainOnly": mainArgs, "both": bothArgs} {
			idx := flagIndex(args, c.flag)
			if idx == -1 {
				t.Errorf("%s: %s not emitted; args=%v", name, c.flag, args)
				continue
			}
			if idx+1 >= len(args) || args[idx+1] != c.val {
				t.Errorf("%s: %s value: got %q, want %q", name, c.flag, args[idx+1], c.val)
			}
		}
	}

	// 2. Stripping only the two mamba flags from `both` reproduces `mainOnly`
	//    exactly — the #404 change adds nothing else and reorders nothing.
	var stripped []string
	for i := 0; i < len(bothArgs); i++ {
		if bothArgs[i] == "--mamba-cache-mode" || bothArgs[i] == "--mamba-backend" {
			i++ // skip value
			continue
		}
		stripped = append(stripped, bothArgs[i])
	}
	if !reflect.DeepEqual(stripped, mainArgs) {
		t.Errorf("#404 disturbed main's flags:\n mainOnly=%v\n stripped =%v", mainArgs, stripped)
	}
}

// TestMambaResolveCoexistsWithMainOverrides proves the resolver keeps main's
// new preset+override fields and the #404 fields on the same EffectiveConfig
// (guards against a rebase resolution that dropped one side of the merge).
func TestMambaResolveCoexistsWithMainOverrides(t *testing.T) {
	p := baseLongContextPreset()
	p.ChatTemplate = "/tmpl/chat.jinja"
	p.SpeculativeConfig = `{"method":"dspark"}`
	p.LimitMmPerPrompt = `{"image":0}`
	p.MaxNumSeqs = 2
	p.KVCacheDtypeSkipLayers = "sliding_window"
	p.MambaCacheMode = "align"
	p.MambaBackend = "flashinfer"

	e, _, err := ResolveLongContext(p, nil)
	if err != nil {
		t.Fatalf("ResolveLongContext: %v", err)
	}
	if e.ChatTemplate != "/tmpl/chat.jinja" {
		t.Errorf("ChatTemplate dropped: %q", e.ChatTemplate)
	}
	if e.SpeculativeConfig != `{"method":"dspark"}` {
		t.Errorf("SpeculativeConfig dropped: %q", e.SpeculativeConfig)
	}
	if e.LimitMmPerPrompt != `{"image":0}` {
		t.Errorf("LimitMmPerPrompt dropped: %q", e.LimitMmPerPrompt)
	}
	if e.MaxNumSeqs != 2 {
		t.Errorf("MaxNumSeqs dropped: %d", e.MaxNumSeqs)
	}
	if e.KVCacheDtypeSkipLayers != "sliding_window" {
		t.Errorf("KVCacheDtypeSkipLayers dropped: %q", e.KVCacheDtypeSkipLayers)
	}
	if e.MambaCacheMode != "align" || e.MambaBackend != "flashinfer" {
		t.Errorf("mamba fields dropped: %q / %q", e.MambaCacheMode, e.MambaBackend)
	}
}
