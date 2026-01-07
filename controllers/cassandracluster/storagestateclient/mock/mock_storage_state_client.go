package mock

import (
	"context"

	"github.com/stretchr/testify/mock"
	corev1 "k8s.io/api/core/v1"
)

type StorageStateClient struct {
	mock.Mock
}

func (c *StorageStateClient) GetPVC(ctx context.Context, namespace, name string) (*corev1.PersistentVolumeClaim, error) {
	args := c.Called(ctx, namespace, name)
	return args.Get(0).(*corev1.PersistentVolumeClaim), args.Error(1)
}

func (c *StorageStateClient) ListPVC(ctx context.Context, namespace string, selector map[string]string) (*corev1.PersistentVolumeClaimList, error) {
	args := c.Called(ctx, namespace, selector)
	return args.Get(0).(*corev1.PersistentVolumeClaimList), args.Error(1)
}

func (c *StorageStateClient) UpdatePVC(ctx context.Context, pvc *corev1.PersistentVolumeClaim) error {
	args := c.Called(ctx, pvc)
	return args.Error(0)
}

func (c *StorageStateClient) DeletePVC(ctx context.Context, pvc *corev1.PersistentVolumeClaim) error {
	args := c.Called(ctx, pvc)
	return args.Error(0)
}

func (c *StorageStateClient) CreatePVC(ctx context.Context, pvc *corev1.PersistentVolumeClaim) error {
	args := c.Called(ctx, pvc)
	return args.Error(0)
}

func (c *StorageStateClient) GetPV(ctx context.Context, name string) (*corev1.PersistentVolume, error) {
	args := c.Called(ctx, name)
	return args.Get(0).(*corev1.PersistentVolume), args.Error(1)
}

func (c *StorageStateClient) UpdatePV(ctx context.Context, pv *corev1.PersistentVolume) error {
	args := c.Called(ctx, pv)
	return args.Error(0)
}
