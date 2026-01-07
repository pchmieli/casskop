package migration

import (
	"context"
	"encoding/json"
	"fmt"

	api "github.com/cscetbon/casskop/api/v2"
	"github.com/cscetbon/casskop/controllers/cassandracluster/consts"
	"github.com/cscetbon/casskop/controllers/cassandracluster/pods"
	"github.com/cscetbon/casskop/controllers/cassandracluster/storage/actionstep"
	sc "github.com/cscetbon/casskop/controllers/cassandracluster/storage/change"
	"github.com/cscetbon/casskop/controllers/cassandracluster/storagestateclient"
	"github.com/cscetbon/casskop/controllers/cassandracluster/sts"
	"github.com/cscetbon/casskop/controllers/cassandracluster/view"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/utils/ptr"
)

// TODO: needed for whole-rack-at-once algorithm, probably to be deleted eventually

func SwapOldAndNewPVCs(ctx context.Context, cc *api.CassandraCluster, rack view.RackView,
	dataPVCConfigDiff sc.DataPVCConfigurationChange, stsClient sts.StsClient,
	storageStateClient storagestateclient.StorageStateClient, podsClient pods.PodsClient) actionstep.StepResult {

	if rack.LivingStatefulSet() != nil {
		if sc.DoesStatefulSetHaveNewDataPVCConfig(rack, dataPVCConfigDiff) {
			return actionstep.Pass()
		}
		return actionstep.Break()
	}

	desiredStatefulSet, err := getDesiredStatefulSet(rack, dataPVCConfigDiff)
	if err != nil {
		return actionstep.Error(err)
	}
	tempStsExists, err := stsClient.CheckStatefulSetExists(ctx, cc.Namespace, getTempRetainStsName(desiredStatefulSet))
	if err != nil {
		return actionstep.Error(err)
	}
	if tempStsExists {
		// should be deleted in previous steps
		return actionstep.Break()
	}

	// TODO: wait for both sts pods deletion
	regularPodList, err := podsClient.ListPods(ctx, cc.Namespace, rack.GetLabelsForCassandraDCRack(cc))
	if err != nil {
		return actionstep.Error(err)
	}
	if len(regularPodList.Items) > 0 {
		rack.Log().Info("Waiting for regular sts pods deletion before swapping PVCs")
		return actionstep.Break()
	}

	//TODO later: reduce duplication
	appLabel := desiredStatefulSet.Spec.Selector.MatchLabels["app"]
	dcLabel := desiredStatefulSet.Spec.Selector.MatchLabels["dc-rack"]
	tempStsMatchLabels := map[string]string{
		"app":             appLabel,
		"dc-rack":         dcLabel,
		"temp-retain-sts": "true",
	}
	tempStsPodList, err := podsClient.ListPods(ctx, cc.Namespace, tempStsMatchLabels)
	if err != nil {
		return actionstep.Error(err)
	}
	if len(tempStsPodList.Items) > 0 {
		rack.Log().Info("Waiting for temp sts pods deletion before swapping PVCs")
		return actionstep.Break()
	}

	// Swap PVs between old PVCs (data-{sts-name}-{ordinal}) and temp retaining PVCs (data-temp-retain-{sts-name}-{ordinal})
	// Strategy:
	// 1. List all old PVCs and temp retaining PVCs
	// 2. For each pair (old, temp):
	//    a. Get PV names from both PVCs
	//    b. Delete both PVCs (PVs remain with Retain policy)
	//    c. Create new PVC with old name pointing to temp PV (new storage class/capacity)
	//    d. Delete the old PV (or keep it as backup based on policy)

	replicas := *desiredStatefulSet.Spec.Replicas

	// Extract desired storage class and capacity from desiredStatefulSet
	desiredStorageClass, desiredCapacity, err := extractDesiredStorageConfig(desiredStatefulSet)
	if err != nil {
		return actionstep.Error(fmt.Errorf("failed to extract desired storage config: %w", err))
	}

	rack.Log().Infof("Starting PVC swap for %d replicas with desired storage class=%s, capacity=%s",
		replicas, desiredStorageClass, desiredCapacity.String())

	allReady := true
	for ordinal := int32(0); ordinal < replicas; ordinal++ {
		//TODO later: consider fetching using labels instead of constructing names
		originalPVCName := fmt.Sprintf("data-%s-%d", desiredStatefulSet.Name, ordinal)
		tempPVCName := fmt.Sprintf("data-temp-retain-%s-%d", desiredStatefulSet.Name, ordinal)
		podName := fmt.Sprintf("%s-%d", desiredStatefulSet.Name, ordinal)

		ready, err := swapSinglePVCPair(ctx, originalPVCName, tempPVCName, podName, storageStateClient, rack)
		if err != nil {
			return actionstep.Error(fmt.Errorf("failed to swap PVC pair %s <-> %s: %w", originalPVCName, tempPVCName, err))
		}
		allReady = allReady && ready
	}

	if allReady {
		rack.Log().Info("PVC swap completed successfully")
		return actionstep.Pass()
	}
	rack.Log().Info("PVC swap in progress, waiting for completion")
	return actionstep.Break()
}

type PvcVolumeChange struct {
	PvcSnapshot              corev1.PersistentVolumeClaim
	PvcCurrent               *corev1.PersistentVolumeClaim
	TargetPvName             string
	TargetPvStorageClassName string
	TargetPvRequestStorage   resource.Quantity
}

func (c *PvcVolumeChange) PvcExistsAndBoundToTargetPV() bool {
	return c.PvcExistsAndHasTargetPVSpecified() && c.PvcCurrent.Status.Phase == corev1.ClaimBound
}

func (c *PvcVolumeChange) PvcExistsAndHasTargetPVSpecified() bool {
	return c.PvcCurrent != nil && c.PvcCurrent.Spec.VolumeName == c.TargetPvName
}

func (c *PvcVolumeChange) PvcExistsButWithoutTargetPvSpecified() bool {
	return c.PvcCurrent != nil && c.PvcCurrent.Spec.VolumeName != c.TargetPvName
}

func NewPvcChange(ctx context.Context, storageStateClient storagestateclient.StorageStateClient,
	pvcDump, startPvName, targetPvName string) (PvcVolumeChange, error) {

	pvcSnapshot, err := deserializePVCFromJSON(pvcDump)
	if err != nil {
		return PvcVolumeChange{}, fmt.Errorf("failed to deserialize PVC from dump: %w", err)
	}

	//TODO later: repeating pattern (404 check) - try to make it generic utility function?
	pvcCurrent, err := storageStateClient.GetPVC(ctx, pvcSnapshot.Namespace, pvcSnapshot.Name)
	if err != nil {
		if isNotFoundError(err) {
			pvcCurrent = nil
		} else {
			return PvcVolumeChange{}, fmt.Errorf("failed to get PVC %s: %w", pvcSnapshot.Name, err)
		}
	}

	return PvcVolumeChange{
		PvcSnapshot:  *pvcSnapshot,
		PvcCurrent:   pvcCurrent,
		TargetPvName: targetPvName,
	}, nil
}

// before calling: originalPVC is bound to old PV, tempPVC is bound to new PV
// after calling: originalPVC is bound to new PV, tempPVC is bound to old PV (both to be deleted manually)
// TODO: re-think this comment if auto-deletion is added
// TODO later: too many params
func swapSinglePVCPair(ctx context.Context, originalPVCName, tempPVCName, podName string,
	storageStateClient storagestateclient.StorageStateClient, rack view.RackView) (ready bool, err error) {

	rackStatus := rack.RackStatus()

	//TODO later: move it to caller
	if rackStatus.StorageMigrationState == nil && rackStatus.StorageMigrationState.Pods == nil {
		return false, fmt.Errorf("cannot swap PVCs: StorageMigrationState.Pods not exists for rack %s",
			rack.DcRackName())
	}
	podMigrationState, exists := rackStatus.StorageMigrationState.Pods[podName]
	if !exists {
		return false, fmt.Errorf("cannot swap PVCs: pod %s not found in StorageMigrationState for rack %s",
			podName, rack.DcRackName())
	}

	originalPVCChange, err := NewPvcChange(ctx, storageStateClient,
		podMigrationState.RegularPvcDump, podMigrationState.OldPvName, podMigrationState.NewPvName)
	if err != nil {
		return false, err
	}
	tempPVCChange, err := NewPvcChange(ctx, storageStateClient,
		podMigrationState.TempPvcDump, podMigrationState.NewPvName, podMigrationState.OldPvName)
	if err != nil {
		return false, err
	}
	allPvcChanges := []*PvcVolumeChange{&originalPVCChange, &tempPVCChange} //TODO later: change to values, when all fields are read-only

	if originalPVCChange.PvcExistsAndBoundToTargetPV() && tempPVCChange.PvcExistsAndBoundToTargetPV() {
		rack.Log().Infof("Both PVCs %s and %s already swapped & bound, skipping", originalPVCName, tempPVCName)
		return true, nil
	}

	if originalPVCChange.PvcExistsAndHasTargetPVSpecified() && tempPVCChange.PvcExistsAndHasTargetPVSpecified() {
		rack.Log().Infof("Both PVCs %s and %s already swapped but not yet bound, skipping, need to wait more",
			originalPVCName, tempPVCName)
		return false, nil
	}

	deletionMade := false
	for _, change := range allPvcChanges {
		if change.PvcExistsButWithoutTargetPvSpecified() {
			err = storageStateClient.DeletePVC(ctx, change.PvcCurrent)
			if err != nil && !isNotFoundError(err) {
				return false, fmt.Errorf("failed to delete temp PVC %s: %w", tempPVCName, err)
			}
			deletionMade = true
		}
	}
	if deletionMade {
		return false, nil
	}

	claimRefClearMade := false
	for _, change := range allPvcChanges {
		pv, err := storageStateClient.GetPV(ctx, change.TargetPvName)
		if err != nil {
			return false, fmt.Errorf("failed to get PV %s: %w", change.TargetPvName, err)
		}

		//TODO later: shouldn't be created here... maybe fetch pv right away when creating PvcVolumeChange?
		change.TargetPvStorageClassName = pv.Spec.StorageClassName
		change.TargetPvRequestStorage = pv.Spec.Capacity[corev1.ResourceStorage]

		// Clear claimRefs to allow PVs to be bound to new PVCs
		if pv.Spec.ClaimRef != nil {
			rack.Log().Infof("Clearing claimRef on PV: %s", pv.Name)
			pv.Spec.ClaimRef = nil
			err = storageStateClient.UpdatePV(ctx, pv)
			if err != nil {
				return false, fmt.Errorf("failed to clear claimRef on PV %s: %w", pv.Name, err)
			}
			claimRefClearMade = true
		} else {
			rack.Log().Infof("ClaimRef already cleared on PV: %s", pv.Name)
		}
	}
	if claimRefClearMade {
		return false, nil
	}

	//TODO later: wait for PVs has no volume attachments (here, or in caller?)

	for _, change := range allPvcChanges {
		//TODO corner case (idempotence): if one is created successfully, and the second fails - we would try to create the first one again in next loop
		updatedPvc := change.PvcSnapshot.DeepCopy()

		updatedPvc.Spec.VolumeName = change.TargetPvName
		updatedPvc.Spec.StorageClassName = ptr.To(change.TargetPvStorageClassName)
		updatedPvc.Spec.Resources.Requests[corev1.ResourceStorage] = change.TargetPvRequestStorage

		//TODO later: Clear fields that should not be copied before doing snapshot
		updatedPvc.ResourceVersion = ""
		updatedPvc.UID = ""
		updatedPvc.Status = corev1.PersistentVolumeClaimStatus{}

		rack.Log().Infof("Re-creating PVC %s pointing to PV %s", updatedPvc.Name, change.TargetPvName)
		err = storageStateClient.CreatePVC(ctx, updatedPvc)
		if err != nil {
			return false, fmt.Errorf("failed to re-create PVC %s: %w", updatedPvc.Name, err)
		}
	}
	return false, nil

	//TODO later: another check for desiredCapacity and desiredStorageClass ??? or it's responsibility of previous step (temp sts creator)
}

//TODO later: desired sts and accessing various things from it (e.g for temp sts or for pvc operations) should be encapssulated in some interface
// maybe rack interface to be extended?

// extractDesiredStorageConfig extracts the desired storage class and capacity from the StatefulSet's data PVC template
func extractDesiredStorageConfig(sts *appsv1.StatefulSet) (string, resource.Quantity, error) {
	// Find the data PVC template
	for _, pvcTemplate := range sts.Spec.VolumeClaimTemplates {
		if pvcTemplate.Name == consts.DataPVCName {
			storageClass := ""
			if pvcTemplate.Spec.StorageClassName != nil {
				storageClass = *pvcTemplate.Spec.StorageClassName
			}

			capacity := pvcTemplate.Spec.Resources.Requests[corev1.ResourceStorage]
			if capacity.IsZero() {
				return "", resource.Quantity{}, fmt.Errorf("data PVC template has zero capacity")
			}

			return storageClass, capacity, nil
		}
	}

	return "", resource.Quantity{}, fmt.Errorf("data PVC template not found in StatefulSet")
}

// isNotFoundError checks if an error is a Kubernetes NotFound (404) error
func isNotFoundError(err error) bool {
	return apierrors.IsNotFound(err)
}

// deserializePVCFromJSON deserializes a PVC from JSON string
func deserializePVCFromJSON(pvcJSON string) (*corev1.PersistentVolumeClaim, error) {
	pvc := &corev1.PersistentVolumeClaim{}
	err := json.Unmarshal([]byte(pvcJSON), pvc)
	if err != nil {
		return nil, err
	}
	return pvc, nil
}
