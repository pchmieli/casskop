package sts

import appsv1 "k8s.io/api/apps/v1"

func IsStatefulSetNotReady(storedStatefulSet *appsv1.StatefulSet) bool {
	return !IsStatefulSetReady(storedStatefulSet)
}

func IsStatefulSetReady(storedStatefulSet *appsv1.StatefulSet) bool {
	return storedStatefulSet.Status.ReadyReplicas == *storedStatefulSet.Spec.Replicas
}
