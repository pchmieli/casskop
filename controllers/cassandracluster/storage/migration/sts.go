package migration

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	api "github.com/cscetbon/casskop/api/v2"
	"github.com/cscetbon/casskop/controllers/cassandracluster/consts"
	"github.com/cscetbon/casskop/controllers/cassandracluster/pods"
	"github.com/cscetbon/casskop/controllers/cassandracluster/storage/actionstep"
	sc "github.com/cscetbon/casskop/controllers/cassandracluster/storage/change"
	"github.com/cscetbon/casskop/controllers/cassandracluster/storagestateclient"
	"github.com/cscetbon/casskop/controllers/cassandracluster/sts"
	"github.com/cscetbon/casskop/controllers/cassandracluster/view"
	"github.com/cscetbon/casskop/pkg/k8s"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
)

// TODO: needed for whole-rack-at-once algorithm, probably to be deleted eventually

func CreateAndDeleteTempStatefulSetWithNewDataConfig(ctx context.Context, cc *api.CassandraCluster, rack view.RackView,
	dataPVCConfigDiff sc.DataPVCConfigurationChange, stsClient sts.StsClient,
	storageStateClient storagestateclient.StorageStateClient) actionstep.StepResult {

	if rack.RackStatus().CassandraLastAction.Status == api.StatusDone {
		// already done, maybe need another reconcile to stabilize
		return actionstep.Pass()
	}

	desiredStatefulSet, err := getDesiredStatefulSet(rack, dataPVCConfigDiff)
	if err != nil {
		return actionstep.Error(err)
	}

	//TODO: potential change needed if we allow double-udpate (update params after action is started)
	//      what if we have new PVs created, saved in status, but CC changes?
	//		the step should execute
	//		will the step automatically delete tempPvc, PV and create new ones?
	//			no, but let there be a possibility of manual control
	//			it will probably behave incorrectly, because it will create the retain sts again
	//			and it will attach to existing temp pvcs...
	//			maybe prevent this and block further CC changes during migration?

	//TODO: check how the algorithm will behave when migration state is saved for some pods but not for others

	rackStatus := rack.RackStatus()

	// Check if migration state exists and is complete for all pods
	if isMigrationStateComplete(rackStatus, desiredStatefulSet) {
		rack.Log().Info("RackStorageMigrationState is complete and matches desired configuration, skipping temp StatefulSet creation")
		return actionstep.Pass()
	}

	tempStsName := getTempRetainStsName(desiredStatefulSet)

	tempStsExists, err := stsClient.CheckStatefulSetExists(ctx, cc.Namespace, tempStsName)
	if err != nil {
		return actionstep.Error(err)
	}

	//TODO later: maybe even add check it is !0
	expectedReplicas := *desiredStatefulSet.Spec.Replicas

	allReady, err := fetchTempDataPvcsAndCheckIfAllReady(ctx, cc.Namespace, expectedReplicas, storageStateClient, rack, tempStsName)
	//TODO: think twice if it is correct condition...
	// in next action step we  swap PVCs, and we probably Break() multiple times
	// so this step will fire again...
	// and it may return false here, depending on condition and timing
	if err != nil {
		return actionstep.Error(err)
	}

	if allReady {
		if !tempStsExists {

			rack.Log().Infof("All temp retaining PVCs are ready and temp retaining StatefulSet '%s' is deleted, need to store migration state (per pod)", tempStsName)

			// Fill StorageMigrationState with information about all PVCs and PVs
			err := fillStorageMigrationState(ctx, cc.Namespace, desiredStatefulSet, tempStsName,
				storageStateClient, rackStatus, rack)
			if err != nil {
				rack.Log().Errorf("Failed to fill StorageMigrationState: %v", err)
				return actionstep.Error(err)
			}

			return actionstep.Break()
		}

		//TODO later: check deletionTimestamp to not delete twice

		rack.Log().Infof("All temp retaining PVCs are ready, deleting temp retaining StatefulSet '%s'", tempStsName)
		err := stsClient.DeleteStatefulSet(ctx, cc.Namespace, tempStsName)
		if err != nil {
			return actionstep.Error(err)
		}
		return actionstep.Break()
	}

	if tempStsExists {
		rack.Log().Infof("Temp retaining StatefulSet '%s' exists but PVs not ready yet - keep waiting... ", tempStsName)
		return actionstep.Break()
	}

	err = createTempStsWithNewPvcs(ctx, cc, rack, desiredStatefulSet, stsClient)
	if err != nil {
		return actionstep.Error(err)
	}
	return actionstep.Break()
}

func getTempRetainStsName(desiredStatefulSet *appsv1.StatefulSet) string {
	return "temp-retain-" + desiredStatefulSet.Name
}

func getDesiredStatefulSet(rack view.RackView, dataPVCConfigDiff sc.DataPVCConfigurationChange) (*appsv1.StatefulSet, error) {
	desiredStatefulSet, err := sc.UnmarshallSnapshottedStatefulSet(rack)
	if err != nil {
		return nil, err
	}
	err = sc.ApplyPVCModification(desiredStatefulSet, dataPVCConfigDiff)
	if err != nil {
		return nil, err
	}
	return desiredStatefulSet, nil
}

func fetchTempDataPvcsAndCheckIfAllReady(ctx context.Context, namespace string, expectedReplicas int32,
	storageStateClient storagestateclient.StorageStateClient, rack view.RackView, tempStsName string) (bool, error) {

	// List all PVCs with the temp-retain label
	// PVC naming: data-temp-retain-{original-sts-name}-{ordinal}
	//TODO: not enough, there might be pvcs from other dc-rack name, need to add more labels to pvcs, or filter by name
	selector := map[string]string{
		//TODO: add prefix cassandraclusters.db.orange.com or sth like that
		"temp-retain-sts": "true",
		"dc-rack":         rack.DcRackName().String(),
	}

	pvcList, err := storageStateClient.ListPVC(ctx, namespace, selector)
	if err != nil {
		return false, err
	}

	//TODO  check if pvc names match prefix: consts.DataPVCName + "-" +	tempStsName

	// Check if we have the expected number of PVCs
	if len(pvcList.Items) != int(expectedReplicas) {
		rack.Log().Infof("Found %d/%d temp retaining PVCs, waiting for more to be created", len(pvcList.Items), expectedReplicas)
		return false, nil
	}

	// Check if all PVCs are Bound (meaning PVs are provisioned)
	boundCount := 0
	for _, pvc := range pvcList.Items {
		if pvc.Status.Phase == corev1.ClaimBound {
			boundCount++
		} else {
			rack.Log().Infof("PVC %s is in phase %s (not Bound yet)", pvc.Name, pvc.Status.Phase)
		}
	}

	if boundCount != int(expectedReplicas) {
		rack.Log().Infof("Only %d/%d temp retaining PVCs are Bound, waiting for all to be ready", boundCount, expectedReplicas)
		return false, nil
	}

	rack.Log().Infof("All %d temp retaining PVCs are Bound and ready", boundCount)
	return true, nil
}

func createTempStsWithNewPvcs(ctx context.Context, cc *api.CassandraCluster, rack view.RackView,
	desiredStatefulSet *appsv1.StatefulSet, stsClient sts.StsClient) error {

	rack.Log().Info("Creating temp-retaining StatefulSet with new capacity and storage class for dc-rack " + rack.DcRackName()) //TODO: more details

	tempRetainingSts := buildTempRetainingStatefulSet(cc, desiredStatefulSet)

	rack.Log().Infof("Creating temporary retaining StatefulSet '%s' with new storage configuration (capacity: %s, storage class: %s)",
		tempRetainingSts.Name,
		getDataPVCCapacity(tempRetainingSts),
		getDataPVCStorageClass(tempRetainingSts))

	return stsClient.CreateStatefulSet(ctx, tempRetainingSts)
}

// buildTempRetainingStatefulSet creates a temporary StatefulSet with "temp-retain-" prefix
// This StatefulSet will create new PVCs with the new storage class/capacity
func buildTempRetainingStatefulSet(cc *api.CassandraCluster, desiredStatefulSet *appsv1.StatefulSet) *appsv1.StatefulSet {
	tempSts := &appsv1.StatefulSet{}

	tempSts.Name = getTempRetainStsName(desiredStatefulSet)
	tempSts.Namespace = desiredStatefulSet.Namespace
	//TODO later: sth more?
	k8s.AddOwnerRefToObject(tempSts, k8s.AsOwner(cc))

	//TODO later: check if non empty
	appLabel := desiredStatefulSet.Spec.Selector.MatchLabels["app"]
	dcLabel := desiredStatefulSet.Spec.Selector.MatchLabels["dc-rack"]
	tempStsMatchLabels := map[string]string{
		//TODO later: rethink labels
		"app":             appLabel,
		"dc-rack":         dcLabel,
		"temp-retain-sts": "true",
	}
	antiAffinityLabels := map[string]string{
		//TODO later: rethink labels
		"app":             appLabel,
		"temp-retain-sts": "true",
	}

	dataPVCTemplate := findDataPVCTemplate(desiredStatefulSet.Spec.VolumeClaimTemplates)
	if dataPVCTemplate != nil {
		// Add label to identify temp retaining PVCs
		if dataPVCTemplate.Labels == nil {
			dataPVCTemplate.Labels = make(map[string]string)
		}
		//TODO: it must be same method for labelling and for filtering (detecting if ready)
		dataPVCTemplate.Labels["temp-retain-sts"] = "true"
		dataPVCTemplate.Labels["dc-rack"] = dcLabel

		k8s.AddOwnerRefToObject(dataPVCTemplate, k8s.AsOwner(cc))
		tempSts.Spec.VolumeClaimTemplates = []corev1.PersistentVolumeClaim{*dataPVCTemplate}
	}

	tempSts.Spec.Replicas = desiredStatefulSet.Spec.Replicas

	tempSts.Spec.Selector = &metav1.LabelSelector{
		MatchLabels: tempStsMatchLabels,
	}

	var annotations = map[string]string{}
	var tolerations = []corev1.Toleration{}
	//TODO: maybe better take it from sts dump?
	if cc.Spec.Pod != nil {
		annotations = cc.Spec.Pod.Annotations
		tolerations = cc.Spec.Pod.Tolerations
	}

	tempSts.Spec.Template.ObjectMeta = metav1.ObjectMeta{
		Annotations: annotations,
		Labels:      tempStsMatchLabels,
	}

	tempSts.Spec.Template.Spec.Tolerations = tolerations
	tempSts.Spec.Template.Spec.SecurityContext = &corev1.PodSecurityContext{
		//TODO: maybe better take it from sts dump?
		RunAsUser:    ptr.To(cc.Spec.RunAsUser),
		RunAsNonRoot: ptr.To(true),
		FSGroup:      ptr.To(cc.Spec.FSGroup),
	}

	tempSts.Spec.Template.Spec.Affinity = &corev1.Affinity{}
	tempSts.Spec.Template.Spec.Affinity.NodeAffinity = desiredStatefulSet.Spec.Template.Spec.Affinity.NodeAffinity
	tempSts.Spec.Template.Spec.Affinity.PodAntiAffinity = &corev1.PodAntiAffinity{
		RequiredDuringSchedulingIgnoredDuringExecution: []corev1.PodAffinityTerm{
			{
				LabelSelector: &metav1.LabelSelector{
					MatchLabels: antiAffinityLabels,
				},
				TopologyKey: "kubernetes.io/hostname",
			},
		},
	}

	//TODO: will it work smoothly? maybe sth that dont need to be downloaded additionally, e.g. casskop or casskop-sidecar image?
	tempSts.Spec.Template.Spec.Containers = []corev1.Container{
		{
			Name:    "placeholder",
			Image:   "busybox:1.36",
			Command: []string{"sleep", "infinity"},
		},
	}

	return tempSts
}

// findDataPVCTemplate finds the data PVC template from the list of volume claim templates
func findDataPVCTemplate(templates []corev1.PersistentVolumeClaim) *corev1.PersistentVolumeClaim {
	for i, template := range templates {
		if template.Name == consts.DataPVCName {
			return &templates[i]
		}
	}
	return nil
}

// getDataPVCCapacity returns the capacity of the data PVC for logging
func getDataPVCCapacity(sts *appsv1.StatefulSet) string {
	dataPVC := findDataPVCTemplate(sts.Spec.VolumeClaimTemplates)
	if dataPVC != nil && dataPVC.Spec.Resources.Requests != nil {
		if capacity, ok := dataPVC.Spec.Resources.Requests[corev1.ResourceStorage]; ok {
			return capacity.String()
		}
	}
	return "unknown"
}

// getDataPVCStorageClass returns the storage class of the data PVC for logging
func getDataPVCStorageClass(sts *appsv1.StatefulSet) string {
	dataPVC := findDataPVCTemplate(sts.Spec.VolumeClaimTemplates)
	if dataPVC != nil && dataPVC.Spec.StorageClassName != nil {
		return *dataPVC.Spec.StorageClassName
	}
	return "default"
}

// isMigrationStateComplete checks if RackStorageMigrationState exists, is filled for all pods,
// and matches the desired storage configuration
func isMigrationStateComplete(rackStatus *api.CassandraRackStatus, desiredStatefulSet *appsv1.StatefulSet) bool {
	if rackStatus == nil {
		return false
	}

	if rackStatus.StorageMigrationState == nil {
		return false
	}

	if rackStatus.StorageMigrationState.Pods == nil {
		return false
	}

	// Extract desired storage config
	desiredStorageClass, desiredCapacity, err := extractDesiredStorageConfig(desiredStatefulSet)
	if err != nil {
		return false
	}

	expectedReplicas := int32(0)
	if desiredStatefulSet.Spec.Replicas != nil {
		expectedReplicas = *desiredStatefulSet.Spec.Replicas
	}

	// Check if we have migration state for all pods
	if len(rackStatus.StorageMigrationState.Pods) != int(expectedReplicas) {
		return false
	}

	// Check if all pods have the desired storage config
	for _, podState := range rackStatus.StorageMigrationState.Pods {
		// Check if required fields are filled
		if podState.NewPvCapacity == "" || podState.NewPvStorageClass == "" {
			return false
		}

		// Check if storage class matches
		if podState.NewPvStorageClass != desiredStorageClass {
			return false
		}

		// Check if capacity matches
		if podState.NewPvCapacity != desiredCapacity.String() {
			return false
		}

		// Ensure pod has both PV names recorded (old and new)
		if podState.OldPvName == "" || podState.NewPvName == "" {
			return false
		}

		// Check if PVC dumps are present for validation and potential rollback
		if podState.RegularPvcDump == "" || podState.TempPvcDump == "" {
			return false
		}
	}

	return true
}

// fillStorageMigrationState fetches regular and temp PVCs and fills the RackStorageMigrationState
func fillStorageMigrationState(ctx context.Context, namespace string, desiredStatefulSet *appsv1.StatefulSet,
	tempStsName string, storageStateClient storagestateclient.StorageStateClient,
	rackStatus *api.CassandraRackStatus, rack view.RackView) error {

	// Extract desired storage config
	desiredStorageClass, desiredCapacity, err := extractDesiredStorageConfig(desiredStatefulSet)
	if err != nil {
		return err
	}

	expectedReplicas := int32(0)
	if desiredStatefulSet.Spec.Replicas != nil {
		expectedReplicas = *desiredStatefulSet.Spec.Replicas
	}

	// Initialize StorageMigrationState if not exists
	if rackStatus.StorageMigrationState == nil {
		rackStatus.StorageMigrationState = &api.RackStorageMigrationState{
			Pods: make(map[string]api.PodStorageMigrationState),
		}
	}
	if rackStatus.StorageMigrationState.Pods == nil {
		rackStatus.StorageMigrationState.Pods = make(map[string]api.PodStorageMigrationState)
	}

	// Process each pod ordinal
	for ordinal := int32(0); ordinal < expectedReplicas; ordinal++ {
		podName := fmt.Sprintf("%s-%d", desiredStatefulSet.Name, ordinal)
		regularPVCName := fmt.Sprintf("%s-%s-%d", consts.DataPVCName, desiredStatefulSet.Name, ordinal)
		tempPVCName := fmt.Sprintf("%s-%s-%d", consts.DataPVCName, tempStsName, ordinal)

		//TODO later: check if already exists in status? and skip?

		// Get regular PVC
		regularPVC, err := storageStateClient.GetPVC(ctx, namespace, regularPVCName)
		if err != nil {
			rack.Log().Warnf("Failed to get regular PVC %s: %v", regularPVCName, err)
			continue
		}

		// Get temp PVC
		tempPVC, err := storageStateClient.GetPVC(ctx, namespace, tempPVCName)
		if err != nil {
			rack.Log().Warnf("Failed to get temp PVC %s: %v", tempPVCName, err)
			continue
		}

		// Serialize PVCs to JSON for dumps
		regularPVCDump, err := serializePVCToJSON(regularPVC)
		if err != nil {
			rack.Log().Warnf("Failed to serialize regular PVC %s: %v", regularPVCName, err)
			regularPVCDump = "" // Continue with empty dump
		}

		tempPVCDump, err := serializePVCToJSON(tempPVC)
		if err != nil {
			rack.Log().Warnf("Failed to serialize temp PVC %s: %v", tempPVCName, err)
			tempPVCDump = "" // Continue with empty dump
		}

		// Fill migration state for this pod
		podState := api.PodStorageMigrationState{
			RegularPvcDump:    regularPVCDump,
			TempPvcDump:       tempPVCDump,
			OldPvName:         regularPVC.Spec.VolumeName,
			NewPvName:         tempPVC.Spec.VolumeName,
			NewPvCapacity:     desiredCapacity.String(),
			NewPvStorageClass: desiredStorageClass,
		}

		rackStatus.StorageMigrationState.Pods[podName] = podState
		rack.Log().Infof("Filled migration state for pod %s: old PV=%s, new PV=%s, capacity=%s, storage class=%s",
			podName, podState.OldPvName, podState.NewPvName, podState.NewPvCapacity, podState.NewPvStorageClass)
	}

	rack.Log().Infof("StorageMigrationState filled for %d pods", len(rackStatus.StorageMigrationState.Pods))
	return nil
}

// serializePVCToJSON serializes a PVC to JSON string for storing in status
func serializePVCToJSON(pvc *corev1.PersistentVolumeClaim) (string, error) {
	pvcBytes, err := json.Marshal(pvc)
	if err != nil {
		return "", err
	}
	return string(pvcBytes), nil
}

func waitTillStatefulSetAndAllPodsAreReady(ctx context.Context, cc *api.CassandraCluster, rack view.RackView,
	podsClient pods.PodsClient) actionstep.StepResult {

	if !sc.DoesStatefulSetHaveNewCapacity(cc, rack.LivingStatefulSet()) {
		rack.Log().Infof("Resize action is in progress, statefulset need to be re-created with new capacity")
		return actionstep.Break()
	}

	//TODO: check storageClass too

	if sts.IsStatefulSetReady(rack.LivingStatefulSet()) {
		podList, err := podsClient.ListPods(ctx, cc.Namespace, rack.GetLabelsForCassandraDCRack(cc))
		if err != nil {
			return actionstep.Error(err)
		}
		expectedNodesPerRacks := *rack.LivingStatefulSet().Spec.Replicas
		if len(podList.Items) != int(expectedNodesPerRacks) {
			errMsg := fmt.Sprintf("Number of pods (%d) different than expected Replicas (%d) for DC-Rack %s",
				len(podList.Items), expectedNodesPerRacks, rack.DcRackName())
			rack.Log().Warn(errMsg)
			return actionstep.Error(errors.New(errMsg))
		}
		if sc.AllPodsReady(podList) {
			//TODO later: different logging, migration does not finish just after pods are ready
			//rack.Log().Info("Resize action finalization, " +
			//	"all pods are ready with new DataCapacity, we can finalize the action")
			return actionstep.Pass()
		}
	}

	rack.Log().Info("Resize action is in progress, " +
		"we wait for all pods to be ready with new DataCapacity before finalizing the action")
	return actionstep.Break()
}
