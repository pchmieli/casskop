package view

import (
	api "github.com/cscetbon/casskop/api/v2"
	"github.com/sirupsen/logrus"
	appsv1 "k8s.io/api/apps/v1"
)

type RackView interface {
	ClusterName() string
	DcName() api.DcName
	RackName() api.RackName
	DcRackName() api.DcRackName
	RackStatus() *api.CassandraRackStatus
	StoredStatefulSet() *appsv1.StatefulSet
	StoredStatefulSetExists() bool
	GetLabelsForCassandraDCRack(cc *api.CassandraCluster) map[string]string
	Log() *logrus.Entry
}
