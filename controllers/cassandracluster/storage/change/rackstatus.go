package change

import (
	"errors"

	api "github.com/cscetbon/casskop/api/v2"
	"github.com/cscetbon/casskop/controllers/cassandracluster/storage/actionstep"
	"github.com/cscetbon/casskop/controllers/cassandracluster/view"
	json "github.com/json-iterator/go"
	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
)

func MakeOldStatefulSetSnapshot(rack view.RackView) actionstep.StepResult {
	if isStatefulSetSnapshottedAlready(rack.RackStatus()) {
		return actionstep.Pass()
	}

	livingStatefulSet := rack.LivingStatefulSet()
	if livingStatefulSet == nil {
		return actionstep.Error(errors.New("StatefulSet snapshot not exist and StatefulSet itself is not found, cannot proceed with storage upsize"))
	}
	statefulSetSnapshotJson, err := prepareStatefulSetSnapshot(livingStatefulSet)
	if err != nil {
		return actionstep.Error(err)
	}
	rack.RackStatus().StatefulSetSnapshotBeforeStorageResize = statefulSetSnapshotJson
	return actionstep.Break()
}

func isStatefulSetSnapshottedAlready(dcRackStatus *api.CassandraRackStatus) bool {
	return dcRackStatus.StatefulSetSnapshotBeforeStorageResize != ""
}

func UnmarshallSnapshottedStatefulSet(rack view.RackView) (*appsv1.StatefulSet, error) {
	marshalledStatefulSet := rack.RackStatus().StatefulSetSnapshotBeforeStorageResize
	newStatefulSet := &appsv1.StatefulSet{}
	err := json.Unmarshal([]byte(marshalledStatefulSet), newStatefulSet)
	if err != nil {
		return nil, errors.New("cannot unmarshall snapshotted statefulSet for storage upsize: " + err.Error())
	}
	return newStatefulSet, nil
}

func StartAction(rack view.RackView, action api.ClusterStateInfo) {
	lastAction := &rack.RackStatus().CassandraLastAction
	rack.RackStatus().Phase = api.ClusterPhasePending.Name
	lastAction.Status = api.StatusOngoing
	lastAction.Name = action.Name
	lastAction.StartTime = ptr.To(metav1.Now())
	lastAction.EndTime = nil
}

func FinalizeMigrationAction(rackStatus *api.CassandraRackStatus, action api.ClusterStateInfo) actionstep.StepResult {
	lastAction := &rackStatus.CassandraLastAction
	lastAction.Status = api.StatusDone
	lastAction.Name = action.Name
	lastAction.EndTime = ptr.To(metav1.Now())
	rackStatus.StatefulSetSnapshotBeforeStorageResize = ""
	rackStatus.StorageMigrationState = nil
	return actionstep.Pass()
}
