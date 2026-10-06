package main

import (
	"os"
	"strings"
	"testing"

	rbacv1 "k8s.io/api/rbac/v1"
	"sigs.k8s.io/yaml"
)

func TestLeaderElectionNamespaceRBAC(t *testing.T) {
	data, err := os.ReadFile("config/rbac/role.yaml")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, doc := range strings.Split(string(data), "---") {
		var role rbacv1.Role
		if err := yaml.Unmarshal([]byte(doc), &role); err != nil {
			t.Fatal(err)
		}
		if role.Kind != "Role" || role.Namespace != "vllm-system" {
			continue
		}
		for _, rule := range role.Rules {
			if strings.Join(rule.APIGroups, ",") != "coordination.k8s.io" || strings.Join(rule.Resources, ",") != "leases" {
				continue
			}
			for _, verb := range []string{"get", "create", "update", "watch"} {
				if !containsRBACVerb(rule.Verbs, verb) {
					t.Errorf("lease role missing %s", verb)
				}
			}
			found = true
		}
	}
	if !found {
		t.Fatal("missing namespaced lease Role in generated RBAC")
	}
	data, err = os.ReadFile("config/rbac/leader_election_role_binding.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var binding rbacv1.RoleBinding
	if err := yaml.Unmarshal(data, &binding); err != nil {
		t.Fatal(err)
	}
	if binding.Namespace != "vllm-system" || binding.RoleRef.Kind != "Role" || binding.RoleRef.Name != "vllm-operator-role" {
		t.Fatalf("wrong lease RoleBinding: %+v", binding)
	}
	if len(binding.Subjects) != 1 || binding.Subjects[0].Kind != "ServiceAccount" || binding.Subjects[0].Name != "vllm-operator" || binding.Subjects[0].Namespace != "vllm-system" {
		t.Fatalf("wrong binding subjects: %+v", binding.Subjects)
	}
}

func containsRBACVerb(verbs []string, want string) bool {
	for _, verb := range verbs {
		if verb == want {
			return true
		}
	}
	return false
}

func TestDeployCreatesNamespaceBeforeRBAC(t *testing.T) {
	data, err := os.ReadFile("Makefile")
	if err != nil {
		t.Fatal(err)
	}
	recipe := string(data)
	start := strings.Index(recipe, "deploy: manifests install")
	if start < 0 {
		t.Fatal("missing deploy target")
	}
	recipe = recipe[start:]
	namespace := strings.Index(recipe, "$(KUBECTL) apply -f config/manager/namespace.yaml")
	rbac := strings.Index(recipe, "$(KUBECTL) apply -f config/rbac")
	if namespace < 0 || rbac < 0 || namespace > rbac {
		t.Fatal("fresh deploy must create vllm-system before namespaced RBAC")
	}
	data, err = os.ReadFile("config/manager/namespace.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var ns struct {
		Kind     string
		Metadata struct{ Name string }
	}
	if err := yaml.Unmarshal(data, &ns); err != nil {
		t.Fatal(err)
	}
	if ns.Kind != "Namespace" || ns.Metadata.Name != "vllm-system" {
		t.Fatalf("wrong operator Namespace manifest: %+v", ns)
	}
}
