package storageupsize

import (
	"context"

	api "github.com/cscetbon/casskop/api/v2"
	"github.com/cscetbon/casskop/controllers/cassandracluster/pods"
	"github.com/cscetbon/casskop/controllers/cassandracluster/storagestateclient"
	"github.com/cscetbon/casskop/controllers/cassandracluster/sts"
	"github.com/cscetbon/casskop/controllers/cassandracluster/view"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
)

func ShouldBeStarted(rack view.RackView, requestedCapacity string) bool {
	requested := silentParseResourceQuantity(requestedCapacity)
	_, current := findDataCapacity(rack.StoredStatefulSet().Spec.VolumeClaimTemplates)
	if !requested.Equal(current) {
		rack.Log().Infof("Storage upsize should be started: ask %v and have %v", requested, current)
		return true
	}
	return false
}

func Start(rack view.RackView) {
	startUpsizeAction(rack)
}

func IsStarted(dcRackStatus *api.CassandraRackStatus) bool {
	return dcRackStatus.CassandraLastAction.Name == api.ActionStorageUpsize.Name &&
		dcRackStatus.CassandraLastAction.Status != api.StatusDone
}

func Reconcile(ctx context.Context, cc *api.CassandraCluster, rack view.RackView,
	setNewDataCapacity func(statefulSet *appsv1.StatefulSet) error,
	storageStateClient storagestateclient.StorageStateClient, stsClient sts.StsClient, podsClient pods.PodsClient) error {

	if stepResult := makeOldStatefulSetSnapshot(rack); stepResult.HasError() {
		return stepResult.Error()
	} else if stepResult.ShouldBreakReconcileLoop() {
		return nil
	}

	if stepResult := removeStatefulSetOrphan(ctx, cc, rack, stsClient); stepResult.HasError() {
		return stepResult.Error()
	} else if stepResult.ShouldBreakReconcileLoop() {
		return nil
	}

	if stepResult := recreateStatefulSetWithNewCapacity(ctx, rack, setNewDataCapacity, stsClient); stepResult.HasError() {
		return stepResult.Error()
	} else if stepResult.ShouldBreakReconcileLoop() {
		return nil
	}

	dataPVCs, err := getAllDataPvcs(ctx, cc, rack, storageStateClient)
	if err != nil {
		return err
	}

	if stepResult := ensureAllPVCsHaveNewCapacity(ctx, cc, dataPVCs, rack, storageStateClient); stepResult.HasError() {
		return stepResult.Error()
	} else if stepResult.ShouldBreakReconcileLoop() {
		return nil
	}

	if stepResult := waitTillAllFilesystemsHaveNewCapacity(cc, dataPVCs, rack); stepResult.HasError() {
		return stepResult.Error()
	} else if stepResult.ShouldBreakReconcileLoop() {
		return nil
	}

	if stepResult := waitTillStatefulSetAndAllPodsAreReady(ctx, cc, rack, podsClient); stepResult.HasError() {
		return stepResult.Error()
	}
	return nil
}

// RevertAnyStorageUpsizeBeyondUpsizeAction reverts any storage capacity changes if upsize action IS NOT started
// current action should finish, then upsize action should be started and then these changes should be applied
func RevertAnyStorageUpsizeBeyondUpsizeAction(rack view.RackView, newStatefulSet *appsv1.StatefulSet) {
	if !IsStarted(rack.RackStatus()) {
		_, current := findDataCapacity(rack.StoredStatefulSet().Spec.VolumeClaimTemplates)
		index, requested := findDataCapacity(newStatefulSet.Spec.VolumeClaimTemplates)
		if !requested.Equal(current) {
			if newStatefulSet.Spec.VolumeClaimTemplates[index].Spec.Resources.Requests == nil {
				newStatefulSet.Spec.VolumeClaimTemplates[index].Spec.Resources.Requests = corev1.ResourceList{}
			}
			newStatefulSet.Spec.VolumeClaimTemplates[index].Spec.Resources.Requests[corev1.ResourceStorage] = current
			rack.Log().
				Infof("Storage Resize request detected, postponing resize from %s to %s until other actions are done",
					requested.String(), current.String())
		}
	}
}
