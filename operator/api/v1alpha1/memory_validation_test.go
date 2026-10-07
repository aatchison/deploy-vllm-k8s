package v1alpha1

import (
	"strings"
	"testing"

	apiextensions "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions"
)

func TestGeneratedMemoryValidation(t *testing.T) {
	for _, kind := range []string{"modelpresets", "longcontextpresets", "vllminstances", "longcontextinstances"} {
		t.Run(kind, func(t *testing.T) {
			full := loadGeneratedSchema(t, "vllm.aatchison.io_"+kind+".yaml")
			spec := full.Properties["spec"]
			if strings.HasSuffix(kind, "instances") {
				spec = spec.Properties["overrides"]
			}
			subset := &apiextensions.JSONSchemaProps{Type: "object", Properties: map[string]apiextensions.JSONSchemaProps{}}
			for _, name := range []string{"memoryRequest", "memoryLimit"} {
				field, ok := spec.Properties[name]
				if !ok || field.Type != "string" || field.Pattern == "" {
					t.Fatalf("missing typed quantity pattern for %s", name)
				}
				subset.Properties[name] = field
			}
			for _, rule := range spec.XValidations {
				if strings.Contains(rule.Rule, "memory") {
					subset.XValidations = append(subset.XValidations, rule)
				}
			}
			if len(subset.XValidations) == 0 {
				t.Fatal("missing memory quantity CEL rules")
			}
			for _, field := range []string{"memoryRequest", "memoryLimit"} {
				for _, value := range []string{"64Gi", "1024Mi", "1G", "1.5Gi", "1e9", "invalid", "-1Gi", "0Gi", "", "1KI", " 8Gi"} {
					valid := value == "64Gi" || value == "1024Mi" || value == "1G" || value == "1.5Gi" || value == "1e9" || (value == "" && strings.HasSuffix(kind, "instances"))
					errs := validateGeneratedSchema(t, subset, map[string]interface{}{field: value})
					if valid != (len(errs) == 0) {
						t.Errorf("%s=%q valid=%v errors=%v", field, value, valid, errs)
					}
				}
			}
			for _, tc := range []struct {
				request, limit string
				valid          bool
			}{{"1Gi", "1024Mi", true}, {"1G", "1Gi", true}, {"2Gi", "1Gi", false}} {
				errs := validateGeneratedSchema(t, subset, map[string]interface{}{"memoryRequest": tc.request, "memoryLimit": tc.limit})
				if tc.valid != (len(errs) == 0) {
					t.Errorf("request %s limit %s valid=%v: %v", tc.request, tc.limit, tc.valid, errs)
				}
			}
			if errs := validateGeneratedSchema(t, subset, map[string]interface{}{}); len(errs) != 0 {
				t.Fatalf("unset resources: %v", errs)
			}
		})
	}
}
