package vllm

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	v1 "github.com/aatchison/deploy-vllm-k8s/operator/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func resolveMemoryTest(t *testing.T, long bool, presetJSON, overrideJSON string) (EffectiveConfig, string) {
	t.Helper()
	var e EffectiveConfig
	var hash string
	var err error
	if long {
		p := baseLongContextPreset()
		var o v1.LongContextOverrides
		if err := json.Unmarshal([]byte(presetJSON), p); err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal([]byte(overrideJSON), &o); err != nil {
			t.Fatal(err)
		}
		e, hash, err = ResolveLongContext(p, &o)
	} else {
		p := basePreset()
		var o v1.ModelConfigOverrides
		if err := json.Unmarshal([]byte(presetJSON), p); err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal([]byte(overrideJSON), &o); err != nil {
			t.Fatal(err)
		}
		e, hash, err = Resolve(p, &o)
	}
	if err != nil {
		t.Fatal(err)
	}
	return e, hash
}

func TestMemoryResources(t *testing.T) {
	for _, long := range []bool{false, true} {
		for _, tc := range []struct{ name, preset, overrides, request, limit string }{
			{"unset", "{}", "{}", "", ""},
			{"preset", `{"memoryRequest":"64Gi","memoryLimit":"80Gi"}`, "{}", "64Gi", "80Gi"},
			{"override", `{"memoryRequest":"64Gi","memoryLimit":"80Gi"}`, `{"memoryRequest":"72Gi","memoryLimit":"96Gi"}`, "72Gi", "96Gi"},
			{"partial", `{"memoryRequest":"64Gi","memoryLimit":"80Gi"}`, `{"memoryLimit":"96Gi"}`, "64Gi", "96Gi"},
			{"request only", `{"memoryRequest":"64Gi"}`, "{}", "64Gi", ""},
			{"limit only", `{"memoryLimit":"80Gi"}`, "{}", "", "80Gi"},
			{"clear request", `{"memoryRequest":"64Gi","memoryLimit":"80Gi"}`, `{"memoryRequest":""}`, "", "80Gi"},
			{"clear limit", `{"memoryRequest":"64Gi","memoryLimit":"80Gi"}`, `{"memoryLimit":""}`, "64Gi", ""},
			{"clear both", `{"memoryRequest":"64Gi","memoryLimit":"80Gi"}`, `{"memoryRequest":"","memoryLimit":""}`, "", ""},
		} {
			t.Run(tc.name+map[bool]string{false: "/standard", true: "/long"}[long], func(t *testing.T) {
				e, hash := resolveMemoryTest(t, long, tc.preset, tc.overrides)
				if err := ValidateEffectiveConfig(e); err != nil {
					t.Fatal(err)
				}
				dep := BuildDeployment("test", "default", 1, e, "models", corev1.SecretKeySelector{}, nil, metav1.OwnerReference{})
				resources := dep.Spec.Template.Spec.Containers[0].Resources
				for field, want := range map[string]string{"request": tc.request, "limit": tc.limit} {
					list := resources.Requests
					if field == "limit" {
						list = resources.Limits
					}
					got, ok := list[corev1.ResourceMemory]
					if (want != "") != ok || (ok && got.String() != want) {
						t.Errorf("memory %s: got %s present=%v want %s", field, got.String(), ok, want)
					}
				}
				if len(resources.Limits) != 1+map[bool]int{false: 0, true: 1}[tc.limit != ""] {
					t.Errorf("MIG limit changed: %v", resources)
				}
				base, baseHash := resolveMemoryTest(t, long, "{}", "{}")
				if tc.request != "" || tc.limit != "" {
					if hash == baseHash {
						t.Error("memory change did not change config hash")
					}
				} else {
					baseline := BuildDeployment("test", "default", 1, base, "models", corev1.SecretKeySelector{}, nil, metav1.OwnerReference{})
					if hash != baseHash || !reflect.DeepEqual(dep, baseline) {
						t.Error("unset or cleared memory must restore baseline hash and Deployment")
					}
				}
			})
		}
	}
}

func TestMemoryValidation(t *testing.T) {
	for _, long := range []bool{false, true} {
		for _, field := range []string{"memoryRequest", "memoryLimit"} {
			for _, value := range []string{"invalid", "-1Gi", "0Gi", "1e", "1KI", " 8Gi", ""} {
				overrides, _ := json.Marshal(map[string]string{field: value})
				e, _ := resolveMemoryTest(t, long, "{}", string(overrides))
				err := ValidateEffectiveConfig(e)
				// Empty override explicitly clears an inherited resource.
				if value == "" {
					if err != nil {
						t.Fatal(err)
					}
					continue
				}
				if err == nil || !strings.Contains(err.Error(), field) {
					t.Errorf("%s=%q: expected field error, got %v", field, value, err)
				}
			}
		}
		for _, tc := range []struct {
			preset, override string
			valid            bool
		}{
			{`{"memoryRequest":"1Gi","memoryLimit":"1024Mi"}`, "{}", true},
			{`{"memoryRequest":"1G","memoryLimit":"1Gi"}`, "{}", true},
			{`{"memoryRequest":"2Gi","memoryLimit":"1Gi"}`, "{}", false},
			{`{"memoryRequest":"64Gi","memoryLimit":"80Gi"}`, `{"memoryLimit":"32Gi"}`, false},
			{`{"memoryRequest":"64Gi","memoryLimit":"80Gi"}`, `{"memoryRequest":"96Gi"}`, false},
			{`{"memoryRequest":"64Gi","memoryLimit":"80Gi"}`, `{"memoryRequest":"","memoryLimit":""}`, true},
		} {
			e, _ := resolveMemoryTest(t, long, tc.preset, tc.override)
			err := ValidateEffectiveConfig(e)
			if tc.valid != (err == nil) {
				t.Errorf("preset %s override %s: %v", tc.preset, tc.override, err)
			}
		}
	}
}

func TestExplicitPrefixCaching(t *testing.T) {
	for _, tc := range []struct {
		value *bool
		want  string
	}{{nil, ""}, {boolPtr(true), "--enable-prefix-caching"}, {boolPtr(false), "--no-enable-prefix-caching"}} {
		e := baseEffectiveConfig()
		e.EnablePrefixCaching = nil
		baseline := buildArgs(e)
		e.EnablePrefixCaching = tc.value
		got := buildArgs(e)
		var filtered []string
		var flags []string
		for _, a := range got {
			if a == "--enable-prefix-caching" || a == "--no-enable-prefix-caching" {
				flags = append(flags, a)
			} else {
				filtered = append(filtered, a)
			}
		}
		var want []string
		if tc.want != "" {
			want = []string{tc.want}
		}
		if !reflect.DeepEqual(flags, want) || !reflect.DeepEqual(filtered, baseline) {
			t.Errorf("got %v want flag %q and unchanged baseline %v", got, tc.want, baseline)
		}
	}
	p := baseLongContextPreset()
	p.EnablePrefixCaching = boolPtr(true)
	e, _, err := ResolveLongContext(p, &v1.LongContextOverrides{EnablePrefixCaching: boolPtr(false), MambaCacheMode: strPtr("none")})
	if err != nil {
		t.Fatal(err)
	}
	if flagIndex(buildArgs(e), "--no-enable-prefix-caching") < 0 {
		t.Fatal("false override must reach argv with mamba mode none")
	}
}
