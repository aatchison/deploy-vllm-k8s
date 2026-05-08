package vllm

import (
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	corev1 "k8s.io/api/core/v1"
)

// TestBuildDeploymentManagedByLabel is the regression guard for issue #83:
// the operator scopes its controller-runtime informer cache to objects
// labelled "app.kubernetes.io/managed-by=vllm-operator". Drop the label on
// the SSA-applied Deployment and the cache will never observe it — every
// reconcile would then loop on IsNotFound for an object that exists in the
// API server. The label is a load-bearing wire-up, not just decoration.
func TestBuildDeploymentManagedByLabel(t *testing.T) {
	dep := buildTestDeployment(baseEffectiveConfig())

	got := dep.Labels[ManagedByLabelKey]
	if got != ManagedByLabelValue {
		t.Errorf("Deployment.metadata.labels[%q]: got %q, want %q (cache-scope filter relies on this — see operator/main.go cache.Options.ByObject)",
			ManagedByLabelKey, got, ManagedByLabelValue)
	}

	// Existing label set must be preserved — the pod selector and other
	// tooling still rely on the "app" label.
	if dep.Labels["app"] == "" {
		t.Error("Deployment.metadata.labels[app] must remain set alongside managed-by")
	}
}

// TestBuildServiceManagedByLabel mirrors the Deployment guard for Services.
// Services are also scoped in the cache; missing this label means the
// reconciler's "re-read after SSA-apply to learn the actual NodePort" step
// returns IsNotFound forever and the requeue-loop never converges.
func TestBuildServiceManagedByLabel(t *testing.T) {
	svc := BuildService("test-instance", "default", nil, metav1.OwnerReference{})

	got := svc.Labels[ManagedByLabelKey]
	if got != ManagedByLabelValue {
		t.Errorf("Service.metadata.labels[%q]: got %q, want %q (cache-scope filter relies on this — see operator/main.go cache.Options.ByObject)",
			ManagedByLabelKey, got, ManagedByLabelValue)
	}

	if svc.Labels["app"] == "" {
		t.Error("Service.metadata.labels[app] must remain set alongside managed-by")
	}
}

// TestManagedByLabelConstants pins the wire-format string. The cache filter
// in main.go and the BuildDeployment/BuildService labels MUST agree on the
// exact key+value; if a future refactor renames either side without the
// other, the cache silently de-syncs and reconciles stop. This test fails
// loudly so the constant change is intentional.
func TestManagedByLabelConstants(t *testing.T) {
	if ManagedByLabelKey != "app.kubernetes.io/managed-by" {
		t.Errorf("ManagedByLabelKey changed: got %q, want %q. The cache filter in operator/main.go uses this same value — they MUST stay in sync.",
			ManagedByLabelKey, "app.kubernetes.io/managed-by")
	}
	if ManagedByLabelValue != "vllm-operator" {
		t.Errorf("ManagedByLabelValue changed: got %q, want %q. The cache filter in operator/main.go uses this same value — they MUST stay in sync.",
			ManagedByLabelValue, "vllm-operator")
	}
}

// silence unused-import if corev1 is dropped from this test set in the future
var _ = corev1.Container{}
