package pods

import (
	"context"

	v1 "k8s.io/api/core/v1"
)

type PodsClient interface {
	ListPods(ctx context.Context, namespace string, selector map[string]string) (*v1.PodList, error)
	GetPod(ctx context.Context, namespace, name string) (*v1.Pod, error)
	DeletePod(ctx context.Context, pod *v1.Pod) error
	CreatePod(ctx context.Context, pod *v1.Pod) error
}
