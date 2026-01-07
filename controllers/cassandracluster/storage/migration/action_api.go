package migration

import (
	"context"
	"strings"

	api "github.com/cscetbon/casskop/api/v2"
	"github.com/cscetbon/casskop/controllers/cassandracluster/pods"
	sc "github.com/cscetbon/casskop/controllers/cassandracluster/storage/change"
	"github.com/cscetbon/casskop/controllers/cassandracluster/storagestateclient"
	"github.com/cscetbon/casskop/controllers/cassandracluster/sts"
	"github.com/cscetbon/casskop/controllers/cassandracluster/view"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
)

// TODO: unit tests
// TODO: maybe we should not start on rack1 if rack2 is not finished... (may happen when double-change done on CR)

func ShouldBeStarted(rack view.RackView, requestedCapacityString, requestedStorageClass string) bool {
	volumeClaimTemplates := rack.LivingStatefulSet().Spec.VolumeClaimTemplates
	_, currentStorageClass := sc.FindDataStorageClass(volumeClaimTemplates)
	_, currentCapacity := sc.FindDataCapacity(volumeClaimTemplates)
	requestedCapacity := sc.SilentParseResourceQuantity(requestedCapacityString)

	storageClassChanged := currentStorageClass != requestedStorageClass
	capacityChanged := !requestedCapacity.Equal(currentCapacity)
	capacityDecreased := requestedCapacity.Cmp(currentCapacity) < 0

	var changedParts []string
	if storageClassChanged {
		changedParts = append(changedParts, "storage class changed from "+currentStorageClass+" to "+requestedStorageClass)
	}
	if capacityChanged {
		changedParts = append(changedParts, "capacity changed from "+currentCapacity.String()+" to "+requestedCapacity.String())
	}
	details := "(" + strings.Join(changedParts, "; ") + ")"

	// When capacity is increased and no storage class change, no migration is needed, StorageUpsize action will handle it
	if storageClassChanged || capacityDecreased {
		rack.Log().Infof("Storage migration should be started: " + details)
		return true
	}

	return false
}

func Start(rack view.RackView) {
	sc.StartAction(rack, api.ActionStorageMigration)
}

func IsStarted(dcRackStatus *api.CassandraRackStatus) bool {
	return dcRackStatus.CassandraLastAction.Name == api.ActionStorageMigration.Name &&
		dcRackStatus.CassandraLastAction.Status != api.StatusDone
}

func Reconcile(ctx context.Context, cc *api.CassandraCluster, rack view.RackView, newStorageClass string,
	newDataCapacity resource.Quantity, storageStateClient storagestateclient.StorageStateClient,
	stsClient sts.StsClient, podsClient pods.PodsClient) error {

	if ShouldMigrateWholeRackAtOnceWhenStorageMigration(cc) {
		return ReconcileWholeRack(ctx, cc, rack, newStorageClass, newDataCapacity, storageStateClient, stsClient, podsClient)
	} else {
		return ReconcileSinglePod(ctx, cc, rack, newStorageClass, newDataCapacity, storageStateClient, stsClient, podsClient)
	}
}

func ShouldMigrateWholeRackAtOnceWhenStorageMigration(cc *api.CassandraCluster) bool {
	if cc.Annotations == nil {
		return false
	}
	value, exists := cc.Annotations["cassandraclusters.db.orange.com/storage-migration/migrate-whole-rack-at-once"]
	return exists && value == "true"
}

func RevertAnyStorageMigrationBeyondMigrationAction(rack view.RackView, newStatefulSet *appsv1.StatefulSet) {
	if !IsStarted(rack.RackStatus()) {
		livingVolumeClaimTemplates := rack.LivingStatefulSet().Spec.VolumeClaimTemplates

		// Revert capacity change if detected
		_, currentCapacity := sc.FindDataCapacity(livingVolumeClaimTemplates)
		capacityIndex, requestedCapacity := sc.FindDataCapacity(newStatefulSet.Spec.VolumeClaimTemplates)
		if !requestedCapacity.Equal(currentCapacity) {
			dataPvcResources := &newStatefulSet.Spec.VolumeClaimTemplates[capacityIndex].Spec.Resources
			if dataPvcResources.Requests == nil {
				dataPvcResources.Requests = corev1.ResourceList{}
			}
			dataPvcResources.Requests[corev1.ResourceStorage] = currentCapacity
			rack.Log().
				Infof("Storage Resize request detected, postponing resize from %s to %s until other actions are done",
					requestedCapacity.String(), currentCapacity.String())
		}

		// Revert storage class change if detected
		_, currentStorageClass := sc.FindDataStorageClass(livingVolumeClaimTemplates)
		classIndex, requestedStorageClass := sc.FindDataStorageClass(newStatefulSet.Spec.VolumeClaimTemplates)
		if requestedStorageClass != currentStorageClass {
			newStatefulSet.Spec.VolumeClaimTemplates[classIndex].Spec.StorageClassName = &currentStorageClass
			rack.Log().
				Infof("Storage Class change detected, postponing change from %s to %s until other actions are done",
					requestedStorageClass, currentStorageClass)
		}
	}
}
