// Package compatibility provides an offline approximation of the Kubernetes
// CRD admission steps used by this operator. It deliberately uses the same
// apiextensions libraries as the API server: pruning, defaulting, OpenAPI
// validation, and CEL validation.
package compatibility

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"reflect"
	"sort"
	"strings"

	apiextensions "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	structuralschema "k8s.io/apiextensions-apiserver/pkg/apiserver/schema"
	"k8s.io/apiextensions-apiserver/pkg/apiserver/schema/cel"
	structuraldefaulting "k8s.io/apiextensions-apiserver/pkg/apiserver/schema/defaulting"
	"k8s.io/apiextensions-apiserver/pkg/apiserver/schema/pruning"
	crdvalidation "k8s.io/apiextensions-apiserver/pkg/apiserver/validation"
	"k8s.io/apimachinery/pkg/util/validation/field"
	celconfig "k8s.io/apiserver/pkg/apis/cel"
	"sigs.k8s.io/yaml"
)

// Schema contains both forms required by the Kubernetes admission helpers.
type Schema struct {
	OpenAPI    *apiextensions.JSONSchemaProps
	Structural *structuralschema.Structural
}

// LoadSchema reads a JSON or YAML CRD and returns its schema for version.
func LoadSchema(path, version string) (*Schema, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read CRD %s: %w", path, err)
	}
	var crd apiextensionsv1.CustomResourceDefinition
	if err := yaml.Unmarshal(data, &crd); err != nil {
		return nil, fmt.Errorf("decode CRD %s: %w", path, err)
	}
	for i := range crd.Spec.Versions {
		v := &crd.Spec.Versions[i]
		if v.Name != version || v.Schema == nil || v.Schema.OpenAPIV3Schema == nil {
			continue
		}
		internal := &apiextensions.JSONSchemaProps{}
		if err := apiextensionsv1.Convert_v1_JSONSchemaProps_To_apiextensions_JSONSchemaProps(v.Schema.OpenAPIV3Schema, internal, nil); err != nil {
			return nil, fmt.Errorf("convert CRD %s schema: %w", path, err)
		}
		structural, err := structuralschema.NewStructural(internal)
		if err != nil {
			return nil, fmt.Errorf("build structural schema for %s: %w", path, err)
		}
		return &Schema{OpenAPI: internal, Structural: structural}, nil
	}
	return nil, fmt.Errorf("CRD %s has no %s OpenAPI schema", path, version)
}

// AdmissionResult records mutations and errors produced by offline admission.
type AdmissionResult struct {
	Object         map[string]interface{}
	PrunedPaths    []string
	DefaultedPaths []string
	Errors         field.ErrorList
}

// Admit deep-copies obj, detects structural pruning, applies schema defaults,
// then runs Kubernetes OpenAPI and CEL validation. Callers must reject a
// result with either PrunedPaths or Errors.
func (s *Schema) Admit(obj map[string]interface{}) (*AdmissionResult, error) {
	admitted, err := cloneObject(obj)
	if err != nil {
		return nil, err
	}
	// JSON decoded into interface{} represents every number as float64, while
	// the apiserver's unstructured decoder represents schema integers as int64.
	// Match the apiserver before CEL type-checking.
	coerceIntegers(admitted, s.Structural)
	pruned := pruning.PruneWithOptions(admitted, s.Structural, true, structuralschema.UnknownFieldPathOptions{TrackUnknownFieldPaths: true})
	beforeDefaults, err := cloneObject(admitted)
	if err != nil {
		return nil, err
	}
	structuraldefaulting.Default(admitted, s.Structural)

	openAPIValidator, _, err := crdvalidation.NewSchemaValidator(s.OpenAPI)
	if err != nil {
		return nil, fmt.Errorf("build OpenAPI validator: %w", err)
	}
	errs := crdvalidation.ValidateCustomResource(field.NewPath("resource"), admitted, openAPIValidator)
	celValidator := cel.NewValidator(s.Structural, true, celconfig.PerCallLimit)
	if celValidator != nil {
		celErrs, _ := celValidator.Validate(context.Background(), field.NewPath("resource"), s.Structural, admitted, nil, celconfig.RuntimeCELCostBudget)
		errs = append(errs, celErrs...)
	}
	return &AdmissionResult{Object: admitted, PrunedPaths: pruned, DefaultedPaths: changedLeafPaths(beforeDefaults, admitted), Errors: errs}, nil
}

func coerceIntegers(value interface{}, schema *structuralschema.Structural) {
	if schema == nil {
		return
	}
	switch x := value.(type) {
	case map[string]interface{}:
		for key, child := range x {
			childSchema, ok := schema.Properties[key]
			if !ok && schema.AdditionalProperties != nil && schema.AdditionalProperties.Structural != nil {
				childSchema = *schema.AdditionalProperties.Structural
				ok = true
			}
			if !ok {
				continue
			}
			if number, ok := child.(float64); ok && childSchema.Type == "integer" && exactInt64(number) {
				x[key] = int64(number)
				continue
			}
			coerceIntegers(child, &childSchema)
		}
	case []interface{}:
		if schema.Items == nil {
			return
		}
		for i, child := range x {
			if number, ok := child.(float64); ok && schema.Items.Type == "integer" && exactInt64(number) {
				x[i] = int64(number)
				continue
			}
			coerceIntegers(child, schema.Items)
		}
	}
}

func exactInt64(number float64) bool {
	// 2^63 is exactly representable as float64 but is outside int64. The
	// strict upper bound also avoids Go's implementation-specific overflow
	// conversion. Fractional values stay float64 so OpenAPI rejects them.
	return !math.IsNaN(number) && !math.IsInf(number, 0) && math.Trunc(number) == number && number >= -9223372036854775808.0 && number < 9223372036854775808.0
}

func cloneObject(obj map[string]interface{}) (map[string]interface{}, error) {
	data, err := json.Marshal(obj)
	if err != nil {
		return nil, fmt.Errorf("encode resource: %w", err)
	}
	var out map[string]interface{}
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("decode resource copy: %w", err)
	}
	return out, nil
}

// Difference is one field-level semantic schema difference. Descriptions are
// intentionally excluded because prose is not an admission contract.
type Difference struct {
	Path      string
	Live      interface{}
	Generated interface{}
}

func (d Difference) String() string {
	side := "changed"
	if d.Live == nil {
		side = "generated-only"
	}
	if d.Generated == nil {
		side = "live-only"
	}
	return fmt.Sprintf("%s %s (live=%s generated=%s)", side, d.Path, compactJSON(d.Live), compactJSON(d.Generated))
}

// Differences compares semantic JSON Schema fields. It reports live-only and
// generated-only paths explicitly and treats CEL rules as keyed set members.
func Differences(live, generated *Schema) ([]Difference, error) {
	a, err := semanticMap(live.OpenAPI)
	if err != nil {
		return nil, err
	}
	b, err := semanticMap(generated.OpenAPI)
	if err != nil {
		return nil, err
	}
	var out []Difference
	diffValue("$", a, b, &out)
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, nil
}

func semanticMap(schema *apiextensions.JSONSchemaProps) (map[string]interface{}, error) {
	external := &apiextensionsv1.JSONSchemaProps{}
	if err := apiextensionsv1.Convert_apiextensions_JSONSchemaProps_To_v1_JSONSchemaProps(schema, external, nil); err != nil {
		return nil, fmt.Errorf("convert schema for comparison: %w", err)
	}
	data, err := json.Marshal(external)
	if err != nil {
		return nil, fmt.Errorf("encode schema: %w", err)
	}
	var value map[string]interface{}
	if err := json.Unmarshal(data, &value); err != nil {
		return nil, fmt.Errorf("decode schema: %w", err)
	}
	normalized, ok := normalize(value, "").(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("normalized schema is not an object")
	}
	return normalized, nil
}

func normalize(value interface{}, key string) interface{} {
	switch x := value.(type) {
	case map[string]interface{}:
		out := make(map[string]interface{}, len(x))
		for k, v := range x {
			if k == "description" || k == "title" {
				continue
			}
			out[k] = normalize(v, k)
		}
		return out
	case []interface{}:
		if key == "x-kubernetes-validations" {
			out := map[string]interface{}{}
			for _, item := range x {
				m, ok := item.(map[string]interface{})
				if !ok {
					continue
				}
				rule, _ := m["rule"].(string)
				out[rule] = normalize(m, "")
			}
			return out
		}
		out := make([]interface{}, len(x))
		for i := range x {
			out[i] = normalize(x[i], "")
		}
		if key == "required" || key == "enum" {
			sort.Slice(out, func(i, j int) bool { return compactJSON(out[i]) < compactJSON(out[j]) })
		}
		return out
	default:
		return value
	}
}

func diffValue(path string, a, b interface{}, out *[]Difference) {
	if reflect.DeepEqual(a, b) {
		return
	}
	am, aok := a.(map[string]interface{})
	bm, bok := b.(map[string]interface{})
	if aok && bok {
		keys := map[string]struct{}{}
		for k := range am {
			keys[k] = struct{}{}
		}
		for k := range bm {
			keys[k] = struct{}{}
		}
		names := make([]string, 0, len(keys))
		for k := range keys {
			names = append(names, k)
		}
		sort.Strings(names)
		for _, k := range names {
			av, aexists := am[k]
			bv, bexists := bm[k]
			child := path + "." + k
			if strings.HasPrefix(k, "!") || strings.Contains(k, "self.") {
				child = path + "[rule=" + k + "]"
			}
			if !aexists {
				*out = append(*out, Difference{Path: child, Generated: bv})
				continue
			}
			if !bexists {
				*out = append(*out, Difference{Path: child, Live: av})
				continue
			}
			diffValue(child, av, bv, out)
		}
		return
	}
	*out = append(*out, Difference{Path: path, Live: a, Generated: b})
}

func changedLeafPaths(a, b interface{}) []string {
	var diffs []Difference
	diffValue("$", a, b, &diffs)
	out := make([]string, 0, len(diffs))
	for _, d := range diffs {
		out = append(out, d.Path)
	}
	sort.Strings(out)
	return out
}

func compactJSON(v interface{}) string { b, _ := json.Marshal(v); return string(b) }
