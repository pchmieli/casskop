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

func removeNodes(rack view.RackView, podsClient pods.PodsClient, ctx context.Context, cc *api.CassandraCluster) as.StepResult {
	if rack.IsStatefulSetAliveNow() {
		return as.Pass()
	}

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

	upNodes := status.GetUpNodes()
	if len(upNodes) != 0 {
		rack.Log().Warningf("There should be no UP nodes in the rack during this phase migration, "+
			"casskop won't continue until these are DOWN; UP node IPs: %v", upNodes.GetIps())
		return as.Break()
	}

	downNodes := status.GetDownNodes()

	if len(downNodes) == 0 {
		return as.Pass()
	}

	for _, downNode := range downNodes {
		if downNode.State == "Leaving" {
			rack.Log().Infof("Skipping removal of down node with IP %s as it is in Leaving state", downNode.IP)
			continue
		}
		rack.Log().Infof("Removing down node with IP %s from the ring", downNode.IP)
		//TODO: check if it's blocking and make it async if true
		err := ops.RemoveNode(downNode.HostID)
		if err != nil {
			return as.Error(fmt.Errorf("failed to remove node %s: %v", downNode.IP, err))
		}
		rack.Log().Infof("Successfully removed node %s from the ring", downNode.IP)
	}

	return as.Break()
}

func triggerAndMonitorRackRepair(ctx context.Context, cc *api.CassandraCluster, rack view.RackView, podsClient pods.PodsClient) as.StepResult {

	//TODO: this operation seems to be sync - need to reimplement it so it can be run in async mode (repair may span over log time)

	racks := rack.RackStatus()
	if racks.StorageMigrationState == nil || racks.StorageMigrationState.Pods == nil {
		return as.Error(fmt.Errorf("cannot trigger repair: StorageMigrationState is not initialized for rack %s", rack.DcRackName()))
	}

	podList, err := podsClient.ListPods(ctx, cc.Namespace, rack.GetLabelsForCassandraDCRack(cc))
	if err != nil {
		return as.Error(err)
	}
	if len(podList.Items) == 0 {
		return as.Error(fmt.Errorf("cannot trigger repair: no pods found for rack %s", rack.DcRackName()))
	}

	// Ensure all rack pods are Ready before starting repairs.
	for _, p := range podList.Items {
		if !cassandrapod.IsReady(&p) {
			rack.Log().Infof("Waiting for pod %s to be Ready before triggering repair", p.Name)
			return as.Break()
		}
	}

	// Trigger repairs (best-effort async) for pods that haven't been triggered yet.
	triggeredAny := false
	for _, p := range podList.Items {
		state, ok := racks.StorageMigrationState.Pods[p.Name]
		if !ok {
			// Not fatal: we can still run repair, but we need a place to persist state.
			// This should not happen if earlier steps filled StorageMigrationState correctly.
			return as.Error(fmt.Errorf("cannot trigger repair: pod %s missing in StorageMigrationState for rack %s", p.Name, rack.DcRackName()))
		}
		if state.RepairTriggered {
			continue
		}

		host := k8s.PodHostname(p)
		port := 8778 // TODO later: const
		jolokiaURL := fmt.Sprintf("http://%s:%d/jolokia/", host, port)
		jolokiaClient := go_jolokia.NewJolokiaClient(jolokiaURL)
		ops := nodeoperations.NewNodeOp(jolokiaClient, host)

		cmdID, err := ops.TriggerRepairAsync(true /* primaryRange */)
		if err != nil {
			return as.Error(fmt.Errorf("failed to trigger repair on pod %s: %w", p.Name, err))
		}

		state.RepairTriggered = true
		state.RepairCommandID = cmdID //TODO: never read, do we need it
		racks.StorageMigrationState.Pods[p.Name] = state
		triggeredAny = true
		rack.Log().Infof("Triggered async repair on pod %s (commandId=%s)", p.Name, cmdID)
	}

	if triggeredAny {
		// Persist the updated status and re-enter reconcile to start monitoring.
		return as.Break()
	}

	// Monitor: consider repair done when none of the rack pods is streaming.
	streamingPods := make([]string, 0)
	for _, p := range podList.Items {
		host := k8s.PodHostname(p)
		port := 8778 // TODO later: const
		jolokiaURL := fmt.Sprintf("http://%s:%d/jolokia/", host, port)
		jolokiaClient := go_jolokia.NewJolokiaClient(jolokiaURL)
		ops := nodeoperations.NewNodeOp(jolokiaClient, host)

		hasStreams, err := ops.HasStreamingSessions()
		if err != nil {
			return as.Error(fmt.Errorf("failed to check streaming sessions on pod %s: %w", p.Name, err))
		}
		if hasStreams {
			streamingPods = append(streamingPods, p.Name)
		}
	}

	if len(streamingPods) > 0 {
		rack.Log().Infof("Waiting for repair to finish on rack %s; pods still streaming: %v", rack.DcRackName(), streamingPods)
		return as.Break()
	}

	rack.Log().Infof("Repair finished for all pods in rack %s", rack.DcRackName())
	return as.Pass()
}
