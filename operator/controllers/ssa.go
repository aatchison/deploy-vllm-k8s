package controllers

import (
	"fmt"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// toApplyConfiguration converts a typed Kubernetes object into a
// runtime.ApplyConfiguration suitable for client.Client.Apply.
//
// controller-runtime v0.24 deprecated the old "patch with PatchType=Apply"
// path in favor of a dedicated Apply method that takes either a generated
// applyconfigurations type (one per kind) or an unstructured-wrapped
// configuration. Generating typed apply configs for our internal builders
// would force vllm.BuildDeployment / BuildService to switch from typed
// Deployment/Service to per-kind apply types — a much larger refactor than
// the deprecation actually requires.
//
// We instead route the existing typed builders through DefaultUnstructuredConverter
// and wrap the result via client.ApplyConfigurationFromUnstructured. The wire-level
// payload is identical to what the deprecated client.Apply Patch produced (a JSON
// body with TypeMeta, ObjectMeta, Spec), so SSA semantics — FieldOwner ownership,
// ForceOwnership conflict resolution — are preserved.
//
// The function intentionally requires the caller-supplied object to have its
// TypeMeta filled in: BuildDeployment / BuildService both stamp APIVersion+Kind
// already, and an apply payload missing those fields fails server-side with a
// "MissingApiVersion" error rather than the obvious nil-deref.
func toApplyConfiguration(obj runtime.Object) (runtime.ApplyConfiguration, error) {
	u, err := runtime.DefaultUnstructuredConverter.ToUnstructured(obj)
	if err != nil {
		return nil, fmt.Errorf("convert to unstructured: %w", err)
	}
	return client.ApplyConfigurationFromUnstructured(&unstructured.Unstructured{Object: u}), nil
}
