package change

import (
	"context"
	"errors"
	"fmt"
	"strings"

	api "github.com/cscetbon/casskop/api/v2"
	"github.com/cscetbon/casskop/controllers/cassandracluster/consts"
	"github.com/cscetbon/casskop/controllers/cassandracluster/storage/actionstep"
	"github.com/cscetbon/casskop/controllers/cassandracluster/storagestateclient"
	"github.com/cscetbon/casskop/controllers/cassandracluster/view"
	"github.com/sirupsen/logrus"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/utils/ptr"
)

func SilentParseResourceQuantity(qs string) resource.Quantity {
	q, _ := resource.ParseQuantity(qs)
	return q
}

func FindDataCapacity(pvc []corev1.PersistentVolumeClaim) (int, resource.Quantity) {
	i := findDataPVCIndex(pvc)
	if i == -1 {
		return -1, resource.Quantity{}
	}
	if pvc[i].Spec.Resources.Requests == nil {
		return i, resource.Quantity{}
	}
	return i, pvc[i].Spec.Resources.Requests[corev1.ResourceStorage]
}

func FindDataStorageClass(pvc []corev1.PersistentVolumeClaim) (int, string) {
	i := findDataPVCIndex(pvc)
	if i == -1 {
		return -1, ""
	}
	return i, ptr.Deref(pvc[i].Spec.StorageClassName, "")
}

func findDataPVCIndex(pvc []corev1.PersistentVolumeClaim) int {
	for i, template := range pvc {
		if template.Name == consts.DataPVCName {
			return i
		}
	}
	return -1
}

// TODO: move back to upsize package? it is not reused by migration for now
func FetchDataPvcs(ctx context.Context, cc *api.CassandraCluster, rack view.RackView,
	storageStateClient storagestateclient.StorageStateClient, outputPVCs *[]corev1.PersistentVolumeClaim) actionstep.StepResult {

	dataPVCs, err := getAllDataPvcs(ctx, cc, rack, storageStateClient)
	if err != nil {
		return actionstep.Error(err)
	}
	*outputPVCs = dataPVCs
	return actionstep.Pass()
}

func getAllDataPvcs(ctx context.Context, cc *api.CassandraCluster, rack view.RackView,
	storageStateClient storagestateclient.StorageStateClient) ([]corev1.PersistentVolumeClaim, error) {

	if rack.LivingStatefulSet() == nil {
		return nil, errors.New(fmt.Sprintf("[%s]: cannot fetch PVC list for storage upsize because"+
			"livingStatefulSet is nil for DC-Rack %s "+
			"(should not see this message, PVC should not be listed before statefulSet is recreated with new data PVC config)",
			cc.Name, rack.DcRackName()))
	}
	statefulSetName := rack.LivingStatefulSet().Name

	pvcs, err := storageStateClient.ListPVC(ctx, cc.Namespace, rack.GetLabelsForCassandraDCRack(cc))
	if err != nil {
		return nil, err
	}

	dataPVCs := make([]corev1.PersistentVolumeClaim, 0)
	for _, pvc := range pvcs.Items {
		if strings.HasPrefix(pvc.Name, consts.DataPVCName+"-"+statefulSetName) {
			dataPVCs = append(dataPVCs, pvc)
		}
	}

	expectedNodesPerRacks := *rack.LivingStatefulSet().Spec.Replicas

	if len(dataPVCs) != int(expectedNodesPerRacks) {
		errMsg := fmt.Sprintf("[%s]: Number of Data PVCs (%d) different than expected Replicas (%d) for DC-Rack %s",
			cc.Name, len(dataPVCs), expectedNodesPerRacks, rack.DcRackName())
		logrus.Warn(errMsg)
		return nil, errors.New(errMsg)
	}

	return dataPVCs, nil
}
