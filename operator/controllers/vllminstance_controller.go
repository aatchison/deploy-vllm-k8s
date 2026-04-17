package controllers

import (
	"context"
	"fmt"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
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
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=vllm.aatchison.io,resources=modelpresets,verbs=get;list;watch
// +kubebuilder:rbac:groups=vllm.aatchison.io,resources=vllminstances,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=vllm.aatchison.io,resources=vllminstances/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=apps,resources=deployments,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=services,verbs=get;list;watch;create;update;patch;delete
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

	// 1. Resolve preset + overrides.
	var presetSpec *vllmv1alpha1.ModelPresetSpec
	if instance.Spec.PresetRef != nil {
		var preset vllmv1alpha1.ModelPreset
		if err := r.Get(ctx, client.ObjectKey{Namespace: instance.Namespace, Name: instance.Spec.PresetRef.Name}, &preset); err != nil {
			if apierrors.IsNotFound(err) {
				r.setCondition(&instance, vllmv1alpha1.ConditionPresetResolved, metav1.ConditionFalse,
					vllmv1alpha1.ReasonPresetNotFound, fmt.Sprintf("ModelPreset %q not found in namespace %s", instance.Spec.PresetRef.Name, instance.Namespace))
				r.setCondition(&instance, vllmv1alpha1.ConditionReady, metav1.ConditionFalse, vllmv1alpha1.ReasonPresetNotFound, "Preset not found")
				return r.patchStatus(ctx, &instance, ctrl.Result{RequeueAfter: transientRequeue})
			}
			return ctrl.Result{}, err
		}
		presetSpec = &preset.Spec
		r.setCondition(&instance, vllmv1alpha1.ConditionPresetResolved, metav1.ConditionTrue,
			vllmv1alpha1.ReasonPresetFound, fmt.Sprintf("Using ModelPreset %q", instance.Spec.PresetRef.Name))
	} else {
		r.setCondition(&instance, vllmv1alpha1.ConditionPresetResolved, metav1.ConditionTrue,
			vllmv1alpha1.ReasonOverridesUsed, "No presetRef; using overrides")
	}

	effective, hash, err := vllm.Resolve(presetSpec, instance.Spec.Overrides)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("resolve config: %w", err)
	}
	instance.Status.ResolvedConfigHash = hash

	// 2. Storage probe.
	var pvc corev1.PersistentVolumeClaim
	if err := r.Get(ctx, client.ObjectKey{Namespace: instance.Namespace, Name: instance.Spec.PVCName}, &pvc); err != nil {
		if apierrors.IsNotFound(err) {
			r.setCondition(&instance, vllmv1alpha1.ConditionStorageReady, metav1.ConditionFalse,
				vllmv1alpha1.ReasonPVCNotFound, fmt.Sprintf("PVC %q not found", instance.Spec.PVCName))
			r.setCondition(&instance, vllmv1alpha1.ConditionReady, metav1.ConditionFalse, vllmv1alpha1.ReasonPVCNotFound, "Storage not ready")
			return r.patchStatus(ctx, &instance, ctrl.Result{RequeueAfter: transientRequeue})
		}
		return ctrl.Result{}, err
	}
	r.setCondition(&instance, vllmv1alpha1.ConditionStorageReady, metav1.ConditionTrue, vllmv1alpha1.ReasonPVCFound, "PVC exists")

	// 3. Desired replicas.
	replicas := int32(1)
	if instance.Spec.Replicas != nil {
		replicas = *instance.Spec.Replicas
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
	dep := vllm.BuildDeployment(instance.Name, instance.Namespace, replicas, effective, instance.Spec.PVCName, instance.Spec.HFToken, ownerRef)
	if err := r.Patch(ctx, dep, client.Apply, fieldOwner, client.ForceOwnership); err != nil {
		return ctrl.Result{}, fmt.Errorf("apply deployment: %w", err)
	}
	instance.Status.DeploymentName = dep.Name

	// 5. Build + SSA Service.
	svc := vllm.BuildService(instance.Name, instance.Namespace, instance.Spec.NodePort, ownerRef)
	if err := r.Patch(ctx, svc, client.Apply, fieldOwner, client.ForceOwnership); err != nil {
		return ctrl.Result{}, fmt.Errorf("apply service: %w", err)
	}
	instance.Status.ServiceName = svc.Name

	// Re-read the Service to get the actual NodePort (may have been auto-assigned by Kubernetes).
	var actualSvc corev1.Service
	if err := r.Get(ctx, client.ObjectKey{Namespace: svc.Namespace, Name: svc.Name}, &actualSvc); err != nil {
		return ctrl.Result{}, fmt.Errorf("get service: %w", err)
	}
	var actualNodePort int32
	if len(actualSvc.Spec.Ports) > 0 {
		actualNodePort = actualSvc.Spec.Ports[0].NodePort
	}

	// 6. Mirror Deployment conditions.
	var observed appsv1.Deployment
	if err := r.Get(ctx, client.ObjectKey{Namespace: dep.Namespace, Name: dep.Name}, &observed); err != nil {
		return ctrl.Result{}, err
	}
	if observed.Status.ObservedGeneration >= observed.Generation {
		instance.Status.ReadyReplicas = observed.Status.ReadyReplicas
	}

	avail := conditionFromDeployment(observed.Status.Conditions, appsv1.DeploymentAvailable)
	prog := conditionFromDeployment(observed.Status.Conditions, appsv1.DeploymentProgressing)

	if avail != nil {
		r.setCondition(&instance, vllmv1alpha1.ConditionDeploymentAvail, metav1.ConditionStatus(avail.Status), avail.Reason, avail.Message)
	}
	if prog != nil {
		r.setCondition(&instance, vllmv1alpha1.ConditionProgressing, metav1.ConditionStatus(prog.Status), prog.Reason, prog.Message)
	}

	// 7. Endpoint — use the actual assigned NodePort.
	endpoint := r.resolveEndpoint(ctx, instance.Namespace, svc.Name, actualNodePort)
	instance.Status.Endpoint = endpoint

	// 8. Ready condition.
	ready := avail != nil && avail.Status == corev1.ConditionTrue && instance.Status.ReadyReplicas >= replicas
	if ready {
		r.setCondition(&instance, vllmv1alpha1.ConditionReady, metav1.ConditionTrue, vllmv1alpha1.ReasonAllReady, "Deployment available, pods ready")
	} else {
		reason := vllmv1alpha1.ReasonDeploymentUnavailable
		msg := "Waiting for Deployment to become available"
		if prog != nil {
			reason = prog.Reason
			msg = prog.Message
		}
		r.setCondition(&instance, vllmv1alpha1.ConditionReady, metav1.ConditionFalse, reason, msg)
	}

	requeue := ctrl.Result{}
	if !ready {
		// Poll once per minute while we wait for the Deployment to progress; faster polling
		// would be dominated by vLLM startup time anyway.
		requeue = ctrl.Result{RequeueAfter: time.Minute}
	}
	logger.V(1).Info("reconciled", "ready", ready, "hash", hash)
	return r.patchStatus(ctx, &instance, requeue)
}

func (r *VLLMInstanceReconciler) patchStatus(ctx context.Context, instance *vllmv1alpha1.VLLMInstance, res ctrl.Result) (ctrl.Result, error) {
	instance.Status.ObservedGeneration = instance.Generation
	if err := r.Status().Update(ctx, instance); err != nil {
		if apierrors.IsConflict(err) {
			return ctrl.Result{Requeue: true}, nil
		}
		return ctrl.Result{}, err
	}
	return res, nil
}

func (r *VLLMInstanceReconciler) setCondition(instance *vllmv1alpha1.VLLMInstance, t string, s metav1.ConditionStatus, reason, msg string) {
	now := metav1.Now()
	for i := range instance.Status.Conditions {
		c := &instance.Status.Conditions[i]
		if c.Type != t {
			continue
		}
		if c.Status != s {
			c.LastTransitionTime = now
		}
		c.Status = s
		c.Reason = reason
		c.Message = msg
		c.ObservedGeneration = instance.Generation
		return
	}
	instance.Status.Conditions = append(instance.Status.Conditions, metav1.Condition{
		Type:               t,
		Status:             s,
		Reason:             reason,
		Message:            msg,
		LastTransitionTime: now,
		ObservedGeneration: instance.Generation,
	})
}

// resolveEndpoint returns http://<nodeIP>:<nodePort>/v1 where nodeIP is an InternalIP of a
// Ready node that hosts a Ready pod for this service (via EndpointSlice). Falls back to the
// first Ready node if the slice lookup turns up empty.
func (r *VLLMInstanceReconciler) resolveEndpoint(ctx context.Context, namespace, svcName string, nodePort int32) string {
	var slices discoveryv1.EndpointSliceList
	if err := r.List(ctx, &slices, client.InNamespace(namespace), client.MatchingLabels{discoveryv1.LabelServiceName: svcName}); err == nil {
		for _, s := range slices.Items {
			for _, ep := range s.Endpoints {
				if ep.NodeName == nil || ep.Conditions.Ready == nil || !*ep.Conditions.Ready {
					continue
				}
				if ip := r.nodeInternalIP(ctx, *ep.NodeName); ip != "" {
					return fmt.Sprintf("http://%s:%d/v1", ip, nodePort)
				}
			}
		}
	}

	var nodes corev1.NodeList
	if err := r.List(ctx, &nodes); err != nil {
		return ""
	}
	for _, n := range nodes.Items {
		if !nodeReady(&n) {
			continue
		}
		for _, a := range n.Status.Addresses {
			if a.Type == corev1.NodeInternalIP {
				return fmt.Sprintf("http://%s:%d/v1", a.Address, nodePort)
			}
		}
	}
	return ""
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

func nodeReady(n *corev1.Node) bool {
	for _, c := range n.Status.Conditions {
		if c.Type == corev1.NodeReady {
			return c.Status == corev1.ConditionTrue
		}
	}
	return false
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
			builder.WithPredicates(),
		).
		Watches(
			&corev1.Node{},
			handler.EnqueueRequestsFromMapFunc(r.mapNodeToInstances),
		).
		Complete(r)
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
