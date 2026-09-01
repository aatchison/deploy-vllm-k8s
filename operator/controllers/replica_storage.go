package controllers

import (
	"context"
	"fmt"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// validateReplicaStorage permits multiple pods only when the bound claim
// advertises multi-node access. ROX is safe only for read-only model mounts;
// RWX supports the operator's normal read/write cache behavior.
func validateReplicaStorage(pvc *corev1.PersistentVolumeClaim, replicas int32, readOnly bool) error {
	if replicas <= 1 {
		return nil
	}
	hasRWX, hasROX := false, false
	for _, mode := range pvc.Spec.AccessModes {
		switch mode {
		case corev1.ReadWriteMany:
			hasRWX = true
		case corev1.ReadOnlyMany:
			hasROX = true
		}
	}
	if hasRWX || (hasROX && readOnly) {
		return nil
	}
	return fmt.Errorf("replicas=%d requires PVC access mode ReadWriteMany (or ReadOnlyMany with pvcReadOnly=true); got %v", replicas, pvc.Spec.AccessModes)
}

// remediateUnsafeDeployment reduces an already-existing operator-owned
// Deployment to one replica before reporting an unsafe multi-replica request.
// The minimal apply changes only replicas and leaves the normal desired-state
// apply blocked until the user fixes the CR.
func remediateUnsafeDeployment(ctx context.Context, c client.Client, apiReader client.Reader, owner client.Object) (bool, error) {
	// Authoritative ownership must be established before any mutation.
	if apiReader == nil {
		return false, fmt.Errorf("authoritative API reader is required")
	}
	var dep appsv1.Deployment
	key := client.ObjectKeyFromObject(owner)
	if err := c.Get(ctx, key, &dep); err != nil {
		if apierrors.IsNotFound(err) {
			return false, nil
		}
		return false, fmt.Errorf("get existing Deployment for remediation: %w", err)
	}
	if !metav1.IsControlledBy(&dep, owner) {
		// Never mutate a same-named workload that is not owned by this CR.
		return false, nil
	}
	if dep.Spec.Replicas == nil || *dep.Spec.Replicas <= 1 {
		return false, nil
	}
	var authoritative appsv1.Deployment
	if err := apiReader.Get(ctx, key, &authoritative); err != nil {
		return false, fmt.Errorf("authoritative pre-Apply Deployment read: %w", err)
	}
	if authoritative.UID != dep.UID || !metav1.IsControlledBy(&authoritative, owner) {
		return false, fmt.Errorf("authoritative pre-Apply Deployment identity or ownership changed")
	}
	if authoritative.Spec.Replicas == nil || *authoritative.Spec.Replicas <= 1 {
		return false, nil
	}
	ones := int32(1)
	patch := &appsv1.Deployment{
		TypeMeta: metav1.TypeMeta{APIVersion: appsv1.SchemeGroupVersion.String(), Kind: "Deployment"},
		ObjectMeta: metav1.ObjectMeta{
			Name:            dep.Name,
			Namespace:       dep.Namespace,
			ResourceVersion: authoritative.ResourceVersion,
		},
		Spec: appsv1.DeploymentSpec{Replicas: &ones},
	}
	ac, err := toApplyConfiguration(patch)
	if err != nil {
		return false, fmt.Errorf("encode Deployment remediation: %w", err)
	}
	// Do not force ownership: an HPA or second manager may legitimately own
	// replicas. A conflict is surfaced to the caller rather than taking that
	// ownership; the controller remains unsafe until the conflict is resolved.
	// The cache can lag the write. An uncached reader is mandatory; never
	// mutate a Deployment unless the receiver can authoritatively verify it.
	if apiReader == nil {
		return false, fmt.Errorf("authoritative API reader is required")
	}
	if err := c.Apply(ctx, ac, fieldOwner); err != nil {
		return false, fmt.Errorf("apply Deployment remediation: %w", err)
	}
	var observed appsv1.Deployment
	if err := apiReader.Get(ctx, key, &observed); err != nil {
		return false, fmt.Errorf("read back remediated Deployment: %w", err)
	}
	if observed.UID != dep.UID || !metav1.IsControlledBy(&observed, owner) {
		return false, fmt.Errorf("read back remediated Deployment identity or ownership changed")
	}
	if observed.Spec.Replicas == nil || *observed.Spec.Replicas != 1 {
		var got any = observed.Spec.Replicas
		if observed.Spec.Replicas != nil {
			got = *observed.Spec.Replicas
		}
		return false, fmt.Errorf("read back remediated Deployment replicas=%v, want 1", got)
	}
	return true, nil
}
