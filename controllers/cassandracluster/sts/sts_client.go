package sts

import (
	"context"
	"fmt"

	appsv1 "k8s.io/api/apps/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func NewClient(client client.Client) StsClient {
	return &stsClient{client: client}
}

type StsClient interface {
	CreateStatefulSet(ctx context.Context, statefulSet *appsv1.StatefulSet) error
	UpdateStatefulSet(ctx context.Context, statefulSet *appsv1.StatefulSet) error
	DeleteStatefulSetWithOrphanOption(ctx context.Context, namespace, name string) error
	DeleteStatefulSet(ctx context.Context, namespace, name string) error
	GetStatefulSet(ctx context.Context, namespace, name string) (*appsv1.StatefulSet, error)
	CheckStatefulSetExists(ctx context.Context, namespace, name string) (bool, error)
}

var _ StsClient = (*stsClient)(nil)

type stsClient struct {
	client client.Client
}

func (c *stsClient) CreateStatefulSet(ctx context.Context, statefulSet *appsv1.StatefulSet) error {
	err := c.client.Create(ctx, statefulSet)
	if err != nil {
		if apierrors.IsAlreadyExists(err) {
			return fmt.Errorf("statefulset already exists: %v", err)
		}
		return fmt.Errorf("failed to create cassandra statefulset: %v", err)
	}
	return nil
}

func (c *stsClient) UpdateStatefulSet(ctx context.Context, statefulSet *appsv1.StatefulSet) error {
	err := c.client.Update(ctx, statefulSet)
	if err != nil {
		return fmt.Errorf("failed to update cassandra statefulset: %v", err)
	}
	return nil

	//TODO: maybe deduplicate with statefulset.go version (polling for new resource version implemented)
}

func (c *stsClient) DeleteStatefulSetWithOrphanOption(ctx context.Context, namespace, name string) error {
	ss := &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
	}
	return c.client.Delete(ctx, ss, client.PropagationPolicy(metav1.DeletePropagationOrphan))
}

func (c *stsClient) DeleteStatefulSet(ctx context.Context, namespace, name string) error {
	ss := &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
	}
	return c.client.Delete(ctx, ss)
}

func (c *stsClient) GetStatefulSet(ctx context.Context, namespace, name string) (*appsv1.StatefulSet, error) {
	ss := &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
	}
	return ss, c.client.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, ss)
}

func (c *stsClient) CheckStatefulSetExists(ctx context.Context, namespace, name string) (bool, error) {
	_, err := c.GetStatefulSet(ctx, namespace, name)
	if err == nil {
		return true, nil
	}
	if apierrors.IsNotFound(err) {
		return false, nil
	}
	return false, err
}
