package upsize

import (
	"context"

	api "github.com/cscetbon/casskop/api/v2"
	"github.com/cscetbon/casskop/controllers/cassandracluster/storage/actionstep"
	storagechange "github.com/cscetbon/casskop/controllers/cassandracluster/storage/change"
	"github.com/cscetbon/casskop/controllers/cassandracluster/storagestateclient"
	"github.com/cscetbon/casskop/controllers/cassandracluster/view"
	"go.uber.org/multierr"
	corev1 "k8s.io/api/core/v1"
)

func ensureAllPVCsHaveNewCapacity(ctx context.Context, cc *api.CassandraCluster, dataPVCs []corev1.PersistentVolumeClaim,
	rack view.RackView, storageStateClient storagestateclient.StorageStateClient) actionstep.StepResult {

	requestedCapacity := storagechange.SilentParseResourceQuantity(cc.GetDataCapacityForDCName(rack.DcName()))

	anythingChanged := false
	var multiError error
	for _, pvc := range dataPVCs {
		if pvc.Spec.Resources.Requests["storage"] != requestedCapacity {
			anythingChanged = true
			if pvc.Spec.Resources.Requests == nil {
				pvc.Spec.Resources.Requests = corev1.ResourceList{}
			}
			pvc.Spec.Resources.Requests["storage"] = requestedCapacity
			err := storageStateClient.UpdatePVC(ctx, &pvc)
			if err != nil {
				rack.Log().Errorf("Error updating PVC[%s] capacity to %v", pvc.Name, requestedCapacity)
				multiError = multierr.Append(multiError, err)
			}
			rack.Log().Infof("Update PVC[%s] capacity to %v successful", pvc.Name, requestedCapacity)
		}
	}

	if multiError != nil {
		return actionstep.Error(multiError)
	}

	if anythingChanged {
		return actionstep.Break()
	}
	return actionstep.Pass()
}

func waitTillAllFilesystemsHaveNewCapacity(cc *api.CassandraCluster, dataPVCs []corev1.PersistentVolumeClaim,
	rack view.RackView) actionstep.StepResult {

	requestedCapacity := storagechange.SilentParseResourceQuantity(cc.GetDataCapacityForDCName(rack.DcName()))

	var resized, notResizedYet []string

	for _, pvc := range dataPVCs {
		if pvc.Status.Capacity["storage"].Equal(requestedCapacity) {
			resized = append(resized, pvc.Name)
		} else {
			notResizedYet = append(notResizedYet, pvc.Name)
		}
	}

	if len(notResizedYet) == 0 {
		rack.Log().Infof("All PVs for DC-Rack %s resized to %s", rack.DcRackName(), requestedCapacity.String())
		return actionstep.Pass()
	}

	rack.Log().Infof("Still waiting for PVs to be resized for DC-Rack %s. Resized: [%v], Not resized yet: [%v]",
		rack.DcRackName(), resized, notResizedYet)
	return actionstep.Break()
}
