package vllm

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"

	vllmv1alpha1 "github.com/aatchison/deploy-vllm-k8s/operator/api/v1alpha1"
)

// Constant defaults applied when neither preset nor override provides a value.
const (
	DefaultImage           = "docker.io/library/vllm-gemma4:local"
	DefaultImagePullPolicy = "Never"
	DefaultHFHome          = "/models/huggingface"
	ContainerName          = "vllm"
	HTTPPort               = 8000
	LivenessPeriodSeconds  = 30
	LivenessFailureThresh  = 10

	// LMCacheImage is the sidecar image used when KVOffloadBackend == "lmcache".
	// Pin to a specific release tag; bump via separate PR when upstream releases.
	LMCacheImage          = "lmcache/lmcache:v0.4.0"
	// LMCacheContainerName is the name of the LMCache sidecar container.
	LMCacheContainerName  = "lmcache"
	// LMCacheDataVolume is the emptyDir volume shared between vLLM and LMCache.
	LMCacheDataVolume     = "lmcache-data"
	// LMCacheDataMount is the mount path for the shared lmcache-data volume.
	LMCacheDataMount      = "/lmcache-data"
	// LMCacheAdminPort is the TCP port LMCache listens on for health/admin.
	LMCacheAdminPort      = 8000
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
	ModelID                 string                   `json:"modelID"`
	Image                   string                   `json:"image"`
	ImagePullPolicy         string                   `json:"imagePullPolicy"`
	MIGResource             string                   `json:"migResource"`
	MIGResourceCount        int32                    `json:"migResourceCount"`
	Quantization            string                   `json:"quantization,omitempty"`
	DType                   string                   `json:"dtype,omitempty"`
	ServedModelName         string                   `json:"servedModelName,omitempty"`
	MaxModelLen             int32                    `json:"maxModelLen"`
	GPUMemoryUtilization    string                   `json:"gpuMemoryUtilization"`
	TensorParallelSize      int32                    `json:"tensorParallelSize"`
	EnableAutoToolChoice    bool                     `json:"enableAutoToolChoice"`
	ToolCallParser          string                   `json:"toolCallParser,omitempty"`
	SHMSizeLimit            string                   `json:"shmSizeLimit"`
	ProgressDeadlineSeconds int32                    `json:"progressDeadlineSeconds"`
	LivenessProbe           vllmv1alpha1.ProbeConfig  `json:"livenessProbe"`
	ReadinessProbe          vllmv1alpha1.ProbeConfig  `json:"readinessProbe"`
	StartupProbe            *vllmv1alpha1.ProbeConfig `json:"startupProbe,omitempty"`
	KVCacheDtype            string                   `json:"kvCacheDtype,omitempty"`
	EnablePrefixCaching     *bool                    `json:"enablePrefixCaching,omitempty"`
	CPUOffloadGiB           int32                    `json:"cpuOffloadGiB,omitempty"`
	MaxNumBatchedTokens     int32                    `json:"maxNumBatchedTokens,omitempty"`
	EnableChunkedPrefill    bool                     `json:"enableChunkedPrefill,omitempty"`
	KVOffloadBackend        string                   `json:"kvOffloadBackend,omitempty"`
	KVOffloadSize           int32                    `json:"kvOffloadSize,omitempty"`
}

// Resolve merges overrides onto a preset (may be nil if overrides are complete)
// plus controller-level constant defaults. Returns the resolved config and
// a sha256 hex digest of its canonical JSON.
func Resolve(preset *vllmv1alpha1.ModelPresetSpec, overrides *vllmv1alpha1.ModelConfigOverrides) (EffectiveConfig, string, error) {
	var e EffectiveConfig

	if preset != nil {
		e = EffectiveConfig{
			ModelID:                 preset.ModelID,
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
		}
	}

	if overrides != nil {
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
			ModelID:                 preset.ModelID,
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
		}
		if preset.EnablePrefixCaching != nil {
			v := *preset.EnablePrefixCaching
			e.EnablePrefixCaching = &v
		}
	}

	if overrides != nil {
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
