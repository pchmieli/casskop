package upsize

import (
	"context"
	"errors"
	"fmt"

	api "github.com/cscetbon/casskop/api/v2"
	"github.com/cscetbon/casskop/controllers/cassandracluster/pods"
	"github.com/cscetbon/casskop/controllers/cassandracluster/storage/actionstep"
	storagechange "github.com/cscetbon/casskop/controllers/cassandracluster/storage/change"
	"github.com/cscetbon/casskop/controllers/cassandracluster/sts"
	"github.com/cscetbon/casskop/controllers/cassandracluster/view"
)

func waitTillStatefulSetAndAllPodsAreReady(ctx context.Context, cc *api.CassandraCluster, rack view.RackView,
	podsClient pods.PodsClient) actionstep.StepResult {

	if !storagechange.DoesStatefulSetHaveNewCapacity(cc, rack.LivingStatefulSet()) {
		rack.Log().Infof("Resize action is in progress, statefulset need to be re-created with new capacity")
		return actionstep.Break()
	}

	if sts.IsStatefulSetReady(rack.LivingStatefulSet()) {
		podList, err := podsClient.ListPods(ctx, cc.Namespace, rack.GetLabelsForCassandraDCRack(cc))
		if err != nil {
			return actionstep.Error(err)
		}
		expectedNodesPerRacks := *rack.LivingStatefulSet().Spec.Replicas
		if len(podList.Items) != int(expectedNodesPerRacks) {
			errMsg := fmt.Sprintf("Number of pods (%d) different than expected Replicas (%d) for DC-Rack %s",
				len(podList.Items), expectedNodesPerRacks, rack.DcRackName())
			rack.Log().Warn(errMsg)
			return actionstep.Error(errors.New(errMsg))
		}
		if storagechange.AllPodsReady(podList) {
			rack.Log().Info("Resize action finalization, " +
				"all pods are ready with new DataCapacity, we can finalize the action")
			return storagechange.FinalizeMigrationAction(rack.RackStatus(), api.ActionStorageUpsize)
		}
	}

	rack.Log().Info("Resize action is in progress, " +
		"we wait for all pods to be ready with new DataCapacity before finalizing the action")
	return actionstep.Break()
}
