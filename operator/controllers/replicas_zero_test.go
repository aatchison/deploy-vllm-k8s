package controllers

import (
	"context"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	vllmv1alpha1 "github.com/aatchison/deploy-vllm-k8s/operator/api/v1alpha1"
)

// ssaTolerantInterceptor short-circuits client.Apply (Server-Side Apply)
// patches: the controller-runtime v0.17 fake client returns
// "apply patches are not supported" because k8s/k8s#115598 isn't fixed
// upstream. For the replicas=0 tests we don't care whether the Deployment
// actually mutates — we only care about the controller's status outputs —
// so we treat Apply as a no-op against the tracker.
var ssaTolerantInterceptor = interceptor.Funcs{
	Patch: func(ctx context.Context, c client.WithWatch, obj client.Object, patch client.Patch, opts ...client.PatchOption) error {
		if patch.Type() == types.ApplyPatchType {
			return nil
		}
		return c.Patch(ctx, obj, patch, opts...)
	},
}

// fullScheme registers the v1alpha1 + corev1 + discoveryv1 types needed by
// replicas-zero and endpoint-resolution tests.
func fullScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	if err := vllmv1alpha1.AddToScheme(s); err != nil {
		t.Fatalf("AddToScheme v1alpha1: %v", err)
	}
	if err := corev1.AddToScheme(s); err != nil {
		t.Fatalf("AddToScheme corev1: %v", err)
	}
	if err := discoveryv1.AddToScheme(s); err != nil {
		t.Fatalf("AddToScheme discoveryv1: %v", err)
	}
	if err := appsv1.AddToScheme(s); err != nil {
		t.Fatalf("AddToScheme appsv1: %v", err)
	}
	return s
}

func ptr[T any](v T) *T { return &v }

// presetSpec returns a minimal-valid ModelPresetSpec usable by Reconcile.
func presetSpec() vllmv1alpha1.ModelPresetSpec {
	return vllmv1alpha1.ModelPresetSpec{
		ModelID:                 "google/gemma-test",
		Image:                   "vllm:test",
		ImagePullPolicy:         "IfNotPresent",
		MIGResource:             "nvidia.com/mig-1g.5gb",
		MIGResourceCount:        1,
		MaxModelLen:             1024,
		GPUMemoryUtilization:    "0.9",
		TensorParallelSize:      1,
		SHMSizeLimit:            "1Gi",
		ProgressDeadlineSeconds: 600,
		LivenessProbe:           vllmv1alpha1.ProbeConfig{InitialDelaySeconds: 30, PeriodSeconds: 10},
		ReadinessProbe:          vllmv1alpha1.ProbeConfig{InitialDelaySeconds: 30, PeriodSeconds: 10},
	}
}

// longContextPresetSpec returns a minimal-valid LongContextPresetSpec.
func longContextPresetSpec() vllmv1alpha1.LongContextPresetSpec {
	return vllmv1alpha1.LongContextPresetSpec{
		ModelID:                 "google/gemma-test",
		Image:                   "vllm:test",
		ImagePullPolicy:         "IfNotPresent",
		MIGResource:             "nvidia.com/mig-1g.5gb",
		MIGResourceCount:        1,
		MaxModelLen:             1024,
		GPUMemoryUtilization:    "0.9",
		TensorParallelSize:      1,
		SHMSizeLimit:            "1Gi",
		ProgressDeadlineSeconds: 600,
		LivenessProbe:           vllmv1alpha1.ProbeConfig{InitialDelaySeconds: 30, PeriodSeconds: 10},
		ReadinessProbe:          vllmv1alpha1.ProbeConfig{InitialDelaySeconds: 30, PeriodSeconds: 10},
		KVCacheDtype:            "fp8_e4m3",
	}
}

// TestReconcile_ReplicasZero_ReadyFalseAndEmptyEndpoint is the headline
// regression for issue #41: when spec.replicas=0, the previous Ready logic
// (`avail==True && ReadyReplicas>=replicas`) evaluated true (0>=0) and
// status.endpoint pointed at a node that no longer hosted a pod. The fix:
// special-case replicas=0 to Ready=False/Reason=ScaledToZero with empty
// endpoint, regardless of any stale endpoint left in status.
func TestReconcile_ReplicasZero_ReadyFalseAndEmptyEndpoint(t *testing.T) {
	scheme := fullScheme(t)

	preset := &vllmv1alpha1.ModelPreset{
		ObjectMeta: metav1.ObjectMeta{Name: "p", Namespace: "ns"},
		Spec:       presetSpec(),
	}
	pvc := &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{Name: "pvc", Namespace: "ns"},
	}
	inst := &vllmv1alpha1.VLLMInstance{
		ObjectMeta: metav1.ObjectMeta{Name: "vi", Namespace: "ns", Generation: 1},
		Spec: vllmv1alpha1.VLLMInstanceSpec{
			PresetRef: &vllmv1alpha1.PresetReference{Name: "p"},
			PVCName:   "pvc",
			HFToken:   corev1.SecretKeySelector{LocalObjectReference: corev1.LocalObjectReference{Name: "hf"}, Key: "token"},
			Replicas:  ptr(int32(0)),
		},
		// Pre-existing phantom endpoint that the fix must clear.
		Status: vllmv1alpha1.VLLMInstanceStatus{Endpoint: "http://10.0.0.1:32000/v1"},
	}
	// Pre-create stub Deployment + Service so Reconcile's post-Apply Get()s
	// find them (the SSA Apply itself is no-op'd by ssaTolerantInterceptor;
	// see its doc).
	depStub := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: inst.Name, Namespace: inst.Namespace}}
	svcStub := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: "svc-" + inst.Name, Namespace: inst.Namespace},
		Spec:       corev1.ServiceSpec{Ports: []corev1.ServicePort{{NodePort: 32000}}},
	}

	cl := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&vllmv1alpha1.VLLMInstance{}).
		WithObjects(preset, pvc, inst, depStub, svcStub).
		WithInterceptorFuncs(ssaTolerantInterceptor).
		Build()

	r := &VLLMInstanceReconciler{Client: cl, Scheme: scheme}
	res, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(inst)})
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if res.RequeueAfter == 0 {
		t.Errorf("expected non-zero RequeueAfter (poll while scaled-to-zero), got %+v", res)
	}

	var got vllmv1alpha1.VLLMInstance
	if err := cl.Get(context.Background(), client.ObjectKeyFromObject(inst), &got); err != nil {
		t.Fatalf("Get: %v", err)
	}

	if got.Status.Endpoint != "" {
		t.Errorf("status.endpoint must be cleared on replicas=0; got %q", got.Status.Endpoint)
	}

	cond := apimeta.FindStatusCondition(got.Status.Conditions, vllmv1alpha1.ConditionReady)
	switch {
	case cond == nil:
		t.Fatal("Ready condition missing")
	case cond.Status != metav1.ConditionFalse:
		t.Errorf("Ready: got %q, want False", cond.Status)
	case cond.Reason != vllmv1alpha1.ReasonScaledToZero:
		t.Errorf("Ready.Reason: got %q, want %q", cond.Reason, vllmv1alpha1.ReasonScaledToZero)
	}
}

// TestReconcileLongContext_ReplicasZero_ReadyFalseAndEmptyEndpoint mirrors the
// VLLMInstance regression for the LongContextInstance controller.
func TestReconcileLongContext_ReplicasZero_ReadyFalseAndEmptyEndpoint(t *testing.T) {
	scheme := fullScheme(t)

	lcPreset := &vllmv1alpha1.LongContextPreset{
		ObjectMeta: metav1.ObjectMeta{Name: "lcp", Namespace: "ns"},
		Spec:       longContextPresetSpec(),
	}
	pvc := &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{Name: "pvc", Namespace: "ns"},
	}
	inst := &vllmv1alpha1.LongContextInstance{
		ObjectMeta: metav1.ObjectMeta{Name: "lci", Namespace: "ns", Generation: 1},
		Spec: vllmv1alpha1.LongContextInstanceSpec{
			PresetRef: &vllmv1alpha1.LongContextPresetReference{Name: "lcp"},
			PVCName:   "pvc",
			HFToken:   corev1.SecretKeySelector{LocalObjectReference: corev1.LocalObjectReference{Name: "hf"}, Key: "token"},
			Replicas:  ptr(int32(0)),
		},
		Status: vllmv1alpha1.LongContextInstanceStatus{Endpoint: "http://10.0.0.2:32001/v1"},
	}

	depStub := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: inst.Name, Namespace: inst.Namespace}}
	svcStub := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: "svc-" + inst.Name, Namespace: inst.Namespace},
		Spec:       corev1.ServiceSpec{Ports: []corev1.ServicePort{{NodePort: 32001}}},
	}

	cl := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&vllmv1alpha1.LongContextInstance{}).
		WithObjects(lcPreset, pvc, inst, depStub, svcStub).
		WithInterceptorFuncs(ssaTolerantInterceptor).
		Build()

	r := &LongContextInstanceReconciler{Client: cl, Scheme: scheme}
	if _, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(inst)}); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	var got vllmv1alpha1.LongContextInstance
	if err := cl.Get(context.Background(), client.ObjectKeyFromObject(inst), &got); err != nil {
		t.Fatalf("Get: %v", err)
	}

	if got.Status.Endpoint != "" {
		t.Errorf("status.endpoint must be cleared on replicas=0; got %q", got.Status.Endpoint)
	}
	cond := apimeta.FindStatusCondition(got.Status.Conditions, vllmv1alpha1.ConditionReady)
	switch {
	case cond == nil:
		t.Fatal("Ready condition missing")
	case cond.Status != metav1.ConditionFalse || cond.Reason != vllmv1alpha1.ReasonScaledToZero:
		t.Errorf("Ready: got %q/%q, want False/%s", cond.Status, cond.Reason, vllmv1alpha1.ReasonScaledToZero)
	}
}

// TestResolveEndpoint_NoReadyEndpointReturnsEmpty asserts the dropped fallback:
// previously, when the EndpointSlice had no Ready endpoints, resolveEndpoint
// fell back to "first Ready node InternalIP" — a URL pointing at a node that
// had no pod hosting the model. The fix returns "" instead.
func TestResolveEndpoint_NoReadyEndpointReturnsEmpty(t *testing.T) {
	scheme := fullScheme(t)

	// EndpointSlice with one NotReady endpoint; a node exists with InternalIP.
	// The old fallback would have returned http://1.2.3.4:30000/v1 — a phantom.
	notReady := false
	slice := &discoveryv1.EndpointSlice{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "vi-abc",
			Namespace: "ns",
			Labels:    map[string]string{discoveryv1.LabelServiceName: "vi"},
		},
		AddressType: discoveryv1.AddressTypeIPv4,
		Endpoints: []discoveryv1.Endpoint{
			{
				Addresses:  []string{"10.244.0.5"},
				NodeName:   ptr("node-a"),
				Conditions: discoveryv1.EndpointConditions{Ready: &notReady},
			},
		},
	}
	node := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: "node-a"},
		Status: corev1.NodeStatus{
			Addresses:  []corev1.NodeAddress{{Type: corev1.NodeInternalIP, Address: "1.2.3.4"}},
			Conditions: []corev1.NodeCondition{{Type: corev1.NodeReady, Status: corev1.ConditionTrue}},
		},
	}

	cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(slice, node).Build()
	r := &VLLMInstanceReconciler{Client: cl, Scheme: scheme}

	// Opt-in to the NodeIP form so we exercise the EndpointSlice fallback
	// path; the in-cluster DNS default doesn't consult EndpointSlices.
	exposeNodeIP := map[string]string{ExposeNodeIPAnnotation: "true"}
	if got := r.resolveEndpoint(context.Background(), "ns", "vi", 30000, exposeNodeIP); got != "" {
		t.Errorf("expected empty endpoint when no Ready endpoint; got %q (the dropped node fallback returned a phantom URL)", got)
	}
}

// TestResolveEndpoint_StableSortAcrossShuffledEndpoints exercises the
// deterministic-NodeName-sort: with multiple Ready endpoints, both the
// "natural" order and a shuffled order must produce the same URL across
// reconciles. Without sort, EndpointSlice ordering can flap between reads
// and the published URL shuffles between nodes.
func TestResolveEndpoint_StableSortAcrossShuffledEndpoints(t *testing.T) {
	ready := true

	mkSlice := func(name string, eps []discoveryv1.Endpoint) *discoveryv1.EndpointSlice {
		return &discoveryv1.EndpointSlice{
			ObjectMeta: metav1.ObjectMeta{
				Name:      name,
				Namespace: "ns",
				Labels:    map[string]string{discoveryv1.LabelServiceName: "vi"},
			},
			AddressType: discoveryv1.AddressTypeIPv4,
			Endpoints:   eps,
		}
	}

	// Three Ready endpoints across two slices, in arbitrary order.
	epsForward := []discoveryv1.Endpoint{
		{Addresses: []string{"10.244.0.6"}, NodeName: ptr("node-c"), Conditions: discoveryv1.EndpointConditions{Ready: &ready}},
		{Addresses: []string{"10.244.0.5"}, NodeName: ptr("node-a"), Conditions: discoveryv1.EndpointConditions{Ready: &ready}},
		{Addresses: []string{"10.244.0.7"}, NodeName: ptr("node-b"), Conditions: discoveryv1.EndpointConditions{Ready: &ready}},
	}
	// Same endpoints, reverse order — simulates EndpointSlice flap.
	epsReverse := make([]discoveryv1.Endpoint, len(epsForward))
	for i, ep := range epsForward {
		epsReverse[len(epsForward)-1-i] = ep
	}

	mkNode := func(name, ip string) *corev1.Node {
		return &corev1.Node{
			ObjectMeta: metav1.ObjectMeta{Name: name},
			Status: corev1.NodeStatus{
				Addresses:  []corev1.NodeAddress{{Type: corev1.NodeInternalIP, Address: ip}},
				Conditions: []corev1.NodeCondition{{Type: corev1.NodeReady, Status: corev1.ConditionTrue}},
			},
		}
	}

	scheme := fullScheme(t)
	build := func(eps []discoveryv1.Endpoint) string {
		cl := fake.NewClientBuilder().
			WithScheme(scheme).
			WithObjects(
				mkSlice("vi-1", eps),
				mkNode("node-a", "10.0.0.1"),
				mkNode("node-b", "10.0.0.2"),
				mkNode("node-c", "10.0.0.3"),
			).
			Build()
		r := &VLLMInstanceReconciler{Client: cl, Scheme: scheme}
		// Opt-in to the legacy NodeIP form to exercise the deterministic-sort
		// behaviour; the default in-cluster-DNS form bypasses EndpointSlice.
		return r.resolveEndpoint(context.Background(), "ns", "vi", 30000, map[string]string{ExposeNodeIPAnnotation: "true"})
	}

	got1 := build(epsForward)
	got2 := build(epsReverse)
	if got1 != got2 {
		t.Errorf("endpoint flapped on shuffled EndpointSlice: forward=%q reverse=%q", got1, got2)
	}
	// node-a sorts first lexicographically — its IP must win.
	want := "http://10.0.0.1:30000/v1"
	if got1 != want {
		t.Errorf("expected sort to pick node-a (lexicographic min); got %q want %q", got1, want)
	}
}

// TestReadyNodeNames_FiltersAndSorts is a focused unit test for the helper:
// it must drop NotReady / nil-NodeName endpoints, deduplicate the same node
// across slices, and return names in lexicographic order.
func TestReadyNodeNames_FiltersAndSorts(t *testing.T) {
	ready := true
	notReady := false

	slices := []discoveryv1.EndpointSlice{
		{Endpoints: []discoveryv1.Endpoint{
			{NodeName: ptr("node-c"), Conditions: discoveryv1.EndpointConditions{Ready: &ready}},
			{NodeName: ptr("node-a"), Conditions: discoveryv1.EndpointConditions{Ready: &ready}},
			{NodeName: ptr("node-x"), Conditions: discoveryv1.EndpointConditions{Ready: &notReady}}, // dropped
			{NodeName: nil, Conditions: discoveryv1.EndpointConditions{Ready: &ready}},              // dropped
			{NodeName: ptr("node-y"), Conditions: discoveryv1.EndpointConditions{Ready: nil}},       // dropped (Ready nil)
		}},
		{Endpoints: []discoveryv1.Endpoint{
			{NodeName: ptr("node-b"), Conditions: discoveryv1.EndpointConditions{Ready: &ready}},
			{NodeName: ptr("node-a"), Conditions: discoveryv1.EndpointConditions{Ready: &ready}}, // dedup
		}},
	}

	got := readyNodeNames(slices)
	want := []string{"node-a", "node-b", "node-c"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("got[%d]=%q want %q (full=%v)", i, got[i], want[i], got)
		}
	}
}
