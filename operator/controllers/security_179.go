package controllers

import (
	"fmt"
	"strings"

	v1alpha1 "github.com/aatchison/deploy-vllm-k8s/operator/api/v1alpha1"
	"github.com/aatchison/deploy-vllm-k8s/operator/internal/vllm"
	"k8s.io/apimachinery/pkg/util/validation"
)

// ParseEngramIPCNamespaces accepts explicit namespace names only. Empty means
// disabled. Wildcards and empty elements are configuration errors.
func ParseEngramIPCNamespaces(raw string) (map[string]struct{}, error) {
	allowed := map[string]struct{}{}
	if strings.TrimSpace(raw) == "" {
		return allowed, nil
	}
	for _, item := range strings.Split(raw, ",") {
		ns := strings.TrimSpace(item)
		if ns == "" || len(validation.IsDNS1123Label(ns)) != 0 {
			return nil, fmt.Errorf("engram-ipc-namespaces requires explicit namespace names, got %q", ns)
		}
		allowed[ns] = struct{}{}
	}
	return allowed, nil
}

func (r *LongContextInstanceReconciler) validateSecurityPolicy179(e vllm.EffectiveConfig, namespace string) error {
	if err := vllm.ValidateEffectiveConfig(e); err != nil {
		return err
	}
	if e.SecurityProfile == v1alpha1.SecurityProfileEngramIPC {
		if _, ok := r.EngramIPCNamespaces[namespace]; !ok {
			return fmt.Errorf("securityProfile engram-ipc is not allowed in namespace %q", namespace)
		}
	}
	return nil
}
