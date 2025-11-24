package storageupsize

import (
	"errors"

	api "github.com/cscetbon/casskop/api/v2"
	"github.com/cscetbon/casskop/controllers/cassandracluster/storageupsize/actionstep"
	"github.com/cscetbon/casskop/controllers/cassandracluster/view"
	json "github.com/json-iterator/go"
	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
)

func isStatefulSetDumpedAlready(dcRackStatus *api.CassandraRackStatus) bool {
	return dcRackStatus.StatefulSetDumpBeforeStorageResize != ""
}

func dumpOldStatefulSet(rack view.RackView) actionstep.StepResult {
	if isStatefulSetDumpedAlready(rack.RackStatus()) {
		return actionstep.Pass()
	}

	storedStatefulSet := rack.StoredStatefulSet()
	if storedStatefulSet == nil {
		return actionstep.Error(errors.New("StatefulSet dump not exist and StatefulSet itself is not found, cannot proceed with storage upsize"))
	}
	statefulSetDumpJson, err := prepareStatefulSetDump(storedStatefulSet)
	if err != nil {
		return actionstep.Error(err)
	}
	rack.RackStatus().StatefulSetDumpBeforeStorageResize = statefulSetDumpJson
	return actionstep.Break()
}

func unmarshallDumpedStatefulSet(rack view.RackView) (*appsv1.StatefulSet, error) {
	marshalledStatefulSet := rack.RackStatus().StatefulSetDumpBeforeStorageResize
	newStatefulSet := &appsv1.StatefulSet{}
	err := json.Unmarshal([]byte(marshalledStatefulSet), newStatefulSet)
	if err != nil {
		return nil, errors.New("cannot unmarshall dumped statefulSet for storage upsize: " + err.Error())
	}
	return newStatefulSet, nil
}

func startUpsizeAction(rack view.RackView) {
	lastAction := &rack.RackStatus().CassandraLastAction
	rack.RackStatus().Phase = api.ClusterPhasePending.Name
	lastAction.Status = api.StatusOngoing
	lastAction.Name = api.ActionStorageUpsize.Name
	lastAction.StartTime = ptr.To(metav1.Now())
	lastAction.EndTime = nil
}

func finalizeUpsizeAction(rackStatus *api.CassandraRackStatus) {
	lastAction := &rackStatus.CassandraLastAction
	lastAction.Status = api.StatusDone
	lastAction.Name = api.ActionStorageUpsize.Name
	lastAction.EndTime = ptr.To(metav1.Now())
	rackStatus.StatefulSetDumpBeforeStorageResize = ""
}
