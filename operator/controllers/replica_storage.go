package controllers

import (
	"fmt"

	corev1 "k8s.io/api/core/v1"
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
