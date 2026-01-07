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

// ReconcileSinglePod implements node-by-node replacement using replace_address_first_boot
// This approach minimizes data shuffles by preserving token ranges from old nodes

func ReconcileSinglePod(ctx context.Context, cc *api.CassandraCluster, rack view.RackView, newStorageClass string,
	newDataCapacity resource.Quantity, storageStateClient storagestateclient.StorageStateClient,
	stsClient sts.StsClient, podsClient pods.PodsClient) error {

	//TODO later: fetch data at the beginning? (data that is used by mutiple steps, e.g. pvcs - see FetchDataPvcs in storage upsize algorithm)

	//TODO: test with 1-node/rack - check if still works and doesn't cause issues with single node rack (whole rack will be down)

	//TODO: either forbid changing sc/size if migration is in progress
	// or handle it: can't rely on boolean flags e.g. StsDeletedWithOrphan=true
	// or we may rely on them, but reset in case of "double update"
	//
	// that is valid for all state booleans, so all need to be reviewed...
	// in case we allow to switch flags off back, let's make sure steps are idempotent
	// (maybe step with pvc deletion is specific, as we first of all don't want to loose data)

	dataConfigChange := sc.NewDataPVCConfigMigrationChange(newStorageClass, newDataCapacity)

	// Get current pod being migrated (or empty string if all pods are migrated)
	// Empty string allows steps to pass through to finalization step
	currentPodName := ""
	if rack.RackStatus().StorageMigrationState != nil {
		currentPodName = getCurrentPodToMigrate(rack)
	}

	//TODO later: enrich logger with currentPodName!

	steps := []func() as.StepResult{
		func() as.StepResult { return sc.MakeOldStatefulSetSnapshot(rack) },
		func() as.StepResult { return initializeMigrationState(ctx, rack, cc, podsClient) },

		func() as.StepResult { return dumpPodTemplate(ctx, rack, cc, currentPodName, podsClient) },

		func() as.StepResult { return deleteStatefulSetWithOrphan(ctx, rack, cc, currentPodName, stsClient) },
		func() as.StepResult { return waitForStatefulSetDeletion(ctx, rack, cc, stsClient) }, //TODO: necessary? it should be checked in previous step

		func() as.StepResult { return recordOldPodIPAndHostID(ctx, rack, cc, currentPodName, podsClient) },
		func() as.StepResult {
			return captureDiagnosticsBeforeMigration(ctx, rack, cc, currentPodName, podsClient)
		},
		func() as.StepResult { return deletePodByName(ctx, rack, cc, currentPodName, podsClient) },
		func() as.StepResult { return waitForPodDeletion(ctx, rack, cc, currentPodName, podsClient) }, //TODO: necessary? it should be checked in previous step

		func() as.StepResult {
			return ensurePVCHasRetainPolicy(ctx, rack, cc, currentPodName, storageStateClient)
		}, //TODO: maybe ensure it before deleting anything?

		func() as.StepResult {
			return deletePodPVC(ctx, rack, cc, currentPodName, &dataConfigChange, storageStateClient)
		},
		func() as.StepResult {
			return recreatePVCWithNewConfig(ctx, rack, cc, currentPodName, &dataConfigChange, storageStateClient)
		},
		func() as.StepResult {
			return recreateIndividualPodWithExtraReplaceNodeParams(ctx, rack, cc, currentPodName, podsClient)
		},
		func() as.StepResult { return waitForPodReadyAndUN(ctx, rack, cc, currentPodName, podsClient) },

		func() as.StepResult {
			return captureDiagnosticsAfterMigration(ctx, rack, cc, currentPodName, podsClient)
		},
		//TODO: helper to analyze token ranges before&after
		// first, it should take all snapshots, take all ranges, sort them and map to natural numbers
		// then should output: 1 rack2, 2 rack2, 3 rack1, 4 rack3 so it's easier to read&analyze

		func() as.StepResult {
			// Get thresholds from CR using helper methods (with defaults and parsing)
			podThreshold := cc.GetStorageMigrationPodLoadThreshold()
			otherNodesThreshold := cc.GetStorageMigrationOtherNodesLoadThreshold()
			return waitUntilLoadDifferenceIsWithinConfiguredThresholds(ctx, rack, cc, currentPodName, podsClient, podThreshold, otherNodesThreshold)
		},

		func() as.StepResult { return markPodAsMigrated(rack, currentPodName) },

		func() as.StepResult {
			return recreateStatefulSetAfterAllMigrated(ctx, rack, stsClient, newStorageClass, newDataCapacity)
		},
		func() as.StepResult {
			return waitForStatefulSetReadyAfterRecreation(ctx, rack, cc, stsClient, podsClient)
		},
		func() as.StepResult { return checkAndFinalizeMigration(rack) },

		/*
			TODO: read and check if max_hint_window_in_ms can be a problem in our case

			https://cassandra.apache.org/doc/4.0/cassandra/operating/topo_changes.html#replacing-a-dead-node
			If any of the following cases apply, you MUST run repair to make the replaced node consistent again,
				since it missed ongoing writes during/prior to bootstrapping.
				The replacement timeframe refers to the period from when the node initially dies to when a new node completes the replacement process.

					The node is down for longer than max_hint_window_in_ms before being replaced.

					You are replacing using the same IP address as the dead node and replacement takes longer than max_hint_window_in_ms.
		*/
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
