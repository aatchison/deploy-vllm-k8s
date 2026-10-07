package vllm

import (
	"encoding/json"
	"fmt"

	v1alpha1 "github.com/aatchison/deploy-vllm-k8s/operator/api/v1alpha1"
)

// resolveSecurityProfile179 keeps explicit default identical to omission,
// including the resolved-config hash used for drift detection.
func resolveSecurityProfile179(e *EffectiveConfig, preset *v1alpha1.LongContextPresetSpec, overrides *v1alpha1.LongContextOverrides) {
	if preset != nil {
		e.SecurityProfile = preset.SecurityProfile
	}
	if overrides != nil {
		if overrides.SecurityProfile != nil {
			e.SecurityProfile = *overrides.SecurityProfile
		}
	}
	if e.SecurityProfile == v1alpha1.SecurityProfileDefault {
		e.SecurityProfile = ""
	}
}

func validateSecurityProfile179(e EffectiveConfig) error {
	switch e.SecurityProfile {
	case "", v1alpha1.SecurityProfileDefault, v1alpha1.SecurityProfileEngramIPC:
	default:
		return fmt.Errorf("invalid securityProfile: must be default or engram-ipc")
	}
	if e.SecurityProfile == v1alpha1.SecurityProfileEngramIPC && e.EngramConfig == "" {
		return fmt.Errorf("engram-ipc requires a nonempty engramConfig")
	}
	if e.EngramConfig != "" {
		var obj map[string]json.RawMessage
		if len(e.EngramConfig) > 65536 || json.Unmarshal([]byte(e.EngramConfig), &obj) != nil || obj == nil {
			return fmt.Errorf("invalid engramConfig: must encode a JSON object of at most 65536 bytes")
		}
	}
	return nil
}
