package vllm

import (
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
)

// TestBuildDeploymentPodSecurityContext is the regression guard for issue #37:
// the upstream vllm/vllm-openai image runs as root by default, which is
// rejected by the "restricted" PodSecurity admission profile. Every Deployment
// produced by BuildDeployment MUST carry runAsNonRoot=true, an explicit
// non-zero runAsUser, an fsGroup so the PVC is writable, and a
// RuntimeDefault seccompProfile.
func TestBuildDeploymentPodSecurityContext(t *testing.T) {
	dep := buildTestDeployment(baseEffectiveConfig())
	psc := dep.Spec.Template.Spec.SecurityContext
	if psc == nil {
		t.Fatal("pod-level securityContext is nil; restricted PSA will reject the pod")
	}
	if psc.RunAsNonRoot == nil || !*psc.RunAsNonRoot {
		t.Errorf("RunAsNonRoot: got %v, want true", psc.RunAsNonRoot)
	}
	if psc.RunAsUser == nil {
		t.Error("RunAsUser must be set explicitly so a future image swap doesn't silently regress to root")
	} else if *psc.RunAsUser == 0 {
		t.Errorf("RunAsUser must be non-zero; got %d", *psc.RunAsUser)
	}
	if psc.FSGroup == nil {
		t.Error("FSGroup must be set so the PVC mount is writable by the container user")
	}
	// fsGroup must match runAsUser so the HF_HOME directory and weight cache
	// are owned by the running user.
	if psc.RunAsUser != nil && psc.FSGroup != nil && *psc.RunAsUser != *psc.FSGroup {
		t.Errorf("FSGroup (%d) must match RunAsUser (%d) so PVC mount is writable", *psc.FSGroup, *psc.RunAsUser)
	}
	if psc.SeccompProfile == nil {
		t.Fatal("SeccompProfile must be set; required by restricted PSA")
	}
	if psc.SeccompProfile.Type != corev1.SeccompProfileTypeRuntimeDefault {
		t.Errorf("SeccompProfile.Type: got %q, want %q", psc.SeccompProfile.Type, corev1.SeccompProfileTypeRuntimeDefault)
	}
}

// TestBuildDeploymentTorchCacheDirEnv is the regression guard for issue #103:
// a recent torch upgrade calls getpass.getuser() at module-import time
// (torch._dynamo.package), which falls back to pwd.getpwuid(os.getuid()) and
// raises KeyError when the running uid has no /etc/passwd entry. The pod runs
// as uid 1000 (issue #37) and the base image has no matching passwd entry, so
// every vLLM pod crashes before any vLLM code executes.
//
// Setting TORCHINDUCTOR_CACHE_DIR up-front short-circuits the lazy-init in
// torch's cache_dir_utils.py before it can call getpwuid. HOME is also set so
// any other code path that reads $HOME has a writable target (the uid-1000
// passwd entry would point at /home/vllm if it existed; without one, $HOME is
// unset).
//
// Both vars must point at a writable path. /tmp is world-writable and is the
// safe default for an ephemeral cache; the cache is regenerated on each pod
// start anyway.
func TestBuildDeploymentTorchCacheDirEnv(t *testing.T) {
	dep := buildTestDeployment(baseEffectiveConfig())
	containers := dep.Spec.Template.Spec.Containers
	if len(containers) == 0 {
		t.Fatal("expected at least one container")
	}
	env := envMap(containers[0].Env)
	if got := env["TORCHINDUCTOR_CACHE_DIR"]; got == "" {
		t.Error("TORCHINDUCTOR_CACHE_DIR must be set so torch._dynamo.package's import-time getpwuid call is bypassed (issue #103)")
	}
	if got := env["HOME"]; got == "" {
		t.Error("HOME must be set so torch and other libraries don't fall back to getpwuid on a uid with no passwd entry (issue #103)")
	}
}

func envMap(env []corev1.EnvVar) map[string]string {
	m := make(map[string]string, len(env))
	for _, e := range env {
		m[e.Name] = e.Value
	}
	return m
}

// TestBuildDeploymentNoServiceAccountToken is the regression guard for issue
// #74: the upstream vllm/vllm-openai container makes zero kube API calls, but
// the namespace's default ServiceAccount token would otherwise be auto-mounted
// at /var/run/secrets/kubernetes.io/serviceaccount/token. On microk8s the
// default SA is often cluster-admin, so an RCE inside vLLM (poisoned weights,
// custom modeling code, future CVE) would inherit a usable kube API token and
// defeat the HF_TOKEN file-mount hardening from #48.
//
// The rendered Pod must have AutomountServiceAccountToken explicitly set to
// false. The default (nil) is unsafe — kubelet treats nil as "automount" — so
// the test asserts both that the pointer is non-nil and that it dereferences
// to false.
func TestBuildDeploymentNoServiceAccountToken(t *testing.T) {
	dep := buildTestDeployment(baseEffectiveConfig())
	got := dep.Spec.Template.Spec.AutomountServiceAccountToken
	if got == nil {
		t.Fatal("AutomountServiceAccountToken is nil; kubelet treats this as automount=true and will mount the default SA token")
	}
	if *got {
		t.Errorf("AutomountServiceAccountToken: got true, want false (issue #74)")
	}
	// The pod must also not pin a non-default ServiceAccountName: leaving it
	// empty + automount=false is the safest combination (no token, no SA
	// binding to escalate against).
	if sa := dep.Spec.Template.Spec.ServiceAccountName; sa != "" {
		t.Errorf("ServiceAccountName: got %q, want empty (issue #74 — no SA binding for model pods)", sa)
	}
}

// TestBuildDeploymentNoServiceAccountTokenWithLMCache is the LMCache-sidecar
// variant of the #74 regression: AutomountServiceAccountToken is a pod-level
// field, so it must remain false even when the sidecar is appended to the
// containers list. A sidecar pulling in a default-SA token would re-introduce
// the same attack surface in the same pod.
func TestBuildDeploymentNoServiceAccountTokenWithLMCache(t *testing.T) {
	e := baseEffectiveConfig()
	e.KVOffloadBackend = "lmcache"
	e.KVOffloadSize = 32

	dep := buildTestDeployment(e)
	got := dep.Spec.Template.Spec.AutomountServiceAccountToken
	if got == nil || *got {
		t.Errorf("AutomountServiceAccountToken with LMCache sidecar: got %v, want pointer-to-false (issue #74)", got)
	}
}

// TestBuildDeploymentVLLMContainerSecurityContext verifies the vLLM container
// itself has AllowPrivilegeEscalation=false and drops ALL capabilities — both
// requirements of the restricted PodSecurity admission profile.
func TestBuildDeploymentVLLMContainerSecurityContext(t *testing.T) {
	dep := buildTestDeployment(baseEffectiveConfig())
	containers := dep.Spec.Template.Spec.Containers
	if len(containers) == 0 {
		t.Fatal("expected at least one container")
	}
	assertContainerHardened(t, "vllm", containers[0])
}

// TestBuildDeploymentLMCacheSidecarSecurityContext verifies the LMCache
// sidecar is hardened identically to the vLLM container. A sidecar without the
// same baseline lets the whole pod be rejected by restricted PSA.
func TestBuildDeploymentLMCacheSidecarSecurityContext(t *testing.T) {
	e := baseEffectiveConfig()
	e.KVOffloadBackend = "lmcache"
	e.KVOffloadSize = 32

	dep := buildTestDeployment(e)
	containers := dep.Spec.Template.Spec.Containers
	if len(containers) < 2 {
		t.Fatalf("expected 2 containers (vllm + lmcache); got %d: %v", len(containers), containerNames(containers))
	}
	assertContainerHardened(t, "lmcache", containers[1])
}

func assertContainerHardened(t *testing.T, label string, c corev1.Container) {
	t.Helper()
	if c.SecurityContext == nil {
		t.Fatalf("%s: container-level SecurityContext is nil", label)
	}
	if c.SecurityContext.AllowPrivilegeEscalation == nil || *c.SecurityContext.AllowPrivilegeEscalation {
		t.Errorf("%s: AllowPrivilegeEscalation: got %v, want false", label, c.SecurityContext.AllowPrivilegeEscalation)
	}
	if c.SecurityContext.Capabilities == nil {
		t.Fatalf("%s: Capabilities must be set", label)
	}
	dropsAll := false
	for _, cap := range c.SecurityContext.Capabilities.Drop {
		if cap == "ALL" {
			dropsAll = true
			break
		}
	}
	if !dropsAll {
		t.Errorf("%s: Capabilities.Drop must include ALL; got %v", label, c.SecurityContext.Capabilities.Drop)
	}
}

// TestBuildDeploymentLMCacheEmptyDirSizeLimit verifies the lmcache-data
// emptyDir has a sizeLimit. Without one, an over-eager LMCache could fill the
// node's ephemeral storage and trigger kubelet eviction of every other pod on
// the node.
func TestBuildDeploymentLMCacheEmptyDirSizeLimit(t *testing.T) {
	t.Run("uses_KVOffloadSize_when_set", func(t *testing.T) {
		e := baseEffectiveConfig()
		e.KVOffloadBackend = "lmcache"
		e.KVOffloadSize = 64

		vol := findVolume(buildTestDeployment(e), LMCacheDataVolume)
		if vol == nil {
			t.Fatal("lmcache-data volume not found")
		}
		if vol.EmptyDir == nil {
			t.Fatal("lmcache-data must be an emptyDir")
		}
		if vol.EmptyDir.SizeLimit == nil {
			t.Fatal("lmcache-data emptyDir.sizeLimit is nil; an unbounded emptyDir can fill node ephemeral storage")
		}
		want := resource.MustParse("64Gi")
		if vol.EmptyDir.SizeLimit.Cmp(want) != 0 {
			t.Errorf("sizeLimit: got %s, want %s", vol.EmptyDir.SizeLimit.String(), want.String())
		}
	})

	t.Run("falls_back_to_default_when_KVOffloadSize_zero", func(t *testing.T) {
		e := baseEffectiveConfig()
		e.KVOffloadBackend = "lmcache"
		e.KVOffloadSize = 0 // preset leaves it unset → LMCache picks its own buffer

		vol := findVolume(buildTestDeployment(e), LMCacheDataVolume)
		if vol == nil {
			t.Fatal("lmcache-data volume not found")
		}
		if vol.EmptyDir == nil || vol.EmptyDir.SizeLimit == nil {
			t.Fatal("lmcache-data emptyDir must always have a sizeLimit, even when KVOffloadSize is unset")
		}
	})
}

// findVolume looks up a Volume by name on the rendered Deployment's pod
// template. Returns nil if not present.
func findVolume(dep *appsv1.Deployment, name string) *corev1.Volume {
	for i := range dep.Spec.Template.Spec.Volumes {
		v := &dep.Spec.Template.Spec.Volumes[i]
		if v.Name == name {
			return v
		}
	}
	return nil
}
