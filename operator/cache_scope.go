package main

import (
	"fmt"
	"strings"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/util/validation"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/aatchison/deploy-vllm-k8s/operator/internal/vllm"
)

// managedCacheOptions bounds every namespaced informer, including dynamically
// created caches. The public managed-by label is a filter, not an ownership proof.
// Quotas are still required inside the managed namespaces.
func managedCacheOptions(namespaces string) (cache.Options, error) {
	scoped := map[string]cache.Config{}
	for _, raw := range strings.Split(namespaces, ",") {
		namespace := strings.TrimSpace(raw)
		if namespace == "" || len(validation.IsDNS1123Label(namespace)) != 0 {
			return cache.Options{}, fmt.Errorf("watch-namespaces must contain explicit namespace names, got %q", namespace)
		}
		scoped[namespace] = cache.Config{}
	}
	selector := labels.SelectorFromSet(labels.Set{vllm.ManagedByLabelKey: vllm.ManagedByLabelValue})
	return cache.Options{
		DefaultNamespaces: scoped,
		ByObject: map[client.Object]cache.ByObject{
			&appsv1.Deployment{}: {Label: selector},
			&corev1.Service{}:    {Label: selector},
		},
	}, nil
}
