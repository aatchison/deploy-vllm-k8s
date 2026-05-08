// Package v1alpha1 contains API Schema definitions for the vllm v1alpha1 API group
// +kubebuilder:object:generate=true
// +groupName=vllm.aatchison.io
package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// GroupVersion identifies the API group + version for vllm.aatchison.io/v1alpha1.
var GroupVersion = schema.GroupVersion{Group: "vllm.aatchison.io", Version: "v1alpha1"}

// schemeBuilder is a thin wrapper around runtime.SchemeBuilder that preserves
// the object-registering Register(&Foo{}, &FooList{}) shape the per-type init
// blocks use. We rolled our own here instead of importing
// sigs.k8s.io/controller-runtime/pkg/scheme so the api package keeps a minimal
// dependency surface (only k8s.io/apimachinery): controller-runtime deprecated
// scheme.Builder for exactly this reason — api packages should be cheap to
// import from anywhere, not pull in the controller-runtime tree.
type schemeBuilder struct {
	gv schema.GroupVersion
	runtime.SchemeBuilder
}

// Register stages the supplied API objects to be registered against the given
// GroupVersion when AddToScheme runs. Mirrors the contract of
// controller-runtime's deprecated scheme.Builder.Register.
func (b *schemeBuilder) Register(objs ...runtime.Object) *schemeBuilder {
	b.SchemeBuilder.Register(func(s *runtime.Scheme) error {
		s.AddKnownTypes(b.gv, objs...)
		metav1.AddToGroupVersion(s, b.gv)
		return nil
	})
	return b
}

// SchemeBuilder is the package-level builder; per-type init blocks call its
// Register method to attach themselves to the scheme.
var SchemeBuilder = &schemeBuilder{gv: GroupVersion}

// AddToScheme installs every type registered against SchemeBuilder onto s.
var AddToScheme = SchemeBuilder.AddToScheme
