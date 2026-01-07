package upsize

import (
	"context"

	api "github.com/cscetbon/casskop/api/v2"
	"github.com/cscetbon/casskop/controllers/cassandracluster/pods"
	as "github.com/cscetbon/casskop/controllers/cassandracluster/storage/actionstep"
	sc "github.com/cscetbon/casskop/controllers/cassandracluster/storage/change"
	"github.com/cscetbon/casskop/controllers/cassandracluster/storagestateclient"
	"github.com/cscetbon/casskop/controllers/cassandracluster/sts"
	"github.com/cscetbon/casskop/controllers/cassandracluster/view"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
)

func ShouldBeStarted(rack view.RackView, requestedCapacity string) bool {
	requested := sc.SilentParseResourceQuantity(requestedCapacity)
	_, current := sc.FindDataCapacity(rack.LivingStatefulSet().Spec.VolumeClaimTemplates)

	//TODO: should allow only for storage upsize (remember about unit tests)
	if !requested.Equal(current) {
		rack.Log().Infof("Storage upsize should be started: ask %v and have %v", requested, current)
		return true
	}
	return false
}

func Start(rack view.RackView) {
	sc.StartAction(rack, api.ActionStorageUpsize)
}

func IsStarted(dcRackStatus *api.CassandraRackStatus) bool {
	return dcRackStatus.CassandraLastAction.Name == api.ActionStorageUpsize.Name &&
		dcRackStatus.CassandraLastAction.Status != api.StatusDone
}

// Reconcile performs the storage upsize action steps
// Each step may
// - return an error
// - execute an action and break the loop (if action was not finished before or even not started yet)
// - do nothing and continue to the next step pass (if action was finished before)
// Usually step do its job once and break the loop, then in the next reconcile loop this step "pass" and the next step is executed
func Reconcile(ctx context.Context, cc *api.CassandraCluster, rack view.RackView, newDataCapacity resource.Quantity,
	storageStateClient storagestateclient.StorageStateClient, stsClient sts.StsClient, podsClient pods.PodsClient) error {

	dataPVCs := make([]corev1.PersistentVolumeClaim, 0)
	dataConfigChange := sc.NewDataPVCCapacityChange(newDataCapacity)

	steps := []func() as.StepResult{
		func() as.StepResult { return sc.MakeOldStatefulSetSnapshot(rack) },
		func() as.StepResult { return sc.RemoveStatefulSetOrphan(ctx, cc, rack, dataConfigChange, stsClient) },
		func() as.StepResult {
			return sc.RecreateStatefulSetWithDataConfig(ctx, rack, dataConfigChange, stsClient)
		},
		func() as.StepResult { return sc.FetchDataPvcs(ctx, cc, rack, storageStateClient, &dataPVCs) },
		func() as.StepResult { return ensureAllPVCsHaveNewCapacity(ctx, cc, dataPVCs, rack, storageStateClient) },
		func() as.StepResult { return waitTillAllFilesystemsHaveNewCapacity(cc, dataPVCs, rack) },
		func() as.StepResult { return waitTillStatefulSetAndAllPodsAreReady(ctx, cc, rack, podsClient) },
	}
	for _, executeStep := range steps {
		if stepResult := executeStep(); stepResult.HasError() {
			return stepResult.Error()
		} else if stepResult.ShouldBreakReconcileLoop() {
			return nil
		}
	}

	return nil
}

// RevertAnyStorageUpsizeBeyondUpsizeAction reverts any storage capacity changes if upsize action IS NOT started
// current action should finish, then upsize action should be started and then these changes should be applied
func RevertAnyStorageUpsizeBeyondUpsizeAction(rack view.RackView, newStatefulSet *appsv1.StatefulSet) {
	if !IsStarted(rack.RackStatus()) {
		_, current := sc.FindDataCapacity(rack.LivingStatefulSet().Spec.VolumeClaimTemplates)
		index, requested := sc.FindDataCapacity(newStatefulSet.Spec.VolumeClaimTemplates)
		if !requested.Equal(current) {
			dataPvcResources := &newStatefulSet.Spec.VolumeClaimTemplates[index].Spec.Resources
			if dataPvcResources.Requests == nil {
				dataPvcResources.Requests = corev1.ResourceList{}
			}
			dataPvcResources.Requests[corev1.ResourceStorage] = current
			rack.Log().
				Infof("Storage Resize request detected, postponing resize from %s to %s until other actions are done",
					requested.String(), current.String())
		}
	}
}
