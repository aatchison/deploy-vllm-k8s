package vllm

import (
	"encoding/json"
	"fmt"
	"regexp"
)

var revision178Pattern = regexp.MustCompile(`^[0-9a-fA-F]{40}$`)

func validateLongContextFlags(e EffectiveConfig) error {
	if e.ModelRevision != "" && !revision178Pattern.MatchString(e.ModelRevision) {
		return fmt.Errorf("invalid modelRevision: must be a 40-hex commit SHA")
	}
	if e.CodeRevision != "" && !revision178Pattern.MatchString(e.CodeRevision) {
		return fmt.Errorf("invalid codeRevision: must be a 40-hex commit SHA")
	}
	if e.TokenizerRevision != "" && !revision178Pattern.MatchString(e.TokenizerRevision) {
		return fmt.Errorf("invalid tokenizerRevision: must be a 40-hex commit SHA")
	}
	for _, cfg := range []struct{ name, value string }{
		{"engramConfig", e.EngramConfig},
	} {
		if cfg.value == "" {
			continue
		}
		var obj map[string]json.RawMessage
		if err := json.Unmarshal([]byte(cfg.value), &obj); err != nil || obj == nil {
			return fmt.Errorf("invalid %s: must be a JSON object", cfg.name)
		}
	}
	return nil
}

// buildLongContextFlagArgs preserves JSON strings byte-for-byte as single argv values.
func buildLongContextFlagArgs(e EffectiveConfig) []string {
	var args []string
	if e.ModelRevision != "" {
		args = append(args, "--revision", e.ModelRevision)
	}
	if e.CodeRevision != "" {
		args = append(args, "--code-revision", e.CodeRevision)
	}
	if e.TokenizerRevision != "" {
		args = append(args, "--tokenizer-revision", e.TokenizerRevision)
	}
	if e.TrustRemoteCode {
		args = append(args, "--trust-remote-code")
	}
	if e.DisableCustomAllReduce {
		args = append(args, "--disable-custom-all-reduce")
	}
	if e.FlashinferAutotune != nil {
		if *e.FlashinferAutotune {
			args = append(args, "--enable-flashinfer-autotune")
		} else {
			args = append(args, "--no-enable-flashinfer-autotune")
		}
	}
	if e.EngramConfig != "" {
		args = append(args, "--engram-config", e.EngramConfig)
	}
	return args
}
