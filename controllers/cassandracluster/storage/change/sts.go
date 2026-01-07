package change

import (
	"context"
	"errors"
	"fmt"

	"github.com/banzaicloud/k8s-objectmatcher/patch"
	api "github.com/cscetbon/casskop/api/v2"
	"github.com/cscetbon/casskop/controllers/cassandracluster/consts"
	"github.com/cscetbon/casskop/controllers/cassandracluster/storage/actionstep"
	"github.com/cscetbon/casskop/controllers/cassandracluster/storage/lastapplied"
	"github.com/cscetbon/casskop/controllers/cassandracluster/sts"
	"github.com/cscetbon/casskop/controllers/cassandracluster/view"
	json "github.com/json-iterator/go"
	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
)

func prepareStatefulSetSnapshot(livingStatefulSet *appsv1.StatefulSet) (string, error) {
	statefulSetSnapshot := livingStatefulSet.DeepCopy()

	statefulSetSnapshot.GenerateName = ""
	statefulSetSnapshot.SelfLink = ""
	statefulSetSnapshot.UID = ""
	statefulSetSnapshot.ResourceVersion = ""
	statefulSetSnapshot.Generation = 0
	statefulSetSnapshot.CreationTimestamp = metav1.Time{}
	statefulSetSnapshot.DeletionTimestamp = nil
	statefulSetSnapshot.DeletionGracePeriodSeconds = nil
	statefulSetSnapshot.ManagedFields = nil

	statefulSetSnapshot.TypeMeta = metav1.TypeMeta{}
	statefulSetSnapshot.Status = appsv1.StatefulSetStatus{}

	statefulSetSnapshotJson, err := json.ConfigCompatibleWithStandardLibrary.Marshal(statefulSetSnapshot)
	if err != nil {
		return "", err
	}
	return string(statefulSetSnapshotJson), nil
}

func RemoveStatefulSet(ctx context.Context, cc *api.CassandraCluster, rack view.RackView,
	dataPVCConfigDiff DataPVCConfigurationChange, stsClient sts.StsClient) actionstep.StepResult {

	if !rack.IsStatefulSetAliveNow() {
		return actionstep.Pass()
	}

	if DoesStatefulSetHaveNewDataPVCConfig(rack, dataPVCConfigDiff) {
		//TODO: consider checking all pvcs - if there is anything to change - instead of just relying on sts being updated at the end
		//TODO: after implementing or declining above, remove code duplication with RemoveStatefulSetOrphan
		return actionstep.Pass()
	}

	rack.Log().Info("Deleting StatefulSet (HARD DELETE, no orphan option)")
	err := stsClient.DeleteStatefulSet(ctx, cc.Namespace, rack.LivingStatefulSet().Name)
	if err != nil {
		return actionstep.Error(err)
	}

	return actionstep.Break()
}

func RemoveStatefulSetOrphan(ctx context.Context, cc *api.CassandraCluster, rack view.RackView,
	dataPVCConfigDiff DataPVCConfigurationChange, stsClient sts.StsClient) actionstep.StepResult {

	if !rack.IsStatefulSetAliveNow() {
		return actionstep.Pass()
	}

	if DoesStatefulSetHaveNewDataPVCConfig(rack, dataPVCConfigDiff) {
		return actionstep.Pass()
	}

	rack.Log().Info("Deleting StatefulSet with orphan option")
	err := stsClient.DeleteStatefulSetWithOrphanOption(ctx, cc.Namespace, rack.LivingStatefulSet().Name)
	if err != nil {
		return actionstep.Error(err)
	}

	return actionstep.Break()
}

func DoesStatefulSetHaveNewDataPVCConfig(rack view.RackView, dataPVCConfigDiff DataPVCConfigurationChange) bool {

	volumeClaimTemplates := rack.LivingStatefulSet().Spec.VolumeClaimTemplates

	if dataPVCConfigDiff.IsCapacityChange() {
		requestedCapacity := dataPVCConfigDiff.GetCapacity()
		_, currentCapacity := FindDataCapacity(volumeClaimTemplates)
		if !requestedCapacity.Equal(currentCapacity) {
			return false
		}
	}

	if dataPVCConfigDiff.IsStorageClassChange() {
		requestedStorageClass := dataPVCConfigDiff.GetStorageClass()
		_, currentStorageClass := FindDataStorageClass(volumeClaimTemplates)
		if currentStorageClass != requestedStorageClass {
			return false
		}
	}

	return true
}

func DoesStatefulSetHaveNewCapacity(cc *api.CassandraCluster, livingStatefulSet *appsv1.StatefulSet) bool {
	requested := SilentParseResourceQuantity(cc.Spec.DataCapacity)
	_, current := FindDataCapacity(livingStatefulSet.Spec.VolumeClaimTemplates)
	return requested.Equal(current)
}

func RecreateStatefulSetWithDataConfig(ctx context.Context, rack view.RackView,
	dataPVCConfigDiff DataPVCConfigurationChange, stsClient sts.StsClient) actionstep.StepResult {

	if rack.IsStatefulSetAliveNow() {
		return actionstep.Pass()
	}

	rack.Log().Info("Creating StatefulSet with new capacity")

	newStatefulSet, err := UnmarshallSnapshottedStatefulSet(rack)
	if err != nil {
		return actionstep.Error(err)
	}

	err = ApplyPVCModification(newStatefulSet, dataPVCConfigDiff)
	if err != nil {
		return actionstep.Error(err)
	}

	err = stsClient.CreateStatefulSet(ctx, newStatefulSet)
	if err != nil {
		return actionstep.Error(err)
	}

	return actionstep.Break()
}

func ApplyPVCModification(newStatefulSet *appsv1.StatefulSet, dataPVCConfigDiff DataPVCConfigurationChange) error {
	err := setNewDataPVCConfig(newStatefulSet, dataPVCConfigDiff)
	if err != nil {
		return err
	}
	return enrichWithCleanLastAppliedAnnotation(newStatefulSet, dataPVCConfigDiff)
}

func setNewDataPVCConfig(statefulSet *appsv1.StatefulSet, dataPVCConfigDiff DataPVCConfigurationChange) error {
	for i, template := range statefulSet.Spec.VolumeClaimTemplates {
		if template.Name == consts.DataPVCName {
			if dataPVCConfigDiff.IsCapacityChange() {
				template.Spec.Resources.Requests["storage"] = dataPVCConfigDiff.GetCapacity()
			}
			if dataPVCConfigDiff.IsStorageClassChange() {
				template.Spec.StorageClassName = ptr.To(dataPVCConfigDiff.GetStorageClass())
			}
			statefulSet.Spec.VolumeClaimTemplates[i] = template
			return nil
		}
	}
	return errors.New(fmt.Sprintf("no %s pvc found in statefulSet %s", consts.DataPVCName, statefulSet.Name))
}

// enrichWithCleanLastAppliedAnnotation
//
// 1. Why not create statefulSet directly from the CR?
//   - because other changes may have been introduced (e.g. scaling or other changes in the pod template)
//     we don't want other actions to interfere with the resize process
//
// 2. Why not create statefulSet from the old CR (last-applied-configuration)?
//   - because other changes may also have been introduced
//     -- someone starts a scale-in
//     -- then changes dataCapacity
//     -- the first rack will still be done correctly,
//     but in the second, at the end of the resize,
//     a StatefulSet with a smaller number of replicas will be applied immediately, without calling decommission
//
// 3. Why do we need to manually handle last-applied annotation on the newStatefulSet?
//   - Banzai stores the original object in annotations and performs a 3-way merge on update
//   - livingStatefulSet is an object fetched from k8s API, so it contains Kubernetes defaults (added by the k8s API server)
//   - newStatefulSet is livingStatefulSet after marshal+unmarshal
//   - if we simply did
//     `patch.DefaultAnnotator.SetLastAppliedAnnotation(newStatefulSet)`
//     we would put into the annotations an object with Kubernetes defaults (added by the k8s API server)
//   - that would force an update during the 3-way merge after the resize
//     (StatefulSet generated from the CR would be clean and would not match the polluted last-applied in the stored StatefulSet)
func enrichWithCleanLastAppliedAnnotation(newStatefulSet *appsv1.StatefulSet, dataPVCConfigDiff DataPVCConfigurationChange) error {

	// best effort = swallow all errors:
	// if we cannot get/edit/encode original sts -> we skip setting last-applied annotation, would lead to extra update after resize (no pod restart)

	originalStatefulSet, err := lastapplied.GetOriginalSts(newStatefulSet)
	if err != nil {
		return nil
	}

	err = setNewDataPVCConfig(&originalStatefulSet, dataPVCConfigDiff)
	if err != nil {
		return nil
	}

	lastApplied, err := lastapplied.EncodeLastAppliedConfigAnnotation(originalStatefulSet)
	if err != nil {
		return nil
	}
	newStatefulSet.Annotations[patch.LastAppliedConfig] = lastApplied

	return nil
}
