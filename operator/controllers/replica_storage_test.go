package controllers

import (
	corev1 "k8s.io/api/core/v1"
	"testing"
)

func TestValidateReplicaStorage(t *testing.T) {
	tests := []struct {
		name     string
		mode     corev1.PersistentVolumeAccessMode
		replicas int32
		readOnly bool
		wantErr  bool
	}{
		{"single RWO allowed", corev1.ReadWriteOnce, 1, false, false},
		{"two RWX allowed", corev1.ReadWriteMany, 2, false, false},
		{"two ROX read-only allowed", corev1.ReadOnlyMany, 2, true, false},
		{"two ROX writable rejected", corev1.ReadOnlyMany, 2, false, true},
		{"two RWO rejected", corev1.ReadWriteOnce, 2, false, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pvc := &corev1.PersistentVolumeClaim{Spec: corev1.PersistentVolumeClaimSpec{AccessModes: []corev1.PersistentVolumeAccessMode{tt.mode}}}
			if gotErr := validateReplicaStorage(pvc, tt.replicas, tt.readOnly); (gotErr != nil) != tt.wantErr {
				t.Fatalf("error=%v, wantErr=%v", gotErr, tt.wantErr)
			}
		})
	}
}
