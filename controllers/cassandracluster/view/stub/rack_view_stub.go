package stub

import (
	api "github.com/cscetbon/casskop/api/v2"
	"github.com/cscetbon/casskop/controllers/cassandracluster/view"
	"github.com/sirupsen/logrus"
	appsv1 "k8s.io/api/apps/v1"
)

type RackView struct {
	ClusterNameStub       string
	DcNameStubStub        string
	RackNameStub          string
	DcRackNameStub        string
	RackStatusStub        *api.CassandraRackStatus
	StoredStatefulSetStub *appsv1.StatefulSet
}

var _ view.RackView = RackView{}

func (v RackView) ClusterName() string {
	return v.ClusterNameStub
}

func (v RackView) DcName() string {
	return v.DcNameStubStub
}

func (v RackView) RackName() string {
	return v.RackNameStub
}

func (v RackView) DcRackName() string {
	return v.DcRackNameStub
}

func (v RackView) RackStatus() *api.CassandraRackStatus {
	return v.RackStatusStub
}

func (v RackView) StoredStatefulSet() *appsv1.StatefulSet {
	return v.StoredStatefulSetStub
}

func (v RackView) StoredStatefulSetExists() bool {
	return v.StoredStatefulSetStub != nil
}

func (v RackView) Log() *logrus.Entry {
	return logrus.WithFields(logrus.Fields{})
}
