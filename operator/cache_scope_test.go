package main

import (
	"os"
	"strings"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	"sigs.k8s.io/yaml"
)

func TestShippedManagerLimitsNamespaces(t *testing.T) {
	data, err := os.ReadFile("config/manager/manager.yaml")
	if err != nil {
		t.Fatal(err)
	}
	// Parse the Deployment document without contacting an API server.
	documents := strings.Split(string(data), "---")
	for _, document := range documents {
		var dep appsv1.Deployment
		if err := yaml.Unmarshal([]byte(document), &dep); err != nil {
			t.Fatal(err)
		}
		if dep.Kind != "Deployment" {
			continue
		}
		for _, c := range dep.Spec.Template.Spec.Containers {
			for _, env := range c.Env {
				if env.Name == "WATCH_NAMESPACES" && env.Value == "vllm" {
					return
				}
			}
		}
	}
	t.Fatal("shipped manager must restrict watched namespaces with WATCH_NAMESPACES=vllm")
}

func TestManagedCacheOptions(t *testing.T) {
	options, err := managedCacheOptions("tenant-a, tenant-b,tenant-a")
	if err != nil {
		t.Fatal(err)
	}
	if len(options.DefaultNamespaces) != 2 {
		t.Fatalf("namespaces=%v", options.DefaultNamespaces)
	}
	for _, ns := range []string{"tenant-a", "tenant-b"} {
		if _, ok := options.DefaultNamespaces[ns]; !ok {
			t.Fatalf("missing namespace %s", ns)
		}
	}
	for object, config := range options.ByObject {
		// nil inherits DefaultNamespaces; an empty non-nil map would watch all namespaces.
		if config.Namespaces != nil {
			t.Fatalf("%T overrides namespace restriction", object)
		}
		if config.Label == nil || config.Label.String() != "app.kubernetes.io/managed-by=vllm-operator" {
			t.Fatalf("%T loses managed-by filter", object)
		}
	}
	for _, invalid := range []string{"", " ", "*", "tenant-a,", ",tenant-a", "BadNamespace", "tenant.a"} {
		if _, err := managedCacheOptions(invalid); err == nil {
			t.Errorf("accepted unbounded/invalid namespace list %q", invalid)
		}
	}
}
