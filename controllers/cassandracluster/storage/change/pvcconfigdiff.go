package change

import "k8s.io/apimachinery/pkg/api/resource"

func NewDataPVCCapacityChange(newCapacity resource.Quantity) DataPVCConfigurationChange {
	return dataPVCConfigurationChange{
		capacity: &newCapacity,
	}
}

func NewDataPVCConfigMigrationChange(newStorageClass string, newCapacity resource.Quantity) DataPVCConfigurationChange {
	return dataPVCConfigurationChange{
		capacity:     &newCapacity,
		storageClass: &newStorageClass,
	}
}

type DataPVCConfigurationChange interface {
	IsCapacityChange() bool
	IsStorageClassChange() bool
	GetCapacity() resource.Quantity
	GetStorageClass() string
}

type dataPVCConfigurationChange struct {
	capacity     *resource.Quantity
	storageClass *string
}

func (d dataPVCConfigurationChange) IsCapacityChange() bool {
	return d.capacity != nil
}

func (d dataPVCConfigurationChange) IsStorageClassChange() bool {
	return d.storageClass != nil
}

func (d dataPVCConfigurationChange) GetCapacity() resource.Quantity {
	return *d.capacity
}

func (d dataPVCConfigurationChange) GetStorageClass() string {
	return *d.storageClass
}
