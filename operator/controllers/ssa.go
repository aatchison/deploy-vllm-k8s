package controllers

import (
	"fmt"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/aatchison/deploy-vllm-k8s/operator/internal/vllm"
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

// resolveServiceEndpoint returns the user-facing URL for a vLLM Service.
// Issue #75 made the Service type configurable; the prior NodePort-only path
// is now one branch of three:
//
//   - LoadBalancer: prefer .status.loadBalancer.ingress[0].IP, then .Hostname.
//     If neither is populated (the LB is still provisioning), return the
//     in-cluster DNS form so consumers at least have a working URL.
//   - NodePort:     fall back to the existing nodeIP:nodePort path computed
//     by the caller (nodePortFallback).
//   - ClusterIP:    return http://<svc>.<ns>.svc:<port>/v1 — only reachable
//     from inside the cluster, but that's the contract of ClusterIP.
//
// The function takes the actual Service (re-read after SSA apply) so the
// branch decision matches what's on the API server, not what we tried to
// SSA-apply.
func resolveServiceEndpoint(svc *corev1.Service, actualNodePort int32, nodePortFallback string) string {
	if svc == nil {
		return ""
	}
	switch svc.Spec.Type {
	case corev1.ServiceTypeLoadBalancer:
		for _, ing := range svc.Status.LoadBalancer.Ingress {
			if ing.IP != "" {
				return fmt.Sprintf("http://%s:%d/v1", ing.IP, vllm.HTTPPort)
			}
			if ing.Hostname != "" {
				return fmt.Sprintf("http://%s:%d/v1", ing.Hostname, vllm.HTTPPort)
			}
		}
		// Cloud LB still provisioning. Fall through to cluster-DNS form so
		// status.endpoint isn't blank for the entire window.
		return fmt.Sprintf("http://%s.%s.svc:%d/v1", svc.Name, svc.Namespace, vllm.HTTPPort)
	case corev1.ServiceTypeNodePort:
		return nodePortFallback
	default: // ClusterIP and any unrecognised type → cluster DNS.
		return fmt.Sprintf("http://%s.%s.svc:%d/v1", svc.Name, svc.Namespace, vllm.HTTPPort)
	}
}
