package vllm

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	corev1 "k8s.io/api/core/v1"

	vllmv1alpha1 "github.com/aatchison/deploy-vllm-k8s/operator/api/v1alpha1"
)

// Constant defaults applied when neither preset nor override provides a value.
const (
	DefaultImage           = "docker.io/library/vllm-gemma4:local"
	DefaultImagePullPolicy = "Never"
	DefaultHFHome          = "/models/huggingface"
	ContainerName          = "vllm"
	HTTPPort               = 8000

	// ContainerHome is the value injected as $HOME for the vLLM container.
	// The base image lacks a passwd entry for the running uid (issue #103),
	// so $HOME would otherwise be unset and library lookups fall through to
	// pwd.getpwuid(). /tmp is writable by any uid under restricted PSA.
	ContainerHome = "/tmp"
	// ContainerUser is the value injected as $USER for the vLLM container.
	// Python's getpass.getuser() returns the first non-empty value of
	// $LOGNAME, $USER, $LNAME, $USERNAME before falling back to getpwuid.
	// Setting this short-circuits every torch caller of getpass.getuser()
	// (issue #103), not just the ones that read TORCHINDUCTOR_CACHE_DIR.
	// The value itself is cosmetic — torch only uses it to build a
	// per-user cache directory name; "vllm" matches the convention used
	// by the Dockerfile's passwd entry.
	ContainerUser = "vllm"
	// TorchInductorCacheDir is the path set on TORCHINDUCTOR_CACHE_DIR so
	// torch's cache_dir_utils.py short-circuits its lazy init before calling
	// getpwuid at module-import time (issue #103). The cache is ephemeral
	// and is rebuilt on each pod start, so /tmp is the safe default.
	TorchInductorCacheDir = "/tmp/torch-inductor"

	// LMCacheImage is the sidecar image used when KVOffloadBackend == "lmcache".
	// Pin to a specific release tag; bump via separate PR when upstream releases.
	LMCacheImage = "lmcache/lmcache:v0.4.0"
	// LMCacheContainerName is the name of the LMCache sidecar container.
	LMCacheContainerName = "lmcache"
	// LMCacheDataVolume is the emptyDir volume shared between vLLM and LMCache.
	LMCacheDataVolume = "lmcache-data"
	// LMCacheDataMount is the mount path for the shared lmcache-data volume.
	LMCacheDataMount = "/lmcache-data"
	// LMCacheAdminPort is the TCP port LMCache listens on for health/admin.
	// Must not collide with HTTPPort (8000). LMCache upstream defaults to 9000
	// for its management interface.
	LMCacheAdminPort = 9000

	// ManagedByLabelKey is the standard "app.kubernetes.io/managed-by" label
	// key. Stamped on every operator-applied Deployment + Service so the
	// controller-runtime informer cache (which is scoped to this label in
	// main.go, issue #83) actually observes the resources we own.
	ManagedByLabelKey = "app.kubernetes.io/managed-by"
	// ManagedByLabelValue is the value used in the managed-by label. Must
	// match the selector built in main.go's cache.Options.ByObject map —
	// changing one without the other silently breaks the cache.
	ManagedByLabelValue = "vllm-operator"
)

// EffectiveConfig is the fully-resolved vLLM configuration after merging
// preset + overrides. Kept flat (no maps) so json.Marshal is byte-stable
// and sha256 of the output is a useful drift indicator.
//
// Optional fields added after the initial schema (KVCacheDtype,
// EnablePrefixCaching, ServedModelName, CPUOffloadGiB,
// MaxNumBatchedTokens, EnableChunkedPrefill, StartupProbe,
// KVOffloadBackend, KVOffloadSize) carry omitempty
// so any path that doesn't set them produces JSON identical to the pre-field
// shape — keeping the resolved-config-hash stable for existing instances.
type EffectiveConfig struct {
	ModelID                 string                    `json:"modelID"`
	Image                   string                    `json:"image"`
	ImagePullPolicy         string                    `json:"imagePullPolicy"`
	MIGResource             string                    `json:"migResource"`
	MIGResourceCount        int32                     `json:"migResourceCount"`
	Quantization            string                    `json:"quantization,omitempty"`
	DType                   string                    `json:"dtype,omitempty"`
	ServedModelName         string                    `json:"servedModelName,omitempty"`
	MaxModelLen             int32                     `json:"maxModelLen"`
	GPUMemoryUtilization    string                    `json:"gpuMemoryUtilization"`
	TensorParallelSize      int32                     `json:"tensorParallelSize"`
	EnableAutoToolChoice    bool                      `json:"enableAutoToolChoice"`
	ToolCallParser          string                    `json:"toolCallParser,omitempty"`
	SHMSizeLimit            string                    `json:"shmSizeLimit"`
	ProgressDeadlineSeconds int32                     `json:"progressDeadlineSeconds"`
	LivenessProbe           vllmv1alpha1.ProbeConfig  `json:"livenessProbe"`
	ReadinessProbe          vllmv1alpha1.ProbeConfig  `json:"readinessProbe"`
	StartupProbe            *vllmv1alpha1.ProbeConfig `json:"startupProbe,omitempty"`
	KVCacheDtype            string                    `json:"kvCacheDtype,omitempty"`
	EnablePrefixCaching     *bool                     `json:"enablePrefixCaching,omitempty"`
	CPUOffloadGiB           int32                     `json:"cpuOffloadGiB,omitempty"`
	MaxNumBatchedTokens     int32                     `json:"maxNumBatchedTokens,omitempty"`
	EnableChunkedPrefill    bool                      `json:"enableChunkedPrefill,omitempty"`
	KVOffloadBackend        string                    `json:"kvOffloadBackend,omitempty"`
	KVOffloadSize           int32                     `json:"kvOffloadSize,omitempty"`
	EnableLora              *bool                     `json:"enableLora,omitempty"`
	LoraModules             string                    `json:"loraModules,omitempty"`
	MaxLoraRank             int                       `json:"maxLoraRank,omitempty"`
	// PVCReadOnly, when true, causes BuildDeployment to mark the /models
	// VolumeMount readOnly. Default false preserves current write-cache
	// behavior. omitempty keeps the resolved-config-hash stable for instances
	// that don't opt in.
	PVCReadOnly            bool   `json:"pvcReadOnly,omitempty"`
	ReasoningParser        string `json:"reasoningParser,omitempty"`
	ChatTemplate           string `json:"chatTemplate,omitempty"`
	SpeculativeConfig      string `json:"speculativeConfig,omitempty"`
	LimitMmPerPrompt       string `json:"limitMmPerPrompt,omitempty"`
	MaxNumSeqs             int32  `json:"maxNumSeqs,omitempty"`
	KVCacheDtypeSkipLayers string `json:"kvCacheDtypeSkipLayers,omitempty"`
	MambaCacheMode         string `json:"mambaCacheMode,omitempty"`

	MambaBackend           string          `json:"mambaBackend,omitempty"`
	EnforceEager           *bool           `json:"enforceEager,omitempty"`
	CompilationConfig      string          `json:"compilationConfig,omitempty"`
	Env                    []corev1.EnvVar `json:"env,omitempty"`
	ModelRevision          string          `json:"modelRevision,omitempty"`
	CodeRevision           string          `json:"codeRevision,omitempty"`
	TokenizerRevision      string          `json:"tokenizerRevision,omitempty"`
	TrustRemoteCode        bool            `json:"trustRemoteCode,omitempty"`
	DisableCustomAllReduce bool            `json:"disableCustomAllReduce,omitempty"`
	FlashinferAutotune     *bool           `json:"flashinferAutotune,omitempty"`
	EngramConfig           string          `json:"engramConfig,omitempty"`
}

// HashConfig returns the sha256 hex digest of the canonical JSON encoding of
// e. Used by callers that mutate an EffectiveConfig after Resolve (e.g. to
// apply spec-level fields such as PVCReadOnly) and need a fresh hash for the
// instance status.
func HashConfig(e EffectiveConfig) (string, error) {
	buf, err := json.Marshal(e)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(buf)
	return hex.EncodeToString(sum[:]), nil
}

// Resolve merges overrides onto a preset (may be nil if overrides are complete)
// plus controller-level constant defaults. Returns the resolved config and
// a sha256 hex digest of its canonical JSON.
func Resolve(preset *vllmv1alpha1.ModelPresetSpec, overrides *vllmv1alpha1.ModelConfigOverrides) (EffectiveConfig, string, error) {
	var e EffectiveConfig

	if preset != nil {
		e = EffectiveConfig{
			ModelID:                 preset.ModelID,
			ChatTemplate:            preset.ChatTemplate,
			ReasoningParser:         preset.ReasoningParser,
			Image:                   preset.Image,
			ImagePullPolicy:         preset.ImagePullPolicy,
			MIGResource:             preset.MIGResource,
			MIGResourceCount:        preset.MIGResourceCount,
			Quantization:            preset.Quantization,
			DType:                   preset.DType,
			ServedModelName:         preset.ServedModelName,
			MaxModelLen:             preset.MaxModelLen,
			GPUMemoryUtilization:    preset.GPUMemoryUtilization,
			TensorParallelSize:      preset.TensorParallelSize,
			EnableAutoToolChoice:    preset.EnableAutoToolChoice,
			ToolCallParser:          preset.ToolCallParser,
			SHMSizeLimit:            preset.SHMSizeLimit,
			ProgressDeadlineSeconds: preset.ProgressDeadlineSeconds,
			LivenessProbe:           preset.LivenessProbe,
			ReadinessProbe:          preset.ReadinessProbe,
			StartupProbe:            preset.StartupProbe,
			MaxNumBatchedTokens:     preset.MaxNumBatchedTokens,
			EnableChunkedPrefill:    preset.EnableChunkedPrefill,
			LoraModules:             preset.LoraModules,
		}
	}

	if preset != nil {
		if preset.EnableLora != nil {
			v := *preset.EnableLora
			e.EnableLora = &v
		}
		if preset.MaxLoraRank != nil {
			e.MaxLoraRank = *preset.MaxLoraRank
		}
	}
	if overrides != nil {
		if overrides.ChatTemplate != nil {
			e.ChatTemplate = *overrides.ChatTemplate
		}
		if overrides.ReasoningParser != nil {
			e.ReasoningParser = *overrides.ReasoningParser
		}
		if overrides.ModelID != nil {
			e.ModelID = *overrides.ModelID
		}
		if overrides.Image != nil {
			e.Image = *overrides.Image
		}
		if overrides.ImagePullPolicy != nil {
			e.ImagePullPolicy = *overrides.ImagePullPolicy
		}
		if overrides.MIGResource != nil {
			e.MIGResource = *overrides.MIGResource
		}
		if overrides.MIGResourceCount != nil {
			e.MIGResourceCount = *overrides.MIGResourceCount
		}
		if overrides.Quantization != nil {
			e.Quantization = *overrides.Quantization
		}
		if overrides.DType != nil {
			e.DType = *overrides.DType
		}
		if overrides.ServedModelName != nil {
			e.ServedModelName = *overrides.ServedModelName
		}
		if overrides.MaxModelLen != nil {
			e.MaxModelLen = *overrides.MaxModelLen
		}
		if overrides.GPUMemoryUtilization != nil {
			e.GPUMemoryUtilization = *overrides.GPUMemoryUtilization
		}
		if overrides.TensorParallelSize != nil {
			e.TensorParallelSize = *overrides.TensorParallelSize
		}
		if overrides.EnableAutoToolChoice != nil {
			e.EnableAutoToolChoice = *overrides.EnableAutoToolChoice
		}
		if overrides.ToolCallParser != nil {
			e.ToolCallParser = *overrides.ToolCallParser
		}
		if overrides.SHMSizeLimit != nil {
			e.SHMSizeLimit = *overrides.SHMSizeLimit
		}
		if overrides.ProgressDeadlineSeconds != nil {
			e.ProgressDeadlineSeconds = *overrides.ProgressDeadlineSeconds
		}
		if overrides.LivenessProbe != nil {
			e.LivenessProbe = *overrides.LivenessProbe
		}
		if overrides.ReadinessProbe != nil {
			e.ReadinessProbe = *overrides.ReadinessProbe
		}
		if overrides.StartupProbe != nil {
			e.StartupProbe = overrides.StartupProbe
		}
		if overrides.MaxNumBatchedTokens != nil {
			e.MaxNumBatchedTokens = *overrides.MaxNumBatchedTokens
		}
		if overrides.EnableChunkedPrefill != nil {
			e.EnableChunkedPrefill = *overrides.EnableChunkedPrefill
		}
		if overrides.EnableLora != nil {
			v := *overrides.EnableLora
			e.EnableLora = &v
		}
		if overrides.LoraModules != nil {
			e.LoraModules = *overrides.LoraModules
		}
		if overrides.MaxLoraRank != nil {
			e.MaxLoraRank = int(*overrides.MaxLoraRank)
		}
		if overrides.PVCReadOnly != nil {
			e.PVCReadOnly = *overrides.PVCReadOnly
		}
	}

	if e.Image == "" {
		e.Image = DefaultImage
	}
	if e.ImagePullPolicy == "" {
		e.ImagePullPolicy = DefaultImagePullPolicy
	}
	if e.TensorParallelSize == 0 {
		e.TensorParallelSize = 1
	}
	if e.MIGResourceCount == 0 {
		e.MIGResourceCount = 1
	}

	buf, err := json.Marshal(e)
	if err != nil {
		return EffectiveConfig{}, "", err
	}
	sum := sha256.Sum256(buf)
	return e, hex.EncodeToString(sum[:]), nil
}

// MergeEnv copies env lists in order. Later values replace earlier values at
// the first occurrence's position; new names append. It never aliases inputs.
func MergeEnv(lists ...[]corev1.EnvVar) []corev1.EnvVar {
	var result []corev1.EnvVar
	positions := make(map[string]int)
	for _, list := range lists {
		for _, env := range list {
			value := *env.DeepCopy()
			if i, ok := positions[env.Name]; ok {
				result[i] = value
			} else {
				positions[env.Name] = len(result)
				result = append(result, value)
			}
		}
	}
	return result
}

// ResolveLongContextInstance includes spec-level env in the resolved config and
// hash so env edits trigger the same drift tracking as other configuration.
func ResolveLongContextInstance(preset *vllmv1alpha1.LongContextPresetSpec, spec vllmv1alpha1.LongContextInstanceSpec) (EffectiveConfig, string, error) {
	e, _, err := ResolveLongContext(preset, spec.Overrides)
	if err != nil {
		return e, "", err
	}
	e.Env = MergeEnv(e.Env, spec.Env)
	hash, err := HashConfig(e)
	return e, hash, err
}

// ResolveLongContext is the LongContextPreset/LongContextInstance sibling of
// Resolve. It carries the standard fields onto an EffectiveConfig and then
// applies the two long-context-specific fields (KVCacheDtype,
// EnablePrefixCaching). The returned EffectiveConfig is fed to BuildDeployment
// exactly like the standard path, and buildArgs emits the two extra vLLM
// flags conditionally.
func ResolveLongContext(preset *vllmv1alpha1.LongContextPresetSpec, overrides *vllmv1alpha1.LongContextOverrides) (EffectiveConfig, string, error) {
	var e EffectiveConfig

	if preset != nil {
		e = EffectiveConfig{
			Env:                     MergeEnv(preset.Env),
			ModelID:                 preset.ModelID,
			MambaBackend:            preset.MambaBackend,
			MambaCacheMode:          preset.MambaCacheMode,
			KVCacheDtypeSkipLayers:  preset.KVCacheDtypeSkipLayers,
			MaxNumSeqs:              preset.MaxNumSeqs,
			LimitMmPerPrompt:        preset.LimitMmPerPrompt,
			SpeculativeConfig:       preset.SpeculativeConfig,
			ChatTemplate:            preset.ChatTemplate,
			ReasoningParser:         preset.ReasoningParser,
			Image:                   preset.Image,
			ImagePullPolicy:         preset.ImagePullPolicy,
			MIGResource:             preset.MIGResource,
			MIGResourceCount:        preset.MIGResourceCount,
			Quantization:            preset.Quantization,
			DType:                   preset.DType,
			ServedModelName:         preset.ServedModelName,
			MaxModelLen:             preset.MaxModelLen,
			GPUMemoryUtilization:    preset.GPUMemoryUtilization,
			TensorParallelSize:      preset.TensorParallelSize,
			EnableAutoToolChoice:    preset.EnableAutoToolChoice,
			ToolCallParser:          preset.ToolCallParser,
			SHMSizeLimit:            preset.SHMSizeLimit,
			ProgressDeadlineSeconds: preset.ProgressDeadlineSeconds,
			LivenessProbe:           preset.LivenessProbe,
			ReadinessProbe:          preset.ReadinessProbe,
			StartupProbe:            preset.StartupProbe,
			KVCacheDtype:            preset.KVCacheDtype,
			CPUOffloadGiB:           preset.CPUOffloadGiB,
			MaxNumBatchedTokens:     preset.MaxNumBatchedTokens,
			EnableChunkedPrefill:    preset.EnableChunkedPrefill,
			KVOffloadBackend:        preset.KVOffloadBackend,
			KVOffloadSize:           preset.KVOffloadSize,
			LoraModules:             preset.LoraModules,
		}
		if preset.EnablePrefixCaching != nil {
			v := *preset.EnablePrefixCaching
			e.EnablePrefixCaching = &v
		}
	}

	if preset != nil {
		if preset.EnforceEager != nil {
			v := *preset.EnforceEager
			e.EnforceEager = &v
		}
		if preset.CompilationConfig != nil {
			e.CompilationConfig = *preset.CompilationConfig
		}
	}
	if preset != nil {
		if preset.EnableLora != nil {
			v := *preset.EnableLora
			e.EnableLora = &v
		}
		if preset.MaxLoraRank != nil {
			e.MaxLoraRank = *preset.MaxLoraRank
		}
	}
	if overrides != nil {
		if overrides.EnforceEager != nil {
			v := *overrides.EnforceEager
			e.EnforceEager = &v
		}
		if overrides.CompilationConfig != nil {
			e.CompilationConfig = *overrides.CompilationConfig
		}
		if overrides.MambaBackend != nil {
			e.MambaBackend = *overrides.MambaBackend
		}
		if overrides.MambaCacheMode != nil {
			e.MambaCacheMode = *overrides.MambaCacheMode
		}
		if overrides.SpeculativeConfig != nil {
			e.SpeculativeConfig = *overrides.SpeculativeConfig
		}
		if overrides.ChatTemplate != nil {
			e.ChatTemplate = *overrides.ChatTemplate
		}
		if overrides.ReasoningParser != nil {
			e.ReasoningParser = *overrides.ReasoningParser
		}
		if overrides.ModelID != nil {
			e.ModelID = *overrides.ModelID
		}
		if overrides.Image != nil {
			e.Image = *overrides.Image
		}
		if overrides.ImagePullPolicy != nil {
			e.ImagePullPolicy = *overrides.ImagePullPolicy
		}
		if overrides.MIGResource != nil {
			e.MIGResource = *overrides.MIGResource
		}
		if overrides.MIGResourceCount != nil {
			e.MIGResourceCount = *overrides.MIGResourceCount
		}
		if overrides.Quantization != nil {
			e.Quantization = *overrides.Quantization
		}
		if overrides.DType != nil {
			e.DType = *overrides.DType
		}
		if overrides.ServedModelName != nil {
			e.ServedModelName = *overrides.ServedModelName
		}
		if overrides.MaxModelLen != nil {
			e.MaxModelLen = *overrides.MaxModelLen
		}
		if overrides.GPUMemoryUtilization != nil {
			e.GPUMemoryUtilization = *overrides.GPUMemoryUtilization
		}
		if overrides.TensorParallelSize != nil {
			e.TensorParallelSize = *overrides.TensorParallelSize
		}
		if overrides.EnableAutoToolChoice != nil {
			e.EnableAutoToolChoice = *overrides.EnableAutoToolChoice
		}
		if overrides.ToolCallParser != nil {
			e.ToolCallParser = *overrides.ToolCallParser
		}
		if overrides.SHMSizeLimit != nil {
			e.SHMSizeLimit = *overrides.SHMSizeLimit
		}
		if overrides.ProgressDeadlineSeconds != nil {
			e.ProgressDeadlineSeconds = *overrides.ProgressDeadlineSeconds
		}
		if overrides.LivenessProbe != nil {
			e.LivenessProbe = *overrides.LivenessProbe
		}
		if overrides.ReadinessProbe != nil {
			e.ReadinessProbe = *overrides.ReadinessProbe
		}
		if overrides.StartupProbe != nil {
			e.StartupProbe = overrides.StartupProbe
		}
		if overrides.KVCacheDtype != nil {
			e.KVCacheDtype = *overrides.KVCacheDtype
		}
		if overrides.EnablePrefixCaching != nil {
			v := *overrides.EnablePrefixCaching
			e.EnablePrefixCaching = &v
		}
		if overrides.CPUOffloadGiB != nil {
			e.CPUOffloadGiB = *overrides.CPUOffloadGiB
		}
		if overrides.MaxNumBatchedTokens != nil {
			e.MaxNumBatchedTokens = *overrides.MaxNumBatchedTokens
		}
		if overrides.EnableChunkedPrefill != nil {
			e.EnableChunkedPrefill = *overrides.EnableChunkedPrefill
		}
		if overrides.KVOffloadBackend != nil {
			e.KVOffloadBackend = *overrides.KVOffloadBackend
		}
		if overrides.KVOffloadSize != nil {
			e.KVOffloadSize = *overrides.KVOffloadSize
		}
		if overrides.EnableLora != nil {
			v := *overrides.EnableLora
			e.EnableLora = &v
		}
		if overrides.LoraModules != nil {
			e.LoraModules = *overrides.LoraModules
		}
		if overrides.MaxLoraRank != nil {
			e.MaxLoraRank = int(*overrides.MaxLoraRank)
		}
		if overrides.PVCReadOnly != nil {
			e.PVCReadOnly = *overrides.PVCReadOnly
		}
	}

	mergeLongContextFlags(&e, preset, overrides)

	if e.Image == "" {
		e.Image = DefaultImage
	}
	if e.ImagePullPolicy == "" {
		e.ImagePullPolicy = DefaultImagePullPolicy
	}
	if e.TensorParallelSize == 0 {
		e.TensorParallelSize = 1
	}
	if e.MIGResourceCount == 0 {
		e.MIGResourceCount = 1
	}

	buf, err := json.Marshal(e)
	if err != nil {
		return EffectiveConfig{}, "", err
	}
	sum := sha256.Sum256(buf)
	return e, hex.EncodeToString(sum[:]), nil
}

// mergeLongContextFlags applies the issue #178 fields without changing standard presets.
func mergeLongContextFlags(e *EffectiveConfig, p *vllmv1alpha1.LongContextPresetSpec, o *vllmv1alpha1.LongContextOverrides) {
	if p != nil {
		e.ModelRevision = p.ModelRevision
		e.CodeRevision = p.CodeRevision
		e.TokenizerRevision = p.TokenizerRevision
		e.TrustRemoteCode = p.TrustRemoteCode
		e.DisableCustomAllReduce = p.DisableCustomAllReduce
		if p.FlashinferAutotune != nil {
			v := *p.FlashinferAutotune
			e.FlashinferAutotune = &v
		}
		e.EngramConfig = p.EngramConfig
	}
	if o != nil {
		if o.ModelRevision != nil {
			e.ModelRevision = *o.ModelRevision
		}
		if o.CodeRevision != nil {
			e.CodeRevision = *o.CodeRevision
		}
		if o.TokenizerRevision != nil {
			e.TokenizerRevision = *o.TokenizerRevision
		}
		if o.TrustRemoteCode != nil {
			e.TrustRemoteCode = *o.TrustRemoteCode
		}
		if o.DisableCustomAllReduce != nil {
			e.DisableCustomAllReduce = *o.DisableCustomAllReduce
		}
		if o.FlashinferAutotune != nil {
			v := *o.FlashinferAutotune
			e.FlashinferAutotune = &v
		}
		if o.EngramConfig != nil {
			e.EngramConfig = *o.EngramConfig
		}
		if o.MaxNumSeqs != nil {
			e.MaxNumSeqs = *o.MaxNumSeqs
		}
	}
}
