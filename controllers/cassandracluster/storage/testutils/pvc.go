package testutils

import (
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
)

func Pvc(name, capacity string) corev1.PersistentVolumeClaim {
	var resources corev1.ResourceList
	if capacity != "" {
		resources = corev1.ResourceList{
			corev1.ResourceStorage: resource.MustParse(capacity),
		}
	}
	return corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: "default",
		},
		Spec: corev1.PersistentVolumeClaimSpec{
			StorageClassName: ptr.To("old-storage-class"),
			Resources: corev1.VolumeResourceRequirements{
				Requests: resources,
			},
		},
	}
}
