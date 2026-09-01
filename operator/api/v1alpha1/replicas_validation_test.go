package v1alpha1

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	apiextensions "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	structuralschema "k8s.io/apiextensions-apiserver/pkg/apiserver/schema"
	"k8s.io/apiextensions-apiserver/pkg/apiserver/schema/cel"
	crdvalidation "k8s.io/apiextensions-apiserver/pkg/apiserver/validation"
	"k8s.io/apimachinery/pkg/util/validation/field"
	celconfig "k8s.io/apiserver/pkg/apis/cel"
	"sigs.k8s.io/yaml"
)

func TestGeneratedCRDReplicaLimit(t *testing.T) {
	tests := []struct {
		name    string
		crdFile string
		kind    string
	}{
		{
			name:    "VLLMInstance",
			crdFile: "vllm.aatchison.io_vllminstances.yaml",
			kind:    "VLLMInstance",
		},
		{
			name:    "LongContextInstance",
			crdFile: "vllm.aatchison.io_longcontextinstances.yaml",
			kind:    "LongContextInstance",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			schema := loadGeneratedSchema(t, tt.crdFile)
			for _, replicas := range []int64{2, 3} {
				obj := map[string]interface{}{
					"apiVersion": "vllm.aatchison.io/v1alpha1",
					"kind":       tt.kind,
					"metadata": map[string]interface{}{
						"name":      "replica-limit-test",
						"namespace": "default",
					},
					"spec": map[string]interface{}{
						"presetRef": map[string]interface{}{"name": "test-preset"},
						"pvcName":   "models",
						"hfToken": map[string]interface{}{
							"name": "hf-token",
							"key":  "token",
						},
						"replicas": replicas,
					},
				}

				errs := validateGeneratedSchema(t, schema, obj)
				if replicas == 2 && len(errs) != 0 {
					t.Fatalf("replicas=2 must be accepted by generated CRD schema: %v", errs)
				}
				if replicas == 3 {
					if len(errs) == 0 {
						t.Fatal("replicas=3 must be rejected by generated CRD schema")
					}
					if !strings.Contains(errs.ToAggregate().Error(), "replicas must be 0, 1, or 2") {
						t.Fatalf("replicas=3 rejected for unexpected reason: %v", errs)
					}
				}
			}
		})
	}
}

func loadGeneratedSchema(t *testing.T, name string) *apiextensions.JSONSchemaProps {
	t.Helper()
	path := filepath.Join("..", "..", "config", "crd", "bases", name)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read generated CRD %s: %v", path, err)
	}

	var crd apiextensionsv1.CustomResourceDefinition
	if err := yaml.Unmarshal(data, &crd); err != nil {
		t.Fatalf("decode generated CRD %s: %v", path, err)
	}
	for i := range crd.Spec.Versions {
		version := &crd.Spec.Versions[i]
		if version.Name != "v1alpha1" || version.Schema == nil || version.Schema.OpenAPIV3Schema == nil {
			continue
		}
		internal := &apiextensions.JSONSchemaProps{}
		if err := apiextensionsv1.Convert_v1_JSONSchemaProps_To_apiextensions_JSONSchemaProps(version.Schema.OpenAPIV3Schema, internal, nil); err != nil {
			t.Fatalf("convert generated schema %s: %v", path, err)
		}
		return internal
	}
	t.Fatalf("generated CRD %s has no v1alpha1 OpenAPI schema", path)
	return nil
}

func validateGeneratedSchema(t *testing.T, schema *apiextensions.JSONSchemaProps, obj interface{}) field.ErrorList {
	t.Helper()
	openAPIValidator, _, err := crdvalidation.NewSchemaValidator(schema)
	if err != nil {
		t.Fatalf("build OpenAPI validator: %v", err)
	}
	errs := crdvalidation.ValidateCustomResource(field.NewPath("resource"), obj, openAPIValidator)

	structural, err := structuralschema.NewStructural(schema)
	if err != nil {
		t.Fatalf("build structural schema: %v", err)
	}
	celValidator := cel.NewValidator(structural, true, celconfig.PerCallLimit)
	if celValidator == nil {
		t.Fatal("generated CRD schema has no CEL validator")
	}
	celErrs, _ := celValidator.Validate(
		context.Background(),
		field.NewPath("resource"),
		structural,
		obj,
		nil,
		celconfig.RuntimeCELCostBudget,
	)
	return append(errs, celErrs...)
}
