package migration

import (
	"context"

	api "github.com/cscetbon/casskop/api/v2"
	"github.com/cscetbon/casskop/controllers/cassandracluster/pods"
	as "github.com/cscetbon/casskop/controllers/cassandracluster/storage/actionstep"
	sc "github.com/cscetbon/casskop/controllers/cassandracluster/storage/change"
	"github.com/cscetbon/casskop/controllers/cassandracluster/storagestateclient"
	"github.com/cscetbon/casskop/controllers/cassandracluster/sts"
	"github.com/cscetbon/casskop/controllers/cassandracluster/view"
	"k8s.io/apimachinery/pkg/api/resource"
)

// TODO: whole-rack-at-once algorithm, probably to be deleted eventually

func ReconcileWholeRack(ctx context.Context, cc *api.CassandraCluster, rack view.RackView, newStorageClass string,
	newDataCapacity resource.Quantity, storageStateClient storagestateclient.StorageStateClient,
	stsClient sts.StsClient, podsClient pods.PodsClient) error {

	//TODO later: fetch data at the beginning? (data that is used by mutiple steps, e.g. pvcs - see FetchDataPvcs in storage upsize algorithm)

	dataConfigChange := sc.NewDataPVCConfigMigrationChange(newStorageClass, newDataCapacity)

	steps := []func() as.StepResult{

		//TODO: check retain policy (or ensure if casskop can edit)

		//TODO later: check if old pvcs (temp-retain from previous runs) still exist... maybe add some label with uuid to distinguish between runds?

		func() as.StepResult { return sc.MakeOldStatefulSetSnapshot(rack) },
		func() as.StepResult { return sc.RemoveStatefulSet(ctx, cc, rack, dataConfigChange, stsClient) },
		func() as.StepResult {
			return CreateAndDeleteTempStatefulSetWithNewDataConfig(ctx, cc, rack, dataConfigChange, stsClient, storageStateClient)
		},
		func() as.StepResult {
			return SwapOldAndNewPVCs(ctx, cc, rack, dataConfigChange, stsClient, storageStateClient, podsClient)
		},

		func() as.StepResult {
			return removeNodes(rack, podsClient, ctx, cc)
		},

		//TODO: maybe we need join_ring=false to avoid issues? then we should join all new nodes at once

		func() as.StepResult {
			return sc.RecreateStatefulSetWithDataConfig(ctx, rack, dataConfigChange, stsClient)
		},

		//TODO: temporarily shown twice
		func() as.StepResult {
			return logNetstats(podsClient, ctx, cc, rack)
		},

		func() as.StepResult {
			return waitTillStatefulSetAndAllPodsAreReady(ctx, cc, rack, podsClient)
		},

		func() as.StepResult {
			return logNetstats(podsClient, ctx, cc, rack)
		},

		func() as.StepResult {
			return triggerAndMonitorRackRepair(ctx, cc, rack, podsClient)
		},
		//TODO: should we run nodetool cleanup on other (or all) racks after repair on this rack?

		func() as.StepResult { return sc.FinalizeMigrationAction(rack.RackStatus(), api.ActionStorageMigration) },
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
