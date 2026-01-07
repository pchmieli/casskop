package migration

import (
	"context"
	"fmt"

	api "github.com/cscetbon/casskop/api/v2"
	"github.com/cscetbon/casskop/controllers/cassandracluster/cassandrapod"
	"github.com/cscetbon/casskop/controllers/cassandracluster/pods"
	as "github.com/cscetbon/casskop/controllers/cassandracluster/storage/actionstep"
	"github.com/cscetbon/casskop/controllers/cassandracluster/view"
	"github.com/cscetbon/casskop/controllers/nodeoperations"
	"github.com/cscetbon/casskop/pkg/k8s"
	"github.com/swarvanusg/go_jolokia"
)

// TODO: needed for whole-rack-at-once algorithm, probably to be deleted eventually

func logNetstats(podsClient pods.PodsClient, ctx context.Context, cc *api.CassandraCluster, rack view.RackView) as.StepResult {
	podList, err := podsClient.ListPods(ctx, cc.Namespace, k8s.LabelsForCassandraDC(cc, rack.DcName().String()))
	if err != nil {
		return as.Error(err)
	}
	//TODO: choose last ready pod instead!
	lastPod := podList.Items[len(podList.Items)-1]
	if !cassandrapod.IsReady(&lastPod) {
		return as.Error(fmt.Errorf("pod %s is not ready", lastPod.Name)) //TODO later: more context in log
	}
	host := k8s.PodHostname(lastPod)
	port := 8778 //TODO later: const

	//TODO later: deduplicate
	jolokiaURL := fmt.Sprintf("http://%s:%d/jolokia/", host, port)

	jolokiaClient := go_jolokia.NewJolokiaClient(jolokiaURL)

	//TODO later: set credential if needed (reuse code from node_operations)
	ops := nodeoperations.NewNodeOp(jolokiaClient, host)

	status, err := ops.GetRackStatus(rack.RackName().String())
	if err != nil {
		return as.Error(fmt.Errorf("failed to get nodetool status for rack %s: %v", rack.RackName().String(), err))
	}
	for _, nodeStatus := range status {
		rack.Log().Infof("Node status: %+v", nodeStatus)
	}

	//TODO: after testing with bigger amount of data, try to analyse netstats and do not pass until data copied
	// be carafeul for cases when node is joint and only new data is distributed
	// Log netstats for monitoring streaming activity (best effort, don't fail if it errors)
	netstats, err := ops.GetNetstats()
	if err != nil {
		rack.Log().Warnf("Failed to get netstats: %v", err)
	} else {
		rack.Log().Infof("Netstats output:\n%s", netstats)
	}
	//TODO: maybe better check it before all pods are ready, so we will have more diagnostics
	joiningNodes := status.GetJoiningNodes()
	if len(joiningNodes) > 0 {
		rack.Log().Infof("There are still joining nodes in the rack, waiting for them to finish joining: %v", joiningNodes.GetIps())
		return as.Break()
	}

	upNormalNodes := status.GetUpNormalNodes()
	if len(upNormalNodes) != len(status) {
		rack.Log().Infof("Not all nodes are Up Normal yet, waiting... ready nodes: %v", upNormalNodes.GetIps())
		return as.Break()
	}
	return as.Pass()
}
