package vllm

// Tests for the GPU-wedge mitigation fields: enforceEager, compilationConfig,
// plus a verification pass over the pre-existing maxNumSeqs wiring.
//
// Measured context: on RTX PRO 6000 (sm_120) the default CUDA-graph capture
// path wedges the device with Xid 79 after ~72 s, while --enforce-eager
// survived a >1 h soak. Neither knob was reachable from the CRD, so the fix
// could only be applied by hand-editing a Deployment.

import (
	"strings"
	"testing"

	vllmv1alpha1 "github.com/aatchison/deploy-vllm-k8s/operator/api/v1alpha1"
)

// --- enforceEager -----------------------------------------------------------

// TestBuildArgsEnforceEagerEmits also pins the flag's ARITY: --enforce-eager is
// a store_true argparse action in vLLM, so emitting a value after it would be
// parsed as a positional argument and rejected.
func TestBuildArgsEnforceEagerEmits(t *testing.T) {
	e := EffectiveConfig{EnforceEager: boolPtr(true)}
	args := buildArgs(e)
	found := false
	for i, a := range args {
		if a != "--enforce-eager" {
			continue
		}
		found = true
		if i+1 < len(args) && !strings.HasPrefix(args[i+1], "--") {
			t.Errorf("--enforce-eager must take no value; got %q after it: %v", args[i+1], args)
		}
	}
	if !found {
		t.Fatalf("--enforce-eager not emitted: %v", args)
	}
}

// TestBuildArgsEnforceEagerOmittedWhenNilOrFalse guards the tri-state: only
// &true emits. nil (unset) and &false must both render byte-identically to
// pre-change behaviour.
func TestBuildArgsEnforceEagerOmittedWhenNilOrFalse(t *testing.T) {
	for name, e := range map[string]EffectiveConfig{
		"nil":   {},
		"false": {EnforceEager: boolPtr(false)},
	} {
		args := buildArgs(e)
		for _, a := range args {
			if a == "--enforce-eager" {
				t.Errorf("EnforceEager=%s leaked --enforce-eager: %v", name, args)
			}
		}
	}
}

func TestResolveLongContextEnforceEager(t *testing.T) {
	p := baseLongContextPreset()

	// Unset in preset stays nil — no implicit default.
	e0, _, err := ResolveLongContext(p, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if e0.EnforceEager != nil {
		t.Errorf("EnforceEager: got %v, want nil when unset", *e0.EnforceEager)
	}

	// Preset value carried through.
	p.EnforceEager = boolPtr(true)
	e1, _, err := ResolveLongContext(p, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if e1.EnforceEager == nil || !*e1.EnforceEager {
		t.Errorf("EnforceEager preset not carried: got %v, want true", e1.EnforceEager)
	}

	// Override can turn it back OFF. This is the case a plain bool would lose:
	// with a non-pointer + omitempty, "override to false" is indistinguishable
	// from "no override" (the trap already documented on EnablePrefixCaching).
	o := &vllmv1alpha1.LongContextOverrides{EnforceEager: boolPtr(false)}
	e2, _, err := ResolveLongContext(p, o)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if e2.EnforceEager == nil || *e2.EnforceEager {
		t.Errorf("EnforceEager override to false not applied: got %v", e2.EnforceEager)
	}
}

// TestResolveLongContextEnforceEagerNoAliasing guards against the classic
// pointer-carry bug: the resolved config must own its bool, so mutating the
// preset afterwards cannot retroactively change a rendered Deployment.
func TestResolveLongContextEnforceEagerNoAliasing(t *testing.T) {
	p := baseLongContextPreset()
	p.EnforceEager = boolPtr(true)
	e, _, err := ResolveLongContext(p, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if e.EnforceEager == p.EnforceEager {
		t.Error("EffectiveConfig.EnforceEager aliases the preset pointer; must be a copy")
	}
	*p.EnforceEager = false
	if e.EnforceEager == nil || !*e.EnforceEager {
		t.Error("mutating the preset changed the already-resolved config")
	}
}

// --- compilationConfig ------------------------------------------------------

// TestBuildArgsCompilationConfigEmitsVerbatim pins the pass-through contract:
// the operator must not parse, reserialise, or reorder the value. A JSON
// re-encode would silently reorder keys and break byte-stability.
func TestBuildArgsCompilationConfigEmitsVerbatim(t *testing.T) {
	// Deliberately non-canonical: spaces, and keys NOT in sorted order.
	const raw = `{"level": 0, "cudagraph_capture_sizes": [1, 2, 4]}`
	args := buildArgs(EffectiveConfig{CompilationConfig: raw})
	found := false
	for i, a := range args {
		if a == "--compilation-config" && i+1 < len(args) && args[i+1] == raw {
			found = true
		}
	}
	if !found {
		t.Fatalf("--compilation-config %s not emitted verbatim: %v", raw, args)
	}
}

// TestBuildArgsCompilationConfigAcceptsBareLevel documents that the field is a
// free-form string on purpose: vLLM's --compilation-config takes either a JSON
// object or a bare optimization level, so a struct-typed field would reject a
// value vLLM accepts.
func TestBuildArgsCompilationConfigAcceptsBareLevel(t *testing.T) {
	args := buildArgs(EffectiveConfig{CompilationConfig: "3"})
	found := false
	for i, a := range args {
		if a == "--compilation-config" && i+1 < len(args) && args[i+1] == "3" {
			found = true
		}
	}
	if !found {
		t.Fatalf("bare optimization level not passed through: %v", args)
	}
}

func TestBuildArgsCompilationConfigOmittedWhenEmpty(t *testing.T) {
	args := buildArgs(EffectiveConfig{})
	for _, a := range args {
		if a == "--compilation-config" {
			t.Errorf("empty CompilationConfig leaked into args: %v", args)
		}
	}
}

func TestResolveLongContextCompilationConfig(t *testing.T) {
	p := baseLongContextPreset()
	p.CompilationConfig = strPtr(`{"level":0}`)

	e, _, err := ResolveLongContext(p, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if e.CompilationConfig != `{"level":0}` {
		t.Errorf("CompilationConfig preset not carried: got %q", e.CompilationConfig)
	}

	o := &vllmv1alpha1.LongContextOverrides{
		CompilationConfig: strPtr(`{"cudagraph_capture_sizes":[1,2]}`),
	}
	e2, _, err := ResolveLongContext(p, o)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if e2.CompilationConfig != `{"cudagraph_capture_sizes":[1,2]}` {
		t.Errorf("CompilationConfig override not applied: got %q", e2.CompilationConfig)
	}
}

// --- maxNumSeqs (pre-existing field: verification, not addition) -------------

// TestResolveLongContextMaxNumSeqsCarried is the verification the brief asked
// for. maxNumSeqs already existed on LongContextPresetSpec and buildArgs
// already emitted --max-num-seqs (TestMaxNumSeqsFlag), but nothing asserted
// the PRESET -> Resolve -> argv path for it on its own.
//
// It also records a real gap found while verifying: LongContextOverrides has
// no maxNumSeqs field, so an instance cannot override the preset's value the
// way it can for e.g. maxNumBatchedTokens. Same for limitMmPerPrompt and
// kvCacheDtypeSkipLayers. Out of scope here; tracked separately.
func TestResolveLongContextMaxNumSeqsCarried(t *testing.T) {
	p := baseLongContextPreset()
	p.MaxNumSeqs = 4

	e, _, err := ResolveLongContext(p, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if e.MaxNumSeqs != 4 {
		t.Fatalf("MaxNumSeqs preset not carried: got %d, want 4", e.MaxNumSeqs)
	}

	args := buildArgs(e)
	found := false
	for i, a := range args {
		if a == "--max-num-seqs" && i+1 < len(args) && args[i+1] == "4" {
			found = true
		}
	}
	if !found {
		t.Errorf("--max-num-seqs 4 not rendered from preset: %v", args)
	}
}

// --- blast-radius guards ----------------------------------------------------

// TestBuildArgsWedgeFieldsAbsentFromBaseline: a preset that sets none of the
// three fields must render exactly as before. This is the guard that keeps the
// change zero-blast-radius for every existing preset.
func TestBuildArgsWedgeFieldsAbsentFromBaseline(t *testing.T) {
	args := buildArgs(baseEffectiveConfig())
	for _, forbidden := range []string{"--enforce-eager", "--compilation-config", "--max-num-seqs"} {
		for _, a := range args {
			if a == forbidden {
				t.Errorf("baseline config emitted %s: %v", forbidden, args)
			}
		}
	}
}

// TestResolveStandardHashUnchangedByWedgeFields pins the standard (ModelPreset)
// resolved-config-hash to the value captured on main@8170956 BEFORE these
// fields existed. omitempty on all three must keep it byte-identical; if it
// changes, every existing VLLMInstance sees a spurious config drift and gets
// needlessly restarted on operator upgrade.
func TestResolveStandardHashUnchangedByWedgeFields(t *testing.T) {
	e, h, err := Resolve(basePreset(), nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if e.EnforceEager != nil {
		t.Errorf("standard path leaked EnforceEager=%v (want nil)", *e.EnforceEager)
	}
	if e.CompilationConfig != "" {
		t.Errorf("standard path leaked CompilationConfig=%q (want empty)", e.CompilationConfig)
	}
	const wantHash = "306b78da7ce660f1ff2c9b939812416d417ce2eb585ba6fb6d2e4cfa967897fc"
	if h != wantHash {
		t.Errorf("standard resolved-config-hash changed: got %s, want %s", h, wantHash)
	}
}

// TestLongContextHashStableWhenWedgeFieldsUnset is the long-context sibling of
// the above: adding the fields must not perturb a preset that ignores them.
func TestLongContextHashStableWhenWedgeFieldsUnset(t *testing.T) {
	h1, err := HashConfig(func() EffectiveConfig {
		e, _, err := ResolveLongContext(baseLongContextPreset(), nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		return e
	}())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	p := baseLongContextPreset()
	p.EnforceEager = boolPtr(false) // explicit false, still no flag emitted...
	e2, _, err := ResolveLongContext(p, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	h2, err := HashConfig(e2)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// ...but it IS a distinct declared intent, so the hash MUST change.
	// Otherwise "explicitly pinned to CUDA graphs" and "never configured"
	// would be indistinguishable in status.resolvedConfigHash.
	if h1 == h2 {
		t.Error("explicit enforceEager=false must be distinguishable from unset in the config hash")
	}
}

// --- end-to-end -------------------------------------------------------------

// TestBuildDeploymentWedgeFieldsEndToEnd asserts the whole path:
// LongContextPreset -> ResolveLongContext -> BuildDeployment -> container argv.
// buildArgs unit tests alone cannot catch a field dropped in the Resolve ->
// BuildDeployment handoff, which is exactly the class of drift this lane exists
// to close.
func TestBuildDeploymentWedgeFieldsEndToEnd(t *testing.T) {
	p := baseLongContextPreset()
	p.EnforceEager = boolPtr(true)
	p.CompilationConfig = strPtr(`{"level":0}`)
	p.MaxNumSeqs = 8

	e, _, err := ResolveLongContext(p, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	dep := buildTestDeployment(e)

	var args []string
	for _, c := range dep.Spec.Template.Spec.Containers {
		if c.Name == ContainerName {
			args = c.Args
		}
	}
	if args == nil {
		t.Fatalf("no %q container in rendered deployment", ContainerName)
	}
	joined := strings.Join(args, "\x00")
	for _, want := range []string{
		"--enforce-eager",
		"--max-num-seqs\x008",
		"--compilation-config\x00{\"level\":0}",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("rendered container args missing %q; got %v", want, args)
		}
	}
}
