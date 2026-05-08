package vllm

import (
	"strings"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// TestBuildDeploymentNoAPIKeyByDefault is the regression guard for the
// "API-key opt-in" property of issue #75. When apiKey is nil, the rendered
// pod must NOT carry an api-key volume, must NOT mount one, and must run with
// the upstream image's default ENTRYPOINT (no shell wrapper that would
// otherwise read a non-existent file and crash the container).
func TestBuildDeploymentNoAPIKeyByDefault(t *testing.T) {
	dep := buildTestDeployment(baseEffectiveConfig())
	c := dep.Spec.Template.Spec.Containers[0]

	if findVolume(dep, APIKeyVolumeName) != nil {
		t.Errorf("api-key volume must not be present when apiKey is nil; volumes: %v",
			volumeNames(dep.Spec.Template.Spec.Volumes))
	}
	for _, m := range c.VolumeMounts {
		if m.Name == APIKeyVolumeName {
			t.Errorf("api-key volume mount must not be present; got %+v", m)
		}
	}
	if len(c.Command) != 0 {
		t.Errorf("Command must be empty when apiKey is nil so the upstream "+
			"image ENTRYPOINT runs unchanged; got %v", c.Command)
	}
}

// TestBuildDeploymentAPIKeyVolume asserts the projected secret volume has the
// right SecretName, mode 0400, and key→path mapping when apiKey is set.
func TestBuildDeploymentAPIKeyVolume(t *testing.T) {
	apiKey := &corev1.SecretKeySelector{
		LocalObjectReference: corev1.LocalObjectReference{Name: "vllm-api-secret"},
		Key:                  "token",
	}
	dep := buildDeploymentWithAPIKey(t, baseEffectiveConfig(), apiKey)

	vol := findVolume(dep, APIKeyVolumeName)
	if vol == nil {
		t.Fatalf("api-key volume not found; volumes: %v", volumeNames(dep.Spec.Template.Spec.Volumes))
	}
	if vol.Secret == nil {
		t.Fatalf("api-key volume must be a Secret source; got: %+v", vol.VolumeSource)
	}
	if vol.Secret.SecretName != "vllm-api-secret" {
		t.Errorf("secretName: got %q, want vllm-api-secret", vol.Secret.SecretName)
	}
	if vol.Secret.DefaultMode == nil || *vol.Secret.DefaultMode != APIKeyFileMode {
		t.Errorf("defaultMode: got %v, want %d (0400)", vol.Secret.DefaultMode, APIKeyFileMode)
	}
	if len(vol.Secret.Items) != 1 ||
		vol.Secret.Items[0].Key != "token" ||
		vol.Secret.Items[0].Path != APIKeyFileName {
		t.Errorf("Items: got %+v, want [{Key:\"token\", Path:%q}]",
			vol.Secret.Items, APIKeyFileName)
	}
}

// TestBuildDeploymentAPIKeyMount asserts the vLLM container mounts the
// projected secret read-only at /var/run/vllm.
func TestBuildDeploymentAPIKeyMount(t *testing.T) {
	apiKey := &corev1.SecretKeySelector{
		LocalObjectReference: corev1.LocalObjectReference{Name: "vllm-api-secret"},
		Key:                  "token",
	}
	dep := buildDeploymentWithAPIKey(t, baseEffectiveConfig(), apiKey)
	c := dep.Spec.Template.Spec.Containers[0]

	var got *corev1.VolumeMount
	for i, m := range c.VolumeMounts {
		if m.Name == APIKeyVolumeName {
			got = &c.VolumeMounts[i]
			break
		}
	}
	if got == nil {
		t.Fatalf("vLLM container missing api-key VolumeMount; mounts: %+v", c.VolumeMounts)
	}
	if got.MountPath != APIKeyMountDir {
		t.Errorf("mountPath: got %q, want %q", got.MountPath, APIKeyMountDir)
	}
	if !got.ReadOnly {
		t.Errorf("api-key mount must be ReadOnly")
	}
}

// TestBuildDeploymentAPIKeyCommandWrapper asserts the Command override is
// installed only when apiKey is set, and that the wrapper:
//   - cats the projected file (so the secret never lands on argv directly),
//   - exec's vllm so SIGTERM propagates,
//   - appends --api-key=$KEY using the env var, NOT the literal value.
func TestBuildDeploymentAPIKeyCommandWrapper(t *testing.T) {
	apiKey := &corev1.SecretKeySelector{
		LocalObjectReference: corev1.LocalObjectReference{Name: "vllm-api-secret"},
		Key:                  "token",
	}
	dep := buildDeploymentWithAPIKey(t, baseEffectiveConfig(), apiKey)
	c := dep.Spec.Template.Spec.Containers[0]

	if len(c.Command) == 0 {
		t.Fatalf("Command must be set when apiKey is configured; args=%v", c.Args)
	}
	if c.Command[0] != "sh" {
		t.Errorf("Command[0]: got %q, want sh", c.Command[0])
	}
	// The script must not embed the secret value — only its file path.
	if len(c.Command) < 3 {
		t.Fatalf("Command shell wrapper must have at least 3 entries; got %v", c.Command)
	}
	script := c.Command[2]
	if !strings.Contains(script, APIKeyMountPath) {
		t.Errorf("script must reference the projected file path %q; got %q", APIKeyMountPath, script)
	}
	if !strings.Contains(script, "exec ") {
		t.Errorf("script must exec the vLLM entrypoint (signal propagation); got %q", script)
	}
	if !strings.Contains(script, `--api-key="$KEY"`) {
		t.Errorf("script must append --api-key=$KEY (using env var, not literal); got %q", script)
	}
}

// TestBuildDeploymentAPIKeyArgsPreserved asserts that when apiKey is set,
// the model-config args (--model, --max-model-len, etc.) still flow through
// to the container Args, since the shell wrapper takes the literal argv via
// "$@" and appends --api-key after.
func TestBuildDeploymentAPIKeyArgsPreserved(t *testing.T) {
	apiKey := &corev1.SecretKeySelector{
		LocalObjectReference: corev1.LocalObjectReference{Name: "vllm-api-secret"},
		Key:                  "token",
	}
	dep := buildDeploymentWithAPIKey(t, baseEffectiveConfig(), apiKey)
	c := dep.Spec.Template.Spec.Containers[0]

	// --model and --max-model-len are produced by buildArgs from the
	// EffectiveConfig; both must be present on c.Args (the wrapper passes
	// them through "$@").
	if flagIndex(c.Args, "--model") < 0 {
		t.Errorf("--model must remain in container Args; got %v", c.Args)
	}
	if flagIndex(c.Args, "--max-model-len") < 0 {
		t.Errorf("--max-model-len must remain in container Args; got %v", c.Args)
	}
	// And critically, the api-key value must NOT appear on argv (the file
	// content is read by the shell wrapper, never by the operator).
	for _, a := range c.Args {
		if a == "--api-key" || strings.HasPrefix(a, "--api-key=") {
			t.Errorf("--api-key must not be on container.Args (it's appended by the "+
				"shell wrapper from the secret file); got %v", c.Args)
		}
	}
}

// buildDeploymentWithAPIKey is the API-key-aware sibling of buildTestDeployment
// — it threads a non-nil apiKey through BuildDeployment so the tests can
// inspect the rendered Pod.
func buildDeploymentWithAPIKey(t *testing.T, e EffectiveConfig, apiKey *corev1.SecretKeySelector) *appsv1.Deployment {
	t.Helper()
	return BuildDeployment(
		"test-instance",
		"default",
		1,
		e,
		"test-pvc",
		corev1.SecretKeySelector{
			LocalObjectReference: corev1.LocalObjectReference{Name: "hf-secret"},
			Key:                  "token",
		},
		apiKey,
		metav1.OwnerReference{},
	)
}
