package controllers

import (
	"context"
	"errors"
	"fmt"
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
	longContextPresetRefIndexKey = "spec.longContextPresetRef.name"
)

// LongContextInstanceReconciler reconciles a LongContextInstance object.
type LongContextInstanceReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	Recorder record.EventRecorder
}

// +kubebuilder:rbac:groups=vllm.aatchison.io,resources=longcontextpresets,verbs=get;list;watch
// +kubebuilder:rbac:groups=vllm.aatchison.io,resources=longcontextinstances,verbs=get;list;watch;patch
// +kubebuilder:rbac:groups=vllm.aatchison.io,resources=longcontextinstances/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=apps,resources=deployments,verbs=get;list;watch;patch
// +kubebuilder:rbac:groups="",resources=services,verbs=get;list;watch;patch
// +kubebuilder:rbac:groups="",resources=persistentvolumeclaims,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=nodes,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=events,verbs=create;patch
// +kubebuilder:rbac:groups=discovery.k8s.io,resources=endpointslices,verbs=get;list;watch

func (r *LongContextInstanceReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	var instance vllmv1alpha1.LongContextInstance
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
	var presetSpec *vllmv1alpha1.LongContextPresetSpec
	if instance.Spec.PresetRef != nil {
		var preset vllmv1alpha1.LongContextPreset
		if err := r.Get(ctx, client.ObjectKey{Namespace: instance.Namespace, Name: instance.Spec.PresetRef.Name}, &preset); err != nil {
			if apierrors.IsNotFound(err) {
				if setLongContextCondition(&instance, vllmv1alpha1.ConditionPresetResolved, metav1.ConditionFalse,
					vllmv1alpha1.ReasonPresetNotFound, fmt.Sprintf("LongContextPreset %q not found in namespace %s", instance.Spec.PresetRef.Name, instance.Namespace)) {
					r.eventf(&instance, corev1.EventTypeWarning, vllmv1alpha1.ReasonPresetNotFound,
						"LongContextPreset %q not found in namespace %s", instance.Spec.PresetRef.Name, instance.Namespace)
				}
				r.setReadyFalse(&instance, vllmv1alpha1.ReasonPresetNotFound, "Preset not found")
				return r.patchStatus(ctx, &instance, orig, ctrl.Result{RequeueAfter: transientRequeue})
			}
			_, perr := r.patchStatus(ctx, &instance, orig, ctrl.Result{})
			return ctrl.Result{}, errors.Join(err, perr)
		}
		presetSpec = &preset.Spec
		setLongContextCondition(&instance, vllmv1alpha1.ConditionPresetResolved, metav1.ConditionTrue,
			vllmv1alpha1.ReasonPresetFound, fmt.Sprintf("Using LongContextPreset %q", instance.Spec.PresetRef.Name))
	} else {
		setLongContextCondition(&instance, vllmv1alpha1.ConditionPresetResolved, metav1.ConditionTrue,
			vllmv1alpha1.ReasonOverridesUsed, "No presetRef; using overrides")
	}

	effective, hash, err := vllm.ResolveLongContext(presetSpec, instance.Spec.Overrides)
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
		if setLongContextCondition(&instance, vllmv1alpha1.ConditionReady, metav1.ConditionFalse, vllmv1alpha1.ReasonInvalidConfig, err.Error()) {
			r.eventf(&instance, corev1.EventTypeWarning, vllmv1alpha1.ReasonInvalidConfig, "%v", err)
		}
		_, perr := r.patchStatus(ctx, &instance, orig, ctrl.Result{})
		return ctrl.Result{}, errors.Join(err, perr)
	}

	if err := vllm.ValidateEffectiveConfig(effective); err != nil {
		msg := err.Error()
		if setLongContextCondition(&instance, vllmv1alpha1.ConditionReady, metav1.ConditionFalse,
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
			if setLongContextCondition(&instance, vllmv1alpha1.ConditionStorageReady, metav1.ConditionFalse,
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
	setLongContextCondition(&instance, vllmv1alpha1.ConditionStorageReady, metav1.ConditionTrue, vllmv1alpha1.ReasonPVCFound, "PVC exists")

	// 3. Desired replicas.
	replicas := int32(1)
	if instance.Spec.Replicas != nil {
		replicas = *instance.Spec.Replicas
	}

	ownerRef := metav1.OwnerReference{
		APIVersion:         vllmv1alpha1.GroupVersion.String(),
		Kind:               "LongContextInstance",
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
		if setLongContextCondition(&instance, vllmv1alpha1.ConditionReady, metav1.ConditionFalse,
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
		if setLongContextCondition(&instance, vllmv1alpha1.ConditionReady, metav1.ConditionFalse,
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

	// Re-read the Service to get the actual NodePort.
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
		setLongContextCondition(&instance, vllmv1alpha1.ConditionDeploymentAvail, metav1.ConditionStatus(avail.Status), avail.Reason, avail.Message)
	}
	if prog != nil {
		setLongContextCondition(&instance, vllmv1alpha1.ConditionProgressing, metav1.ConditionStatus(prog.Status), prog.Reason, prog.Message)
	}

	// 7. Endpoint + Ready. See VLLMInstanceReconciler.Reconcile for the rationale —
	// scale-to-zero is a steady state distinct from "unavailable", and the avail
	// flow would otherwise either lie (Ready=True) or report a misleading reason.
	if replicas == 0 {
		setLongContextCondition(&instance, vllmv1alpha1.ConditionReady, metav1.ConditionFalse,
			vllmv1alpha1.ReasonScaledToZero, "spec.replicas=0; no pods serving")
		instance.Status.Endpoint = ""
		logger.V(1).Info("reconciled longcontext", "ready", false, "hash", hash, "scaledToZero", true)
		return r.patchStatus(ctx, &instance, orig, ctrl.Result{RequeueAfter: time.Minute})
	}

	// 7a. Endpoint. See VLLMInstanceReconciler for the service-type matrix.
	var nodePortEndpoint string
	if actualSvc.Spec.Type == corev1.ServiceTypeNodePort {
		nodePortEndpoint = r.resolveEndpoint(ctx, instance.Namespace, svc.Name, actualNodePort)
	}
	endpoint := resolveServiceEndpoint(&actualSvc, actualNodePort, nodePortEndpoint)
	instance.Status.Endpoint = endpoint

	// 8. Ready condition.
	ready := avail != nil && avail.Status == corev1.ConditionTrue && instance.Status.ReadyReplicas >= replicas
	if ready {
		if setLongContextCondition(&instance, vllmv1alpha1.ConditionReady, metav1.ConditionTrue, vllmv1alpha1.ReasonAllReady, "Deployment available, pods ready") {
			// False→True (or first-time-True) transition. One Normal event so
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
		setLongContextCondition(&instance, vllmv1alpha1.ConditionReady, metav1.ConditionFalse, reason, msg)
	}

	requeue := ctrl.Result{}
	if !ready {
		requeue = ctrl.Result{RequeueAfter: time.Minute}
	}
	logger.V(1).Info("reconciled longcontext", "ready", ready, "hash", hash)
	return r.patchStatus(ctx, &instance, orig, requeue)
}

// patchStatus flushes staged status mutations using a strategic merge patch
// against the original (pre-mutation) snapshot. See VLLMInstanceReconciler's
// patchStatus for the rationale on Patch-vs-Update and IsConflict handling.
func (r *LongContextInstanceReconciler) patchStatus(ctx context.Context, instance, orig *vllmv1alpha1.LongContextInstance, res ctrl.Result) (ctrl.Result, error) {
	if err := r.Status().Patch(ctx, instance, client.MergeFrom(orig)); err != nil {
		return ctrl.Result{}, err
	}
	return res, nil
}

// eventf mirrors VLLMInstanceReconciler.eventf — a nil-safe wrapper around
// EventRecorder.Eventf so tests that build the reconciler without main.go's
// setup don't NPE when condition transitions fire.
func (r *LongContextInstanceReconciler) eventf(instance *vllmv1alpha1.LongContextInstance, eventType, reason, format string, args ...interface{}) {
	if r.Recorder == nil {
		return
	}
	r.Recorder.Eventf(instance, eventType, reason, format, args...)
}

// setReadyFalse flips Ready to False without emitting an event — the upstream
// False condition already emitted the Warning, so we don't double-count.
func (r *LongContextInstanceReconciler) setReadyFalse(instance *vllmv1alpha1.LongContextInstance, reason, msg string) {
	setLongContextCondition(instance, vllmv1alpha1.ConditionReady, metav1.ConditionFalse, reason, msg)
}

// setLongContextCondition mirrors setVLLMCondition for LongContextInstance and
// returns true when the call materially changed the condition's Status, so
// callers can gate event emission on actual transitions.
//
// Snapshots prior Status before delegating: apimeta.FindStatusCondition
// returns a pointer into the slice that SetStatusCondition mutates in place,
// so reading old.Status after the call would give the new value.
func setLongContextCondition(instance *vllmv1alpha1.LongContextInstance, t string, s metav1.ConditionStatus, reason, msg string) bool {
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

// resolveEndpoint mirrors VLLMInstanceReconciler.resolveEndpoint — used only
// for spec.serviceType == NodePort. See that function's doc for the
// no-Ready-endpoint return value and deterministic-sort rationale.
func (r *LongContextInstanceReconciler) resolveEndpoint(ctx context.Context, namespace, svcName string, nodePort int32) string {
	var slices discoveryv1.EndpointSliceList
	if err := r.List(ctx, &slices, client.InNamespace(namespace), client.MatchingLabels{discoveryv1.LabelServiceName: svcName}); err != nil {
		return ""
	}
	for _, name := range readyNodeNames(slices.Items) {
		if ip := r.nodeInternalIP(ctx, name); ip != "" {
			return fmt.Sprintf("http://%s:%d/v1", ip, nodePort)
		}
	}
	return ""
}

func (r *LongContextInstanceReconciler) nodeInternalIP(ctx context.Context, name string) string {
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

// SetupWithManager registers the controller with the manager and installs
// the field index + preset watch.
func (r *LongContextInstanceReconciler) SetupWithManager(mgr ctrl.Manager) error {
	if err := mgr.GetFieldIndexer().IndexField(context.Background(), &vllmv1alpha1.LongContextInstance{}, longContextPresetRefIndexKey, func(obj client.Object) []string {
		inst, ok := obj.(*vllmv1alpha1.LongContextInstance)
		if !ok || inst.Spec.PresetRef == nil {
			return nil
		}
		return []string{inst.Spec.PresetRef.Name}
	}); err != nil {
		return err
	}

	return ctrl.NewControllerManagedBy(mgr).
		For(&vllmv1alpha1.LongContextInstance{}).
		Owns(&appsv1.Deployment{}).
		Owns(&corev1.Service{}).
		Watches(
			&vllmv1alpha1.LongContextPreset{},
			handler.EnqueueRequestsFromMapFunc(r.mapPresetToInstances),
			// See VLLMInstanceReconciler.SetupWithManager for the rationale on
			// GenerationChangedPredicate here.
			builder.WithPredicates(predicate.GenerationChangedPredicate{}),
		).
		Watches(
			&corev1.Node{},
			handler.EnqueueRequestsFromMapFunc(r.mapNodeToInstances),
			builder.WithPredicates(nodeWatchPredicate()),
		).
		// See VLLMInstanceReconciler.SetupWithManager — same rationale: without
		// an EndpointSlice watch, resolveEndpoint's r.List goes to the API
		// server uncached every reconcile (issue #83).
		Watches(
			&discoveryv1.EndpointSlice{},
			handler.EnqueueRequestsFromMapFunc(r.mapEndpointSliceToInstance),
		).
		// See VLLMInstanceReconciler.SetupWithManager for rationale.
		WithOptions(controller.Options{MaxConcurrentReconciles: 4}).
		Complete(r)
}

// mapEndpointSliceToInstance mirrors VLLMInstanceReconciler's variant — the
// EndpointSlice's kubernetes.io/service-name label points at svc-<instance>,
// so trimming the prefix yields the parent LongContextInstance.
func (r *LongContextInstanceReconciler) mapEndpointSliceToInstance(_ context.Context, obj client.Object) []reconcile.Request {
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
		return nil
	}
	return []reconcile.Request{{
		NamespacedName: types.NamespacedName{Namespace: slice.Namespace, Name: instanceName},
	}}
}

func (r *LongContextInstanceReconciler) mapPresetToInstances(ctx context.Context, obj client.Object) []reconcile.Request {
	preset, ok := obj.(*vllmv1alpha1.LongContextPreset)
	if !ok {
		return nil
	}
	var list vllmv1alpha1.LongContextInstanceList
	if err := r.List(ctx, &list,
		client.InNamespace(preset.Namespace),
		client.MatchingFields{longContextPresetRefIndexKey: preset.Name},
	); err != nil {
		return nil
	}
	out := make([]reconcile.Request, 0, len(list.Items))
	for _, inst := range list.Items {
		out = append(out, reconcile.Request{NamespacedName: types.NamespacedName{Name: inst.Name, Namespace: inst.Namespace}})
	}
	return out
}

func (r *LongContextInstanceReconciler) mapNodeToInstances(ctx context.Context, _ client.Object) []reconcile.Request {
	var list vllmv1alpha1.LongContextInstanceList
	if err := r.List(ctx, &list); err != nil {
		return nil
	}
	out := make([]reconcile.Request, 0, len(list.Items))
	for _, inst := range list.Items {
		out = append(out, reconcile.Request{NamespacedName: types.NamespacedName{Name: inst.Name, Namespace: inst.Namespace}})
	}
	return out
}
