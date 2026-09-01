package controllers

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	vllmv1alpha1 "github.com/aatchison/deploy-vllm-k8s/operator/api/v1alpha1"
	"github.com/aatchison/deploy-vllm-k8s/operator/internal/vllm"
)

const (
	presetRefIndexKey = "spec.presetRef.name"
	fieldOwner        = client.FieldOwner("vllm-operator")
	transientRequeue  = 15 * time.Second
)

// VLLMInstanceReconciler reconciles a VLLMInstance object.
type VLLMInstanceReconciler struct {
	client.Client
	// APIReader is the uncached reader used to verify corrective writes.
	APIReader client.Reader
	Scheme    *runtime.Scheme
	Recorder  record.EventRecorder
}

// +kubebuilder:rbac:groups=vllm.aatchison.io,resources=modelpresets,verbs=get;list;watch
// +kubebuilder:rbac:groups=vllm.aatchison.io,resources=vllminstances,verbs=get;list;watch;patch
// +kubebuilder:rbac:groups=vllm.aatchison.io,resources=vllminstances/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=apps,resources=deployments,verbs=get;list;watch;patch
// +kubebuilder:rbac:groups="",resources=services,verbs=get;list;watch;patch
// +kubebuilder:rbac:groups="",resources=persistentvolumeclaims,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=nodes,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=events,verbs=create;patch
// +kubebuilder:rbac:groups=discovery.k8s.io,resources=endpointslices,verbs=get;list;watch

func (r *VLLMInstanceReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	var instance vllmv1alpha1.VLLMInstance
	if err := r.Get(ctx, req.NamespacedName, &instance); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}
	// Capture the original for status patching; mutations below stage in-memory
	// changes that we flush via Status().Patch(MergeFrom(orig)) at every exit.
	orig := instance.DeepCopy()

	// 1. Resolve preset + overrides.
	var presetSpec *vllmv1alpha1.ModelPresetSpec
	if instance.Spec.PresetRef != nil {
		var preset vllmv1alpha1.ModelPreset
		if err := r.Get(ctx, client.ObjectKey{Namespace: instance.Namespace, Name: instance.Spec.PresetRef.Name}, &preset); err != nil {
			if apierrors.IsNotFound(err) {
				if setVLLMCondition(&instance, vllmv1alpha1.ConditionPresetResolved, metav1.ConditionFalse,
					vllmv1alpha1.ReasonPresetNotFound, fmt.Sprintf("ModelPreset %q not found in namespace %s", instance.Spec.PresetRef.Name, instance.Namespace)) {
					r.eventf(&instance, corev1.EventTypeWarning, vllmv1alpha1.ReasonPresetNotFound,
						"ModelPreset %q not found in namespace %s", instance.Spec.PresetRef.Name, instance.Namespace)
				}
				r.setReadyFalse(&instance, vllmv1alpha1.ReasonPresetNotFound, "Preset not found")
				return r.patchStatus(ctx, &instance, orig, ctrl.Result{RequeueAfter: transientRequeue})
			}
			_, perr := r.patchStatus(ctx, &instance, orig, ctrl.Result{})
			return ctrl.Result{}, errors.Join(err, perr)
		}
		presetSpec = &preset.Spec
		setVLLMCondition(&instance, vllmv1alpha1.ConditionPresetResolved, metav1.ConditionTrue,
			vllmv1alpha1.ReasonPresetFound, fmt.Sprintf("Using ModelPreset %q", instance.Spec.PresetRef.Name))
	} else {
		setVLLMCondition(&instance, vllmv1alpha1.ConditionPresetResolved, metav1.ConditionTrue,
			vllmv1alpha1.ReasonOverridesUsed, "No presetRef; using overrides")
	}

	effective, hash, err := vllm.Resolve(presetSpec, instance.Spec.Overrides)
	if err != nil {
		_, perr := r.patchStatus(ctx, &instance, orig, ctrl.Result{})
		return ctrl.Result{}, errors.Join(fmt.Errorf("resolve config: %w", err), perr)
	}
	// Apply spec-level PVCReadOnly (issue #76) unless an override already
	// supplied a value (override wins). Recompute the hash so the status
	// reflects the actual rendered Deployment.
	if instance.Spec.PVCReadOnly != nil &&
		(instance.Spec.Overrides == nil || instance.Spec.Overrides.PVCReadOnly == nil) {
		effective.PVCReadOnly = *instance.Spec.PVCReadOnly
		if newHash, herr := vllm.HashConfig(effective); herr == nil {
			hash = newHash
		}
	}
	instance.Status.ResolvedConfigHash = hash

	if err := vllm.ValidateEffectiveConfig(effective); err != nil {
		msg := err.Error()
		if setVLLMCondition(&instance, vllmv1alpha1.ConditionReady, metav1.ConditionFalse,
			vllmv1alpha1.ReasonInvalidConfiguration, msg) {
			r.eventf(&instance, corev1.EventTypeWarning, vllmv1alpha1.ReasonInvalidConfiguration, "%s", msg)
		}
		_, perr := r.patchStatus(ctx, &instance, orig, ctrl.Result{})
		return ctrl.Result{}, errors.Join(err, perr)
	}

	// 2. Storage probe.
	var pvc corev1.PersistentVolumeClaim
	if err := r.Get(ctx, client.ObjectKey{Namespace: instance.Namespace, Name: instance.Spec.PVCName}, &pvc); err != nil {
		if apierrors.IsNotFound(err) {
			if setVLLMCondition(&instance, vllmv1alpha1.ConditionStorageReady, metav1.ConditionFalse,
				vllmv1alpha1.ReasonPVCNotFound, fmt.Sprintf("PVC %q not found", instance.Spec.PVCName)) {
				r.eventf(&instance, corev1.EventTypeWarning, vllmv1alpha1.ReasonPVCNotFound,
					"PVC %q not found", instance.Spec.PVCName)
			}
			r.setReadyFalse(&instance, vllmv1alpha1.ReasonPVCNotFound, "Storage not ready")
			return r.patchStatus(ctx, &instance, orig, ctrl.Result{RequeueAfter: transientRequeue})
		}
		_, perr := r.patchStatus(ctx, &instance, orig, ctrl.Result{})
		return ctrl.Result{}, errors.Join(err, perr)
	}
	setVLLMCondition(&instance, vllmv1alpha1.ConditionStorageReady, metav1.ConditionTrue, vllmv1alpha1.ReasonPVCFound, "PVC exists")

	// 3. Desired replicas.
	replicas := int32(1)
	if instance.Spec.Replicas != nil {
		replicas = *instance.Spec.Replicas
	}
	if err := validateReplicaStorage(&pvc, replicas, effective.PVCReadOnly); err != nil {
		remediated, remediationErr := remediateUnsafeDeployment(ctx, r.Client, r.APIReader, &instance)
		if remediationErr != nil {
			message := err.Error() + "; remediation failed: " + remediationErr.Error()
			setVLLMCondition(&instance, vllmv1alpha1.ConditionStorageReady, metav1.ConditionFalse,
				vllmv1alpha1.ReasonReplicaStorageUnsafe, message)
			r.setReadyFalse(&instance, vllmv1alpha1.ReasonReplicaStorageUnsafe, message)
			_, perr := r.patchStatus(ctx, &instance, orig, ctrl.Result{})
			return ctrl.Result{}, errors.Join(remediationErr, perr)
		}
		message := err.Error()
		if remediated {
			message += "; existing Deployment remediated to replicas=1"
		}
		setVLLMCondition(&instance, vllmv1alpha1.ConditionStorageReady, metav1.ConditionFalse,
			vllmv1alpha1.ReasonReplicaStorageUnsafe, message)
		r.setReadyFalse(&instance, vllmv1alpha1.ReasonReplicaStorageUnsafe, message)
		return r.patchStatus(ctx, &instance, orig, ctrl.Result{})
	}

	ownerRef := metav1.OwnerReference{
		APIVersion:         vllmv1alpha1.GroupVersion.String(),
		Kind:               "VLLMInstance",
		Name:               instance.Name,
		UID:                instance.UID,
		Controller:         ptrBool(true),
		BlockOwnerDeletion: ptrBool(true),
	}

	// 4. Build + SSA Deployment.
	apiKey := instance.Spec.APIKey
	if instance.Spec.Overrides != nil && instance.Spec.Overrides.APIKey != nil {
		apiKey = instance.Spec.Overrides.APIKey
	}
	dep := vllm.BuildDeployment(instance.Name, instance.Namespace, replicas, effective, instance.Spec.PVCName, instance.Spec.HFToken, apiKey, ownerRef)
	depAC, err := toApplyConfiguration(dep)
	if err != nil {
		_, perr := r.patchStatus(ctx, &instance, orig, ctrl.Result{})
		return ctrl.Result{}, errors.Join(fmt.Errorf("encode deployment apply: %w", err), perr)
	}
	if err := r.Apply(ctx, depAC, fieldOwner, client.ForceOwnership); err != nil {
		if setVLLMCondition(&instance, vllmv1alpha1.ConditionReady, metav1.ConditionFalse,
			vllmv1alpha1.ReasonApplyFailed, fmt.Sprintf("apply Deployment: %v", err)) {
			r.eventf(&instance, corev1.EventTypeWarning, vllmv1alpha1.ReasonApplyFailed,
				"Failed to apply Deployment %q: %v", dep.Name, err)
		}
		_, perr := r.patchStatus(ctx, &instance, orig, ctrl.Result{})
		return ctrl.Result{}, errors.Join(fmt.Errorf("apply deployment: %w", err), perr)
	}
	instance.Status.DeploymentName = dep.Name

	// 5. Build + SSA Service.
	svc := vllm.BuildService(instance.Name, instance.Namespace, instance.Spec.ServiceType, instance.Spec.NodePort, ownerRef)
	svcAC, err := toApplyConfiguration(svc)
	if err != nil {
		_, perr := r.patchStatus(ctx, &instance, orig, ctrl.Result{})
		return ctrl.Result{}, errors.Join(fmt.Errorf("encode service apply: %w", err), perr)
	}
	if err := r.Apply(ctx, svcAC, fieldOwner, client.ForceOwnership); err != nil {
		if setVLLMCondition(&instance, vllmv1alpha1.ConditionReady, metav1.ConditionFalse,
			vllmv1alpha1.ReasonApplyFailed, fmt.Sprintf("apply Service: %v", err)) {
			r.eventf(&instance, corev1.EventTypeWarning, vllmv1alpha1.ReasonApplyFailed,
				"Failed to apply Service %q: %v", svc.Name, err)
		}
		_, perr := r.patchStatus(ctx, &instance, orig, ctrl.Result{})
		return ctrl.Result{}, errors.Join(fmt.Errorf("apply service: %w", err), perr)
	}
	instance.Status.ServiceName = svc.Name

	// Both the Deployment and Service SSA applies have now succeeded, so the
	// desired state for this spec generation has been realized. Advance
	// ObservedGeneration here — NOT on the earlier preset/PVC/resolve/apply
	// failure exits — so that generation == observedGeneration reliably means
	// "the spec was applied" rather than merely "the controller saw it" (#146).
	instance.Status.ObservedGeneration = instance.Generation

	// Re-read the Service to get the actual NodePort (may have been auto-assigned by Kubernetes).
	var actualSvc corev1.Service
	if err := r.Get(ctx, client.ObjectKey{Namespace: svc.Namespace, Name: svc.Name}, &actualSvc); err != nil {
		if apierrors.IsNotFound(err) {
			// Informer cache hasn't observed the Service we just SSA-applied yet.
			// Flush staged status, then requeue; the Owns(&Service) watch will
			// trigger another reconcile once the cache catches up.
			return r.patchStatus(ctx, &instance, orig, ctrl.Result{RequeueAfter: 1 * time.Second})
		}
		_, perr := r.patchStatus(ctx, &instance, orig, ctrl.Result{})
		return ctrl.Result{}, errors.Join(fmt.Errorf("get service: %w", err), perr)
	}
	var actualNodePort int32
	if len(actualSvc.Spec.Ports) > 0 {
		actualNodePort = actualSvc.Spec.Ports[0].NodePort
	}

	// 6. Mirror Deployment conditions.
	var observed appsv1.Deployment
	if err := r.Get(ctx, client.ObjectKey{Namespace: dep.Namespace, Name: dep.Name}, &observed); err != nil {
		if apierrors.IsNotFound(err) {
			// Informer cache hasn't observed the Deployment we just SSA-applied yet.
			// Flush staged status, then requeue; the Owns(&Deployment) watch will
			// trigger another reconcile.
			return r.patchStatus(ctx, &instance, orig, ctrl.Result{RequeueAfter: 1 * time.Second})
		}
		_, perr := r.patchStatus(ctx, &instance, orig, ctrl.Result{})
		return ctrl.Result{}, errors.Join(fmt.Errorf("get deployment: %w", err), perr)
	}
	if observed.Status.ObservedGeneration >= observed.Generation {
		instance.Status.ReadyReplicas = observed.Status.ReadyReplicas
	}

	avail := conditionFromDeployment(observed.Status.Conditions, appsv1.DeploymentAvailable)
	prog := conditionFromDeployment(observed.Status.Conditions, appsv1.DeploymentProgressing)

	if avail != nil {
		setVLLMCondition(&instance, vllmv1alpha1.ConditionDeploymentAvail, metav1.ConditionStatus(avail.Status), avail.Reason, avail.Message)
	}
	if prog != nil {
		setVLLMCondition(&instance, vllmv1alpha1.ConditionProgressing, metav1.ConditionStatus(prog.Status), prog.Reason, prog.Message)
	}

	// 7. Endpoint + Ready. Scale-to-zero is a distinct steady state: no pods are
	// expected to be Ready, so the avail/progressing flow would either lie
	// (Ready=True with 0 replicas) or report a misleading "Waiting" reason. Take
	// the dedicated ScaledToZero exit and clear status.endpoint so consumers
	// don't dial a phantom URL after scale-down.
	if replicas == 0 {
		setVLLMCondition(&instance, vllmv1alpha1.ConditionReady, metav1.ConditionFalse,
			vllmv1alpha1.ReasonScaledToZero, "spec.replicas=0; no pods serving")
		instance.Status.Endpoint = ""
		logger.V(1).Info("reconciled", "ready", false, "hash", hash, "scaledToZero", true)
		return r.patchStatus(ctx, &instance, orig, ctrl.Result{RequeueAfter: time.Minute})
	}

	// 7a. Endpoint. Service-type-aware (issue #75 supersedes the
	// expose-node-ip annotation introduced in #78):
	//   - NodePort:     http://<node-internal-ip>:<nodePort>/v1
	//   - ClusterIP:    http://<svc>.<ns>.svc:<port>/v1 (in-cluster only)
	//   - LoadBalancer: http://<lb-ip-or-host>:<port>/v1 if assigned, else cluster DNS fallback
	//
	// The NodePort path needs an extra EndpointSlice + Node lookup to find the
	// actual internal IP; only do that when the service is a NodePort.
	var nodePortEndpoint string
	if actualSvc.Spec.Type == corev1.ServiceTypeNodePort {
		nodePortEndpoint = r.resolveEndpoint(ctx, instance.Namespace, svc.Name, actualNodePort)
	}
	endpoint := resolveServiceEndpoint(&actualSvc, actualNodePort, nodePortEndpoint)
	instance.Status.Endpoint = endpoint

	// 8. Ready condition.
	ready := avail != nil && avail.Status == corev1.ConditionTrue && instance.Status.ReadyReplicas >= replicas
	if ready {
		if setVLLMCondition(&instance, vllmv1alpha1.ConditionReady, metav1.ConditionTrue, vllmv1alpha1.ReasonAllReady, "Deployment available, pods ready") {
			// False→True (or first-time-True) transition. Emit one Normal event so
			// kubectl describe shows the Ready breadcrumb without per-reconcile spam.
			r.eventf(&instance, corev1.EventTypeNormal, vllmv1alpha1.ReasonAllReady,
				"Instance ready, endpoint=%s", endpoint)
		}
	} else {
		reason := vllmv1alpha1.ReasonDeploymentUnavailable
		msg := "Waiting for Deployment to become available"
		if prog != nil {
			reason = prog.Reason
			msg = prog.Message
		}
		setVLLMCondition(&instance, vllmv1alpha1.ConditionReady, metav1.ConditionFalse, reason, msg)
	}

	requeue := ctrl.Result{}
	if !ready {
		// Poll once per minute while we wait for the Deployment to progress; faster polling
		// would be dominated by vLLM startup time anyway.
		requeue = ctrl.Result{RequeueAfter: time.Minute}
	}
	logger.V(1).Info("reconciled", "ready", ready, "hash", hash)
	return r.patchStatus(ctx, &instance, orig, requeue)
}

// patchStatus flushes staged status mutations using a strategic merge patch
// against the original (pre-mutation) snapshot. Using Patch — not Update —
// avoids the stale-resourceVersion conflict that PUT triggers when another
// reconcile fires from an Owns() watch in parallel. On Conflict we surface the
// error so the workqueue applies exponential backoff rather than hot-looping.
func (r *VLLMInstanceReconciler) patchStatus(ctx context.Context, instance, orig *vllmv1alpha1.VLLMInstance, res ctrl.Result) (ctrl.Result, error) {
	if err := r.Status().Patch(ctx, instance, client.MergeFrom(orig)); err != nil {
		// Conflicts surface as a normal error so controller-runtime's workqueue
		// can apply exponential backoff. Returning Result{Requeue: true}, nil
		// (the previous behaviour) bypasses backoff and causes hot loops under
		// burst conflicts.
		return ctrl.Result{}, err
	}
	return res, nil
}

// eventf emits a Kubernetes Event on the given object via the configured
// EventRecorder, no-oping if the recorder hasn't been wired (tests that
// instantiate the reconciler directly without main.go's setup).
func (r *VLLMInstanceReconciler) eventf(instance *vllmv1alpha1.VLLMInstance, eventType, reason, format string, args ...interface{}) {
	if r.Recorder == nil {
		return
	}
	r.Recorder.Eventf(instance, eventType, reason, format, args...)
}

// setReadyFalse is a wrapper that flips Ready to False with the supplied
// reason+message. It does not emit its own event because the upstream False
// condition (PresetResolved, StorageReady, …) already emitted the Warning;
// re-emitting on Ready would double-count the same root cause.
func (r *VLLMInstanceReconciler) setReadyFalse(instance *vllmv1alpha1.VLLMInstance, reason, msg string) {
	setVLLMCondition(instance, vllmv1alpha1.ConditionReady, metav1.ConditionFalse, reason, msg)
}

// setVLLMCondition is a thin wrapper around apimeta.SetStatusCondition that
// stamps ObservedGeneration. The upstream helper preserves LastTransitionTime
// when the status is unchanged and dedupes no-op writes — both properties our
// previous hand-rolled setCondition lacked.
//
// Returns true if the call materially changed the condition's Status (i.e.
// either the condition is brand new or its Status flipped). Callers use this
// to gate event emission so kubectl describe shows transitions, not per-
// reconcile spam from steady-state writes.
//
// We snapshot the prior Status into a local before delegating because
// apimeta.FindStatusCondition returns a pointer into the slice that
// SetStatusCondition mutates in place — reading old.Status after the call
// gives the new value, not the prior one.
func setVLLMCondition(instance *vllmv1alpha1.VLLMInstance, t string, s metav1.ConditionStatus, reason, msg string) bool {
	old := apimeta.FindStatusCondition(instance.Status.Conditions, t)
	priorStatus := metav1.ConditionUnknown
	hadPrior := old != nil
	if hadPrior {
		priorStatus = old.Status
	}
	apimeta.SetStatusCondition(&instance.Status.Conditions, metav1.Condition{
		Type:               t,
		Status:             s,
		Reason:             reason,
		Message:            msg,
		ObservedGeneration: instance.Generation,
	})
	return !hadPrior || priorStatus != s
}

// resolveEndpoint returns http://<nodeIP>:<nodePort>/v1 where nodeIP is an
// InternalIP of a node hosting a Ready pod, or "" if no Ready endpoint
// exists. Used only when spec.serviceType == NodePort (issue #75); the
// caller routes ClusterIP/LoadBalancer cases through resolveServiceEndpoint.
//
// Ready endpoints are sorted by NodeName so multi-replica deployments
// produce a stable URL across reconciles (EndpointSlice ordering is
// otherwise unspecified and can flap between reads). The previous "first
// Ready node" fallback returned a URL pointing at a node with no pod
// hosting the model, which 503s on every request.
func (r *VLLMInstanceReconciler) resolveEndpoint(ctx context.Context, namespace, svcName string, nodePort int32) string {
	var slices discoveryv1.EndpointSliceList
	if err := r.List(ctx, &slices, client.InNamespace(namespace), client.MatchingLabels{discoveryv1.LabelServiceName: svcName}); err != nil {
		return ""
	}
	readyNodes := readyNodeNames(slices.Items)
	for _, name := range readyNodes {
		if ip := r.nodeInternalIP(ctx, name); ip != "" {
			return fmt.Sprintf("http://%s:%d/v1", ip, nodePort)
		}
	}
	return ""
}

// readyNodeNames flattens the Ready endpoints across all slices and returns the
// hosting NodeNames sorted lexicographically. Sort gives reconciles a stable
// pick when multiple endpoints are Ready (EndpointSlice ordering is unspecified
// and can flap between reads).
func readyNodeNames(slices []discoveryv1.EndpointSlice) []string {
	var names []string
	seen := map[string]struct{}{}
	for _, s := range slices {
		for _, ep := range s.Endpoints {
			if ep.NodeName == nil || ep.Conditions.Ready == nil || !*ep.Conditions.Ready {
				continue
			}
			n := *ep.NodeName
			if _, ok := seen[n]; ok {
				continue
			}
			seen[n] = struct{}{}
			names = append(names, n)
		}
	}
	sort.Strings(names)
	return names
}

func (r *VLLMInstanceReconciler) nodeInternalIP(ctx context.Context, name string) string {
	var n corev1.Node
	if err := r.Get(ctx, types.NamespacedName{Name: name}, &n); err != nil {
		return ""
	}
	for _, a := range n.Status.Addresses {
		if a.Type == corev1.NodeInternalIP {
			return a.Address
		}
	}
	return ""
}

func conditionFromDeployment(conds []appsv1.DeploymentCondition, t appsv1.DeploymentConditionType) *appsv1.DeploymentCondition {
	for i := range conds {
		if conds[i].Type == t {
			return &conds[i]
		}
	}
	return nil
}

func ptrBool(b bool) *bool { return &b }

// SetupWithManager registers the controller with the manager and installs
// the field index + preset watch.
func (r *VLLMInstanceReconciler) SetupWithManager(mgr ctrl.Manager) error {
	if err := mgr.GetFieldIndexer().IndexField(context.Background(), &vllmv1alpha1.VLLMInstance{}, presetRefIndexKey, func(obj client.Object) []string {
		inst, ok := obj.(*vllmv1alpha1.VLLMInstance)
		if !ok || inst.Spec.PresetRef == nil {
			return nil
		}
		return []string{inst.Spec.PresetRef.Name}
	}); err != nil {
		return err
	}

	return ctrl.NewControllerManagedBy(mgr).
		For(&vllmv1alpha1.VLLMInstance{}).
		Owns(&appsv1.Deployment{}).
		Owns(&corev1.Service{}).
		Watches(
			&vllmv1alpha1.ModelPreset{},
			handler.EnqueueRequestsFromMapFunc(r.mapPresetToInstances),
			// Preset status writes (which we do not perform yet, but might) bump
			// resourceVersion without changing spec. GenerationChangedPredicate
			// suppresses the resulting fan-out across every Instance referencing
			// the preset.
			builder.WithPredicates(predicate.GenerationChangedPredicate{}),
		).
		Watches(
			&corev1.Node{},
			handler.EnqueueRequestsFromMapFunc(r.mapNodeToInstances),
			builder.WithPredicates(nodeWatchPredicate()),
		).
		// EndpointSlice watch is required so resolveEndpoint reads from the
		// informer cache instead of going to the API server every reconcile
		// (issue #83). Without an explicit Watches() registration,
		// controller-runtime treats r.List(slices, ...) as an uncached call —
		// N not-ready instances during cold start = N EndpointSlice LIST RPCs
		// per minute against the apiserver.
		Watches(
			&discoveryv1.EndpointSlice{},
			handler.EnqueueRequestsFromMapFunc(r.mapEndpointSliceToInstance),
		).
		// MaxConcurrentReconciles=4 lets the controller drain the workqueue when a
		// preset edit or node-IP change fans out to many instances.
		// controller-runtime guarantees per-object-key serialization regardless of
		// the worker count, so this is safe.
		WithOptions(controller.Options{MaxConcurrentReconciles: 4}).
		Complete(r)
}

// mapEndpointSliceToInstance enqueues the parent VLLMInstance for an
// EndpointSlice change. The slice carries the parent Service via the
// kubernetes.io/service-name label (kube-controller-manager populates this);
// our Service naming convention is svc-<instance>, so trimming the prefix
// yields the instance name.
//
// Returns nil for slices that don't reference an operator-owned Service —
// the trim is a no-op when the prefix isn't present, which we use as the
// "not ours" signal. This lets us share the cluster-wide EndpointSlice cache
// with other controllers without enqueueing on unrelated services.
func (r *VLLMInstanceReconciler) mapEndpointSliceToInstance(_ context.Context, obj client.Object) []reconcile.Request {
	slice, ok := obj.(*discoveryv1.EndpointSlice)
	if !ok {
		return nil
	}
	svcName := slice.Labels[discoveryv1.LabelServiceName]
	if svcName == "" {
		return nil
	}
	instanceName := strings.TrimPrefix(svcName, "svc-")
	if instanceName == svcName {
		// Service name didn't carry our svc-<...> prefix — not an operator-owned
		// Service. Skip the enqueue.
		return nil
	}
	return []reconcile.Request{{
		NamespacedName: types.NamespacedName{Namespace: slice.Namespace, Name: instanceName},
	}}
}

func (r *VLLMInstanceReconciler) mapPresetToInstances(ctx context.Context, obj client.Object) []reconcile.Request {
	preset, ok := obj.(*vllmv1alpha1.ModelPreset)
	if !ok {
		return nil
	}
	var list vllmv1alpha1.VLLMInstanceList
	if err := r.List(ctx, &list,
		client.InNamespace(preset.Namespace),
		client.MatchingFields{presetRefIndexKey: preset.Name},
	); err != nil {
		return nil
	}
	out := make([]reconcile.Request, 0, len(list.Items))
	for _, inst := range list.Items {
		out = append(out, reconcile.Request{NamespacedName: types.NamespacedName{Name: inst.Name, Namespace: inst.Namespace}})
	}
	return out
}

func (r *VLLMInstanceReconciler) mapNodeToInstances(ctx context.Context, _ client.Object) []reconcile.Request {
	var list vllmv1alpha1.VLLMInstanceList
	if err := r.List(ctx, &list); err != nil {
		return nil
	}
	out := make([]reconcile.Request, 0, len(list.Items))
	for _, inst := range list.Items {
		out = append(out, reconcile.Request{NamespacedName: types.NamespacedName{Name: inst.Name, Namespace: inst.Namespace}})
	}
	return out
}
