package cassandracluster

import (
	"context"

	api "github.com/cscetbon/casskop/api/v2"
	"github.com/cscetbon/casskop/controllers/cassandracluster/pods"
	"github.com/cscetbon/casskop/controllers/cassandracluster/storagestateclient"
	"github.com/cscetbon/casskop/controllers/cassandracluster/storageupsize"
	"github.com/cscetbon/casskop/controllers/cassandracluster/sts"
	"github.com/cscetbon/casskop/controllers/cassandracluster/view"
	"github.com/sirupsen/logrus"
	appsv1 "k8s.io/api/apps/v1"
)

func (rcc *CassandraClusterReconciler) RevertAnyStorageUpsizeBeyondUpsizeAction(dcName, rackName, dcRackName string,
	dcRackStatus *api.CassandraRackStatus, statefulSet *appsv1.StatefulSet) {

	storageupsize.RevertAnyStorageUpsizeBeyondUpsizeAction(rcc.newRackView(dcName, rackName, dcRackName, dcRackStatus), statefulSet)
}

func (rcc *CassandraClusterReconciler) UpdateStatusIfStorageUpsize(dcName, rackName, dcRackName string,
	status *api.CassandraClusterStatus) bool {

	rackView := rcc.newRackView(dcName, rackName, dcRackName, status.CassandraRackStatus[dcRackName])
	if rcc.ShouldStorageUpsizeBeStarted(rackView) {
		rcc.StartStorageUpsize(rackView)
		return true
	}
	return false
}

func (rcc *CassandraClusterReconciler) ShouldStorageUpsizeBeStarted(rackView view.RackView) bool {
	return storageupsize.ShouldBeStarted(rackView, rcc.cc.GetDataCapacityForDC(rackView.DcName()))

}

func (rcc *CassandraClusterReconciler) StartStorageUpsize(rackView view.RackView) {
	storageupsize.Start(rackView)
	ClusterPhaseMetric.set(api.ClusterPhasePending, rcc.cc.Name)
	ClusterActionMetric.set(api.ActionStorageUpsize, rcc.cc.Name)
}

func (rcc *CassandraClusterReconciler) IsStorageUpsizeStarted(dcRackStatus *api.CassandraRackStatus) bool {
	return storageupsize.IsStarted(dcRackStatus)
}

func (rcc *CassandraClusterReconciler) ReconcileStorageUpsize(ctx context.Context, cc *api.CassandraCluster,
	status *api.CassandraClusterStatus, dcName string, rackName string) error {

	dcRackName := cc.GetDCRackName(dcName, rackName)
	status.CassandraRackStatus[dcRackName].Phase = api.ClusterPhasePending.Name
	ClusterPhaseMetric.set(api.ClusterPhasePending, cc.Name)

	newDataCapacity := generateResourceQuantity(cc.GetDataCapacityForDC(dcName))
	setNewDataCapacity := storageupsize.DataCapacitySetter(newDataCapacity)

	rackView := rcc.newRackView(dcName, rackName, dcRackName, status.CassandraRackStatus[dcRackName])
	var storageStateClient storagestateclient.StorageStateClient = rcc
	var stsClient sts.StsClient = rcc
	var podsClient pods.PodsClient = rcc

	return storageupsize.Reconcile(ctx, cc, rackView, setNewDataCapacity, storageStateClient, stsClient, podsClient)
}

func (rcc *CassandraClusterReconciler) newRackView(dcName, rackName, dcRackName string,
	dcRackStatus *api.CassandraRackStatus) view.RackView {

	return &rccRackView{
		rcc:          rcc,
		dcName:       dcName,
		rackName:     rackName,
		dcRackName:   dcRackName,
		dcRackStatus: dcRackStatus,
	}
}

type rccRackView struct {
	rcc          *CassandraClusterReconciler
	dcName       string
	rackName     string
	dcRackName   string
	dcRackStatus *api.CassandraRackStatus
}

var _ view.RackView = (*rccRackView)(nil)

func (v *rccRackView) ClusterName() string {
	return v.rcc.cc.Name
}

func (v *rccRackView) DcName() string {
	return v.dcName
}

func (v *rccRackView) RackName() string {
	return v.rackName
}

func (v *rccRackView) DcRackName() string {
	return v.dcRackName
}

func (v *rccRackView) RackStatus() *api.CassandraRackStatus {
	return v.dcRackStatus
}

func (v *rccRackView) StoredStatefulSet() *appsv1.StatefulSet {
	return v.rcc.storedStatefulSet
}

func (v *rccRackView) StoredStatefulSetExists() bool {
	return v.rcc.storedStatefulSet != nil
}

func (v *rccRackView) Log() *logrus.Entry {
	return logrus.WithFields(logrus.Fields{"cluster": v.ClusterName(), "dc-rack": v.DcRackName()})
}
