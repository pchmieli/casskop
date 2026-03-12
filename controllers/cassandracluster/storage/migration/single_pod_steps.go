package migration

import (
	"context"
	//TODO: maybe json-iterator? recall why it is used somewhere... maybe it was for banzaicloud compatibility only
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	api "github.com/cscetbon/casskop/api/v2"
	"github.com/cscetbon/casskop/controllers/cassandracluster/cassandrapod"
	"github.com/cscetbon/casskop/controllers/cassandracluster/pods"
	as "github.com/cscetbon/casskop/controllers/cassandracluster/storage/actionstep"
	sc "github.com/cscetbon/casskop/controllers/cassandracluster/storage/change"
	"github.com/cscetbon/casskop/controllers/cassandracluster/storagestateclient"
	stsClient "github.com/cscetbon/casskop/controllers/cassandracluster/sts"
	"github.com/cscetbon/casskop/controllers/cassandracluster/view"
	"github.com/cscetbon/casskop/controllers/nodeoperations"
	"github.com/swarvanusg/go_jolokia"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

//TODO: eventually divide into separate files/modules

// getPodIfExists is a helper that handles GetPod calls with proper 404 handling
// Returns (pod, error) where:
// - (pod, nil): Pod found
// - (nil, nil): Pod not found (404)
// - (nil, error): Other error occurred
func getPodIfExists(ctx context.Context, podsClient pods.PodsClient, namespace, podName string) (*corev1.Pod, error) {
	pod, err := podsClient.GetPod(ctx, namespace, podName)
	if err != nil {
		if apierrors.IsNotFound(err) {
			// Pod not found - return nil pod with nil error
			return nil, nil
		}
		// Other error - return it
		return nil, err
	}
	// Pod found
	return pod, nil
}

// initializeMigrationState creates migration state and chooses next pod to migrate
func initializeMigrationState(ctx context.Context, rack view.RackView, cc *api.CassandraCluster, podsClient pods.PodsClient) as.StepResult {
	if rack.RackStatus().StorageMigrationState == nil {
		rack.RackStatus().StorageMigrationState = &api.RackStorageMigrationState{
			Pods: make(map[string]api.PodStorageMigrationState),
		}

		// List all pods in the rack
		namespace := cc.Namespace
		labels := rack.GetLabelsForCassandraDCRack(cc)
		podList, err := podsClient.ListPods(ctx, namespace, labels)
		if err != nil {
			return as.Error(fmt.Errorf("failed to list pods: %w", err))
		}

		// Initialize state for each pod
		for _, pod := range podList.Items {
			rack.RackStatus().StorageMigrationState.Pods[pod.Name] = api.PodStorageMigrationState{
				Migrated: false,
			}
		}

		rack.Log().Infof("Initialized migration state for %d pods", len(podList.Items))
		return as.Break()
	}

	return as.Pass()
}

// getCurrentPodToMigrate returns the name of the next pod to migrate
// Returns empty string if all pods are migrated (idempotent - allows steps to pass through to finalization)
// Pod names are sorted to ensure consistent order across reconciles
func getCurrentPodToMigrate(rack view.RackView) string {
	if rack.RackStatus().StorageMigrationState == nil {
		return ""
	}

	// Get all pod names and sort them
	var podNames []string
	for podName := range rack.RackStatus().StorageMigrationState.Pods {
		podNames = append(podNames, podName)
	}
	sort.Strings(podNames)

	// Return first non-migrated pod (in sorted order)
	for _, podName := range podNames {
		if !rack.RackStatus().StorageMigrationState.Pods[podName].Migrated {
			return podName
		}
	}

	// All pods migrated
	return ""
}

// recordOldPodIPAndHostID captures the pod's IP and host ID before deletion
func recordOldPodIPAndHostID(ctx context.Context, rack view.RackView, cc *api.CassandraCluster, podName string, podsClient pods.PodsClient) as.StepResult {
	// Empty podName means all pods migrated - pass through to finalization
	if podName == "" {
		return as.Pass()
	}

	// Check if already recorded
	podState := rack.RackStatus().StorageMigrationState.Pods[podName]
	if podState.OldPodIP != "" && podState.OldHostID != "" {
		rack.Log().Infof("Old IP and host ID already recorded for pod %s", podName)
		return as.Pass()
	}

	// Get pod directly by name
	targetPod, err := getPodIfExists(ctx, podsClient, cc.Namespace, podName)
	if err != nil {
		return as.Error(fmt.Errorf("failed to get pod %s: %w", podName, err))
	}
	if targetPod == nil {
		return as.Error(fmt.Errorf("pod %s not found - cannot record IP and host ID", podName))
	}

	// Get host ID via Jolokia (JMX) instead of nodetool
	jolokiaURL := fmt.Sprintf("http://%s:8778/jolokia/", targetPod.Status.PodIP)
	jolokiaClient := go_jolokia.NewJolokiaClient(jolokiaURL)
	nodeOpsClient := nodeoperations.NewNodeOp(jolokiaClient, targetPod.Status.PodIP)

	hostID, err := nodeOpsClient.GetLocalHostID()
	if err != nil {
		rack.Log().Warnf("Failed to get host ID from pod %s via JMX: %v", podName, err)
		return as.Error(fmt.Errorf("failed to get host ID via JMX: %w", err))
	}

	if hostID == "" {
		return as.Error(fmt.Errorf("host ID is empty for pod %s", podName))
	}

	// Update state
	podState.OldPodIP = targetPod.Status.PodIP
	podState.OldHostID = hostID
	rack.RackStatus().StorageMigrationState.Pods[podName] = podState

	rack.Log().Infof("Recorded old pod %s: IP=%s, HostID=%s", podName, targetPod.Status.PodIP, hostID)
	return as.Break()
}

// captureDiagnostics fetches token ranges and nodetool status from a pod via Jolokia
// Returns (tokenRanges, nodetoolStatus, error)
func captureDiagnostics(ctx context.Context, rack view.RackView, cc *api.CassandraCluster,
	podName string, podsClient pods.PodsClient) (string, string, error) {

	// Get pod directly by name
	targetPod, err := getPodIfExists(ctx, podsClient, cc.Namespace, podName)
	if err != nil {
		return "", "", fmt.Errorf("failed to get pod %s: %w", podName, err)
	}

	if targetPod == nil {
		return "", "", fmt.Errorf("pod %s not found for diagnostics capture", podName)
	}

	// Create Jolokia client
	jolokiaURL := fmt.Sprintf("http://%s:8778/jolokia/", targetPod.Status.PodIP)
	jolokiaClient := go_jolokia.NewJolokiaClient(jolokiaURL)
	nodeOpsClient := nodeoperations.NewNodeOp(jolokiaClient, targetPod.Status.PodIP)

	// Capture token ranges
	tokenRanges, err := nodeOpsClient.GetTokenRanges()
	if err != nil {
		rack.Log().Warnf("Failed to get token ranges from pod %s: %v (continuing anyway)", podName, err)
		tokenRanges = fmt.Sprintf("ERROR: %v", err)
	}

	// Capture nodetool status
	nodetoolStatus, err := nodeOpsClient.GetNodetoolStatus()
	if err != nil {
		rack.Log().Warnf("Failed to get nodetool status from pod %s: %v (continuing anyway)", podName, err)
		nodetoolStatus = fmt.Sprintf("ERROR: %v", err)
	}

	return tokenRanges, nodetoolStatus, nil
}

// captureDiagnosticsBeforeMigration captures token ranges and nodetool status before pod deletion
// This data is useful for debugging and verifying that token ranges are preserved during migration
func captureDiagnosticsBeforeMigration(ctx context.Context, rack view.RackView, cc *api.CassandraCluster, podName string, podsClient pods.PodsClient) as.StepResult {
	if podName == "" {
		return as.Pass()
	}

	// Check if already captured
	podState := rack.RackStatus().StorageMigrationState.Pods[podName]
	if podState.NodetoolStatusBeforeMigration != "" {
		rack.Log().Infof("Diagnostics already captured for pod %s before migration", podName)
		return as.Pass()
	}

	tokenRanges, nodetoolStatus, err := captureDiagnostics(ctx, rack, cc, podName, podsClient)
	if err != nil {
		return as.Error(fmt.Errorf("failed to capture diagnostics for pod %s: %w", podName, err))
	}

	// Store in migration state
	podState.NodetoolStatusBeforeMigration = nodetoolStatus
	rack.RackStatus().StorageMigrationState.Pods[podName] = podState

	rack.Log().Infof("Captured diagnostics for pod %s before migration (token ranges: %d bytes, status: %d bytes)",
		podName, len(tokenRanges), len(nodetoolStatus))
	return as.Break()
}

// dumpPodTemplate captures the pod template from an actual running pod before deletion
// This provides more accurate configuration than StatefulSet template as it includes:
// - Runtime defaults injected by Kubernetes (service account, DNS, security context)
// - Labels/annotations added by controllers and admission webhooks
// - Actual resource limits (after LimitRanger defaults applied)
func dumpPodTemplate(ctx context.Context, rack view.RackView, cc *api.CassandraCluster,
	currentPodName string, podsClient pods.PodsClient) as.StepResult {

	// Skip if no pod to migrate yet
	if currentPodName == "" {
		return as.Pass()
	}

	// Check if template already dumped for this specific pod
	podState := rack.RackStatus().StorageMigrationState.Pods[currentPodName]
	if podState.PodTemplateDump != "" {
		rack.Log().Infof("Pod template already dumped for pod %s", currentPodName)
		return as.Pass()
	}

	// Get actual running pod object (more accurate than StatefulSet template)
	pod, err := getPodIfExists(ctx, podsClient, cc.Namespace, currentPodName)
	if err != nil {
		return as.Error(fmt.Errorf("failed to get pod %s for template dump: %w", currentPodName, err))
	}
	if pod == nil {
		return as.Error(fmt.Errorf("pod %s not found for template dump", currentPodName))
	}

	// Create a clean copy for use as template
	podCopy := pod.DeepCopy()

	// Clear pod-specific metadata that shouldn't be in template
	podCopy.ObjectMeta = metav1.ObjectMeta{
		Name:        pod.Name, // Keep name for debugging/reference
		Namespace:   pod.Namespace,
		Labels:      make(map[string]string),
		Annotations: make(map[string]string),
	}

	// Copy labels but remove pod-specific ones added by StatefulSet controller
	for k, v := range pod.Labels {
		if k != "controller-revision-hash" &&
			k != "statefulset.kubernetes.io/pod-name" {
			podCopy.Labels[k] = v
		}
	}

	// Copy annotations but remove pod-specific ones
	for k, v := range pod.Annotations {
		// Keep most annotations, they're usually important for pod behavior
		podCopy.Annotations[k] = v
	}

	// Clear runtime-assigned spec fields that shouldn't be in template
	podCopy.Spec.NodeName = "" // Let scheduler reassign
	podCopy.Spec.Hostname = "" // Will be set based on pod name

	// Keep ServiceAccountName - it's needed for accessing secrets/configmaps
	// Keep all volumes, volume mounts, init containers, containers - they're all needed

	// Clear status section (not needed for recreation)
	podCopy.Status = corev1.PodStatus{}

	// Clear metadata fields that are runtime-assigned
	podCopy.UID = ""
	podCopy.ResourceVersion = ""
	podCopy.Generation = 0
	podCopy.CreationTimestamp = metav1.Time{}
	podCopy.DeletionTimestamp = nil
	podCopy.DeletionGracePeriodSeconds = nil
	podCopy.OwnerReferences = nil // We're orphaning pods, no owner references

	// Marshal to JSON for storage
	podJSON, err := json.Marshal(podCopy)
	if err != nil {
		return as.Error(fmt.Errorf("failed to marshal pod template: %w", err))
	}

	// Store in migration state
	podState.PodTemplateDump = string(podJSON)
	rack.RackStatus().StorageMigrationState.Pods[currentPodName] = podState

	rack.Log().Infof("Dumped pod template from actual pod %s (%d bytes)", currentPodName, len(podJSON))
	return as.Break()
}

// deleteStatefulSetWithOrphan deletes the StatefulSet while keeping pods running (orphan option)
func deleteStatefulSetWithOrphan(ctx context.Context, rack view.RackView, cc *api.CassandraCluster, podName string, stsClient stsClient.StsClient) as.StepResult {
	if podName == "" {
		return as.Pass()
	}

	// Check if STS already deleted
	storageMigrationState := rack.RackStatus().StorageMigrationState
	if storageMigrationState.StsDeletedWithOrphan {
		return as.Pass()
	}

	statefulSet := rack.LivingStatefulSet()
	if statefulSet == nil {
		// Already deleted
		storageMigrationState.StsDeletedWithOrphan = true
		return as.Break()
	}

	// Delete StatefulSet with orphan option (keeps pods running)
	err := stsClient.DeleteStatefulSetWithOrphanOption(ctx, cc.Namespace, statefulSet.Name)
	if err != nil {
		return as.Error(fmt.Errorf("failed to delete StatefulSet with orphan option: %w", err))
	}

	storageMigrationState.StsDeletedWithOrphan = true

	rack.Log().Infof("Deleted StatefulSet %s with orphan option (pods will remain)", statefulSet.Name)
	return as.Break()
}

// deletePodByName deletes a specific pod by name
func deletePodByName(ctx context.Context, rack view.RackView, cc *api.CassandraCluster, podName string, podsClient pods.PodsClient) as.StepResult {
	if podName == "" {
		return as.Pass()
	}

	// Check if pod is already deleted
	podState := rack.RackStatus().StorageMigrationState.Pods[podName]
	if podState.PodDeleted {
		return as.Pass()
	}

	// Get pod directly by name
	targetPod, err := getPodIfExists(ctx, podsClient, cc.Namespace, podName)
	if err != nil {
		return as.Error(fmt.Errorf("failed to get pod %s: %w", podName, err))
	}
	if targetPod == nil {
		// Pod already deleted or not found
		podState.PodDeleted = true
		rack.RackStatus().StorageMigrationState.Pods[podName] = podState
		rack.Log().Infof("Pod %s not found (already deleted)", podName)
		return as.Pass()
	}

	// Delete the specific pod
	err = podsClient.DeletePod(ctx, targetPod)
	if err != nil {
		return as.Error(fmt.Errorf("failed to delete pod %s: %w", podName, err))
	}

	rack.Log().Infof("Deleted pod %s", podName)
	return as.Break()
}

// waitForStatefulSetDeletion waits for the StatefulSet to be fully deleted
// If all pods are already migrated, the StatefulSet may have been recreated - just pass in that case
func waitForStatefulSetDeletion(ctx context.Context, rack view.RackView, cc *api.CassandraCluster, stsClient stsClient.StsClient) as.StepResult {
	statefulSet := rack.LivingStatefulSet()
	if statefulSet != nil {
		// Check if all pods are already migrated - if so, StatefulSet was recreated
		if rack.RackStatus().StorageMigrationState != nil && rack.RackStatus().StorageMigrationState.AllPodsMigrated() {
			rack.Log().Info("StatefulSet exists but all pods already migrated - StatefulSet was recreated, continuing")
			return as.Pass()
		}

		rack.Log().Info("Waiting for StatefulSet to be deleted")
		return as.Break()
	}

	rack.Log().Info("StatefulSet successfully deleted")
	return as.Pass()
}

// recreateIndividualPodWithExtraReplaceNodeParams creates a single pod (not entire StatefulSet) with migration init container
// The migration init container injects replace_address_first_boot configuration
func recreateIndividualPodWithExtraReplaceNodeParams(ctx context.Context, rack view.RackView, cc *api.CassandraCluster, podName string, podsClient pods.PodsClient) as.StepResult {
	if podName == "" {
		return as.Pass()
	}
	//TODO later: instead of echking it everywhere, check it once outside and split steps slice into 3 groups (before, pod-specific, after)

	// Check if pod already recreated
	podState := rack.RackStatus().StorageMigrationState.Pods[podName]
	if podState.PodRecreated {
		return as.Pass()
	}

	// Get pod template from dumped state
	podTemplateDump := podState.PodTemplateDump
	if podTemplateDump == "" {
		return as.Error(errors.New("pod template dump is empty, cannot recreate pod"))
	}

	// Unmarshal pod object (we dumped the entire pod, not just PodTemplateSpec)
	var podTemplate corev1.Pod
	if err := json.Unmarshal([]byte(podTemplateDump), &podTemplate); err != nil {
		return as.Error(fmt.Errorf("failed to unmarshal pod template: %w", err))
	}

	// Create new pod from template
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:        podName,
			Namespace:   cc.Namespace,
			Labels:      podTemplate.Labels,
			Annotations: podTemplate.Annotations,
		},
		Spec: *podTemplate.Spec.DeepCopy(),
	}

	// Add migration init container at the end of init containers list
	// This container will inject replace_address_first_boot into cassandra.yaml
	migrationInitContainer := createMigrationInitContainer(cc, podState.OldPodIP)
	pod.Spec.InitContainers = append(pod.Spec.InitContainers, migrationInitContainer)

	// Add annotation to mark this pod as being migrated
	if pod.Annotations == nil {
		pod.Annotations = make(map[string]string)
	}
	pod.Annotations["cassandraclusters.db.orange.com/storage-migration"] = "true"
	pod.Annotations["cassandraclusters.db.orange.com/replace-address"] = podState.OldPodIP

	// Create the pod
	if err := podsClient.CreatePod(ctx, pod); err != nil {
		return as.Error(fmt.Errorf("failed to create pod %s: %w", podName, err))
	}

	podState.PodRecreated = true
	rack.RackStatus().StorageMigrationState.Pods[podName] = podState

	rack.Log().Infof("Created pod %s with migration init container (replace_address_first_boot=%s)", podName, podState.OldPodIP)
	return as.Break()
}

// createMigrationInitContainer creates an init container that injects replace_address_first_boot
// as a JVM option into cassandra-env.sh before Cassandra starts
func createMigrationInitContainer(cc *api.CassandraCluster, replaceAddress string) corev1.Container {
	// Reuse the bootstrap image as it has bash and access to the config volume
	return corev1.Container{
		Name:            "storage-migration-config",
		Image:           cc.Spec.BootstrapImage,
		ImagePullPolicy: cc.Spec.ImagePullPolicy,
		Command:         []string{"/bin/bash"},
		Args: []string{
			"-c",
			fmt.Sprintf(`
set -e
echo "Storage migration: Injecting replace_address_first_boot=%s as JVM option"
CASSANDRA_ENV="/etc/cassandra/cassandra-env.sh"
if [ -f "$CASSANDRA_ENV" ]; then
  # Add replace_address_first_boot as JVM option (not YAML property)
  echo "" >> "$CASSANDRA_ENV"
  echo "# Storage migration: replace node with preserved tokens" >> "$CASSANDRA_ENV"
  echo 'JVM_OPTS="$JVM_OPTS -Dcassandra.replace_address_first_boot=%s"' >> "$CASSANDRA_ENV"
  echo "Successfully injected replace_address_first_boot JVM option into cassandra-env.sh"
else
  echo "ERROR: cassandra-env.sh not found at $CASSANDRA_ENV"
  exit 1
fi
`, replaceAddress, replaceAddress),
		},
		VolumeMounts: []corev1.VolumeMount{
			{
				Name:      "bootstrap",
				MountPath: "/etc/cassandra",
			},
		},
	}
}

// waitForPodDeletion waits for the pod to be deleted
// Skips waiting if pod has already been recreated (PodRecreated = true)
func waitForPodDeletion(ctx context.Context, rack view.RackView, cc *api.CassandraCluster, podName string, podsClient pods.PodsClient) as.StepResult {
	if podName == "" {
		return as.Pass()
	}

	// Check if pod already marked as deleted or recreated
	podState := rack.RackStatus().StorageMigrationState.Pods[podName]
	if podState.PodDeleted {
		return as.Pass()
	}

	// If pod has already been recreated, skip waiting for deletion
	// (the new pod has the same name but is a different instance)
	if podState.PodRecreated {
		rack.Log().Infof("Pod %s has been recreated, skipping deletion wait", podName)
		podState.PodDeleted = true
		rack.RackStatus().StorageMigrationState.Pods[podName] = podState
		return as.Pass()
	}

	// Check if pod still exists
	targetPod, err := getPodIfExists(ctx, podsClient, cc.Namespace, podName)
	if err != nil {
		return as.Error(fmt.Errorf("failed to check pod %s existence: %w", podName, err))
	}
	if targetPod == nil {
		// Pod deleted (not found)
		podState.PodDeleted = true
		rack.RackStatus().StorageMigrationState.Pods[podName] = podState
		rack.Log().Infof("Pod %s successfully deleted", podName)
		return as.Pass()
	}

	// Pod still exists, wait
	rack.Log().Infof("Waiting for pod %s to be deleted", podName)
	return as.Break()
}

// ensurePVCHasRetainPolicy ensures the PVC has a PV with Retain reclaim policy before deletion
// This is critical to prevent data loss - we need to ensure the PV will not be deleted when we delete the PVC
func ensurePVCHasRetainPolicy(ctx context.Context, rack view.RackView, cc *api.CassandraCluster, podName string, storageStateClient storagestateclient.StorageStateClient) as.StepResult {
	if podName == "" {
		return as.Pass()
	}

	// Get current pod state
	podState := rack.RackStatus().StorageMigrationState.Pods[podName]

	// If we already have the oldPvName recorded, check that PV's retain policy
	if podState.OldPvName != "" {
		pv, err := storageStateClient.GetPV(ctx, podState.OldPvName)
		if err != nil {
			return as.Error(fmt.Errorf("failed to get PV %s: %w", podState.OldPvName, err))
		}

		// Verify PV has Retain reclaim policy
		if pv.Spec.PersistentVolumeReclaimPolicy != corev1.PersistentVolumeReclaimRetain {
			return as.Error(fmt.Errorf("PV %s has reclaim policy %s, expected Retain. "+
				"Please update the PV reclaim policy to Retain before proceeding with storage migration to prevent data loss",
				pv.Name, pv.Spec.PersistentVolumeReclaimPolicy))
		}

		rack.Log().Infof("Verified PV %s has Retain reclaim policy", pv.Name)
		return as.Pass()
	}

	// If oldPvName not set yet, get it from the PVC
	pvcName := fmt.Sprintf("data-%s", podName)

	pvc, err := storageStateClient.GetPVC(ctx, cc.Namespace, pvcName)
	//TODO: need to handle 404 + check all other k8s "gets" in new code; update, here do not need to handle, but check other places
	if err != nil {
		// Return error - we need to verify the PV before proceeding
		return as.Error(fmt.Errorf("failed to get PVC %s for retain policy verification: %w", pvcName, err))
	}

	// Check if PVC is bound to a PV
	if pvc.Spec.VolumeName == "" {
		return as.Error(fmt.Errorf("PVC %s is not bound to any PV", pvcName))
	}

	// Get the PV
	pv, err := storageStateClient.GetPV(ctx, pvc.Spec.VolumeName)
	if err != nil {
		return as.Error(fmt.Errorf("failed to get PV %s: %w", pvc.Spec.VolumeName, err))
	}

	// Verify PV has Retain reclaim policy
	if pv.Spec.PersistentVolumeReclaimPolicy != corev1.PersistentVolumeReclaimRetain {
		return as.Error(fmt.Errorf("PV %s has reclaim policy %s, expected Retain. "+
			"Please update the PV reclaim policy to Retain before proceeding with storage migration to prevent data loss",
			pv.Name, pv.Spec.PersistentVolumeReclaimPolicy))
	}

	// Record the PV name in status for future checks
	podState.OldPvName = pv.Name
	rack.RackStatus().StorageMigrationState.Pods[podName] = podState

	rack.Log().Infof("Verified PV %s has Retain reclaim policy and recorded in status", pv.Name)
	return as.Break()
}

// deletePodPVC deletes the PVC for the target pod
// If PVC already has desired storage class and capacity, skips deletion and marks as deleted
func deletePodPVC(ctx context.Context, rack view.RackView, cc *api.CassandraCluster, podName string,
	dataConfigChange *sc.DataPVCConfigurationChange, storageStateClient storagestateclient.StorageStateClient) as.StepResult {

	if podName == "" {
		return as.Pass()
	}

	// Check if PVC already deleted
	podState := rack.RackStatus().StorageMigrationState.Pods[podName]
	if podState.OldPVCDeleted {
		return as.Pass()
	}

	// Construct PVC name (assuming standard naming: data-<pod-name>)
	pvcName := fmt.Sprintf("data-%s", podName)

	pvc, err := storageStateClient.GetPVC(ctx, cc.Namespace, pvcName)
	if err != nil {
		// PVC might already be deleted
		rack.Log().Infof("PVC %s not found (marking it as deleted): %v", pvcName, err)
		podState.OldPVCDeleted = true
		rack.RackStatus().StorageMigrationState.Pods[podName] = podState
		return as.Break()
	}

	// Get desired storage class and capacity
	desiredStorageClass := (*dataConfigChange).GetStorageClass()
	desiredCapacity := (*dataConfigChange).GetCapacity()

	// Check if PVC already has desired storage class and capacity
	currentStorageClass := ""
	if pvc.Spec.StorageClassName != nil {
		currentStorageClass = *pvc.Spec.StorageClassName
	}
	currentCapacity := pvc.Spec.Resources.Requests[corev1.ResourceStorage]

	if currentStorageClass == desiredStorageClass && currentCapacity.Cmp(desiredCapacity) == 0 {
		// PVC already has desired configuration - this is strange but possible
		// (e.g. migration was interrupted and PVC was already recreated)
		rack.Log().Warnf("PVC %s already has desired storage class (%s) and capacity (%s) - skipping deletion. "+
			"This might indicate a previous migration attempt. Marking as deleted and continuing.",
			pvcName, desiredStorageClass, desiredCapacity.String())

		// Record old PV name (if not already recorded)
		if podState.OldPvName == "" {
			podState.OldPvName = pvc.Spec.VolumeName
		}

		// Mark as deleted to skip recreation
		podState.OldPVCDeleted = true
		rack.RackStatus().StorageMigrationState.Pods[podName] = podState
		return as.Break()
	}

	// Record old PV name before deletion
	podState.OldPvName = pvc.Spec.VolumeName
	rack.RackStatus().StorageMigrationState.Pods[podName] = podState

	// Delete PVC
	err = storageStateClient.DeletePVC(ctx, pvc)
	if err != nil {
		return as.Error(fmt.Errorf("failed to delete PVC %s: %w", pvcName, err))
	}

	rack.Log().Infof("Deleted PVC %s (bound to PV %s)", pvcName, pvc.Spec.VolumeName)
	return as.Break()
}

// recreatePVCWithNewConfig creates a new PVC with new storage class and capacity
func recreatePVCWithNewConfig(ctx context.Context, rack view.RackView, cc *api.CassandraCluster, podName string,
	dataConfigChange *sc.DataPVCConfigurationChange, storageStateClient storagestateclient.StorageStateClient) as.StepResult {

	if podName == "" {
		return as.Pass()
	}

	// Check if PVC already created
	podState := rack.RackStatus().StorageMigrationState.Pods[podName]
	if podState.NewPVCCreated {
		return as.Pass()
	}

	pvcName := fmt.Sprintf("data-%s", podName)

	// Get new storage class and capacity from the interface
	storageClass := (*dataConfigChange).GetStorageClass()
	capacity := (*dataConfigChange).GetCapacity()

	// Check if PVC already exists
	existingPVC, err := storageStateClient.GetPVC(ctx, cc.Namespace, pvcName)
	if err == nil && existingPVC != nil {
		// PVC already exists, just update status
		rack.Log().Infof("PVC %s already exists, marking as created", pvcName)
		podState.NewPVCCreated = true
		podState.NewPvStorageClass = storageClass
		podState.NewPvCapacity = capacity.String()
		rack.RackStatus().StorageMigrationState.Pods[podName] = podState
		return as.Break()
	}

	// Create new PVC
	labels := rack.GetLabelsForCassandraDCRack(cc)
	newPVC := &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:      pvcName,
			Namespace: cc.Namespace,
			Labels:    labels,
		},
		Spec: corev1.PersistentVolumeClaimSpec{
			AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
			Resources: corev1.VolumeResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceStorage: capacity,
				},
			},
			StorageClassName: &storageClass,
		},
	}

	err = storageStateClient.CreatePVC(ctx, newPVC)
	if err != nil {
		return as.Error(fmt.Errorf("failed to create new PVC %s: %w", pvcName, err))
	}

	podState.NewPVCCreated = true
	podState.NewPvStorageClass = storageClass
	podState.NewPvCapacity = capacity.String()
	rack.RackStatus().StorageMigrationState.Pods[podName] = podState

	rack.Log().Infof("Created new PVC %s with storage class %s and capacity %s",
		pvcName, storageClass, capacity.String())
	return as.Break()
}

func findCassandraContainer(sts *appsv1.StatefulSet) *corev1.Container {
	for i := range sts.Spec.Template.Spec.Containers {
		if sts.Spec.Template.Spec.Containers[i].Name == "cassandra" {
			return &sts.Spec.Template.Spec.Containers[i]
		}
	}
	return nil
}

// waitForPodReadyAndUN waits for the pod to become Ready and UN (Up Normal) in Cassandra
// Also verifies that the old node (DN) has disappeared from the status
func waitForPodReadyAndUN(ctx context.Context, rack view.RackView, cc *api.CassandraCluster, podName string, podsClient pods.PodsClient) as.StepResult {
	if podName == "" {
		return as.Pass()
	}

	// Get pod state to check old host ID
	podState := rack.RackStatus().StorageMigrationState.Pods[podName]

	// Get pod directly by name
	targetPod, err := getPodIfExists(ctx, podsClient, cc.Namespace, podName)
	if err != nil {
		return as.Error(fmt.Errorf("failed to get pod %s: %w", podName, err))
	}
	if targetPod == nil {
		rack.Log().Infof("Waiting for pod %s to be created", podName)
		return as.Break()
	}

	// Check if pod is Ready
	if !cassandrapod.IsReady(targetPod) {
		rack.Log().Infof("Waiting for pod %s to become Ready", podName)
		return as.Break()
	}

	// Check if node is UN (Up Normal) via Jolokia instead of kubectl exec
	jolokiaURL := fmt.Sprintf("http://%s:8778/jolokia/", targetPod.Status.PodIP)
	jolokiaClient := go_jolokia.NewJolokiaClient(jolokiaURL)
	nodeOpsClient := nodeoperations.NewNodeOp(jolokiaClient, targetPod.Status.PodIP)

	// Check if node's operation mode is NORMAL
	status, err := nodeOpsClient.GetNodetoolStatus()
	if err != nil {
		rack.Log().Warnf("Failed to get node status from pod %s via JMX: %v", podName, err)
		return as.Break()
	}

	// Get the new node's Host ID (this is critical - we MUST check by Host ID, not IP!)
	// If pod gets the same IP as before, IP-based checks will be ambiguous
	newHostID, err := nodeOpsClient.GetLocalHostID()
	if err != nil {
		rack.Log().Warnf("Failed to get new host ID from pod %s via JMX: %v", podName, err)
		return as.Break()
	}

	// Store new Host ID if not already stored
	if podState.NewHostID == "" || podState.NewHostID != newHostID {
		podState.NewHostID = newHostID
		podState.NewPodIP = targetPod.Status.PodIP
		rack.RackStatus().StorageMigrationState.Pods[podName] = podState
		rack.Log().Infof("Recorded new Host ID %s for pod %s (IP: %s)", newHostID, podName, targetPod.Status.PodIP)
	}

	// Check nodetool status by Host ID (not IP!) to handle same-IP scenarios correctly
	// Format: "UN  10.244.1.15  67.2 GiB  256  rack1  a3b5c7d9-e1f2-4a5b-8c9d-0e1f2a3b4c5d"
	isNewNodeUN := false
	isOldNodeGone := true // Assume old node is gone unless we find it

	lines := strings.Split(status, "\n")
	for _, line := range lines {
		trimmedLine := strings.TrimSpace(line)

		// Check if this is the new node's line (by NEW Host ID)
		if strings.Contains(line, newHostID) {
			if strings.HasPrefix(trimmedLine, "UN") {
				isNewNodeUN = true
				rack.Log().Infof("New node %s (Host ID %s) found as UN in status", podName, newHostID)
			} else {
				// New node found but not UN yet (could be UJ - Up Joining, etc.)
				rack.Log().Infof("New node %s (Host ID %s) found in status but not UN yet: %s", podName, newHostID, trimmedLine)
				rack.Log().Infof("Current nodetool status:\n%s", status)
				return as.Break()
			}
		}

		// Check if old node (old host ID) is still present
		if podState.OldHostID != "" && strings.Contains(line, podState.OldHostID) {
			// Old node still in status - it should eventually disappear after replacement
			rack.Log().Infof("Old node (Host ID %s) still present in status: %s", podState.OldHostID, trimmedLine)
			isOldNodeGone = false
		}
	}

	if !isNewNodeUN {
		rack.Log().Infof("Waiting for new node %s (Host ID %s) to appear as UN in nodetool status", podName, newHostID)
		rack.Log().Infof("Current nodetool status:\n%s", status)
		return as.Break()
	}

	if !isOldNodeGone {
		rack.Log().Infof("New node %s (Host ID %s) is UN but old node (Host ID %s) still present - waiting for old node to disappear from gossip",
			podName, newHostID, podState.OldHostID)
		rack.Log().Infof("Current nodetool status:\n%s", status)
		return as.Break()
	}

	rack.Log().Infof("Pod %s is Ready and UN with new Host ID %s (verified via Jolokia)", podName, newHostID)
	rack.Log().Infof("Nodetool status:\n%s", status)
	return as.Pass()
}

// captureDiagnosticsAfterMigration captures token ranges and nodetool status after pod is ready
// This allows comparison with pre-migration state to verify token ranges are preserved
func captureDiagnosticsAfterMigration(ctx context.Context, rack view.RackView, cc *api.CassandraCluster, podName string, podsClient pods.PodsClient) as.StepResult {
	if podName == "" {
		return as.Pass()
	}

	// Check if already captured
	podState := rack.RackStatus().StorageMigrationState.Pods[podName]
	if podState.NodetoolStatusAfterMigration != "" {
		rack.Log().Infof("Diagnostics already captured for pod %s after migration", podName)
		return as.Pass()
	}

	tokenRanges, nodetoolStatus, err := captureDiagnostics(ctx, rack, cc, podName, podsClient)
	if err != nil {
		return as.Error(fmt.Errorf("failed to capture diagnostics for pod %s: %w", podName, err))
	}

	// Store in migration state
	podState.NodetoolStatusAfterMigration = nodetoolStatus
	rack.RackStatus().StorageMigrationState.Pods[podName] = podState

	rack.Log().Infof("Captured diagnostics for pod %s after migration (token ranges: %d bytes, status: %d bytes)",
		podName, len(tokenRanges), len(nodetoolStatus))
	return as.Break()
}

// waitUntilLoadDifferenceIsWithinConfiguredThresholds compares nodetool status before and after migration
// Checks that data load differences are within acceptable thresholds (configurable via CR)
// Uses fresh nodetool status for "after" to ensure latest data is captured
// Also checks all other nodes with lower threshold to detect cluster-wide imbalances
func waitUntilLoadDifferenceIsWithinConfiguredThresholds(ctx context.Context, rack view.RackView, cc *api.CassandraCluster,
	podName string, podsClient pods.PodsClient, podLoadThresholdPercent float64, otherNodesThresholdPercent float64) as.StepResult {

	if podName == "" {
		return as.Pass()
	}
	//TODO: do not check again if already marked as migrated
	podState := rack.RackStatus().StorageMigrationState.Pods[podName]

	// Get "before" status from stored state - REQUIRED for comparison
	statusBefore := podState.NodetoolStatusBeforeMigration
	if statusBefore == "" {
		return as.Error(fmt.Errorf("no 'before migration' status found for pod %s - cannot verify load balance", podName))
	}

	// Get FRESH "after" status (not from stored state - data might still be transferring)
	targetPod, err := getPodIfExists(ctx, podsClient, cc.Namespace, podName)
	if err != nil {
		return as.Error(fmt.Errorf("failed to get pod %s for status comparison: %w", podName, err))
	}
	if targetPod == nil {
		return as.Error(fmt.Errorf("pod %s not found for status comparison", podName))
	}

	// Get fresh nodetool status
	jolokiaURL := fmt.Sprintf("http://%s:8778/jolokia/", targetPod.Status.PodIP)
	jolokiaClient := go_jolokia.NewJolokiaClient(jolokiaURL)
	nodeOpsClient := nodeoperations.NewNodeOp(jolokiaClient, targetPod.Status.PodIP)

	statusAfter, err := nodeOpsClient.GetNodetoolStatus()
	if err != nil {
		return as.Error(fmt.Errorf("failed to get fresh nodetool status from pod %s: %w", podName, err))
	}

	// Get or store new pod IP and host ID (only once)
	var newHostID string
	if podState.NewPodIP != "" && podState.NewHostID != "" {
		// Already stored, use existing values
		newHostID = podState.NewHostID
		rack.Log().Infof("Using stored new host ID %s for pod %s", newHostID, podName)
	} else {
		// Not yet stored, get and store them
		newPodIP := targetPod.Status.PodIP
		newHostID, err = nodeOpsClient.GetLocalHostID()
		if err != nil {
			return as.Error(fmt.Errorf("failed to get new host ID for pod %s: %w", podName, err))
		}

		podState.NewPodIP = newPodIP
		podState.NewHostID = newHostID
		rack.RackStatus().StorageMigrationState.Pods[podName] = podState
		rack.Log().Infof("Stored new pod IP %s and host ID %s for pod %s", newPodIP, newHostID, podName)
	}

	// Parse old node load from "before" status using oldHostID
	oldLoad, err := parseLoadFromStatus(statusBefore, podState.OldHostID)
	if err != nil {
		return as.Error(fmt.Errorf("failed to parse old load for host ID %s: %w", podState.OldHostID, err))
	}

	// Parse new node load from "after" status using newHostID
	newLoad, err := parseLoadFromStatus(statusAfter, newHostID)
	if err != nil {
		return as.Error(fmt.Errorf("failed to parse new load for host ID %s: %w", newHostID, err))
	}

	// Calculate load difference percentage for migrated pod
	var diffPercent float64
	if oldLoad > 0 {
		diffPercent = (float64(newLoad) - float64(oldLoad)) / float64(oldLoad) * 100
	} else if newLoad > 0 {
		diffPercent = 100.0 // old was 0, new is not - 100% increase
	}

	rack.Log().Infof("Load comparison for pod %s: old=%d bytes, new=%d bytes, diff=%.2f%%",
		podName, oldLoad, newLoad, diffPercent)

	// Track if we found any issues
	hasIssues := false

	// Check if difference exceeds threshold for migrated pod
	absDiff := diffPercent
	if absDiff < 0 {
		absDiff = -absDiff
	}

	if absDiff > podLoadThresholdPercent {
		rack.Log().Warnf("Load difference (%.2f%%) exceeds threshold (%.2f%%) for pod %s",
			diffPercent, podLoadThresholdPercent, podName)
		hasIssues = true
	} else {
		rack.Log().Infof("Load difference (%.2f%%) within acceptable threshold (%.2f%%) for pod %s",
			diffPercent, podLoadThresholdPercent, podName)
	}

	// Check all other nodes to detect cluster-wide imbalances
	// Parse all node loads from before status
	beforeLoads, err := parseAllLoadsFromStatus(statusBefore)
	if err != nil {
		rack.Log().Warnf("Failed to parse all loads from before status: %v (skipping other nodes check)", err)
		// Don't return error, just skip this check
	} else {
		// Parse all node loads from after status
		afterLoads, err := parseAllLoadsFromStatus(statusAfter)
		if err != nil {
			rack.Log().Warnf("Failed to parse all loads from after status: %v (skipping other nodes check)", err)
		} else {
			// Check each node (excluding the migrated one)
			for hostID, beforeLoad := range beforeLoads {
				if hostID == podState.OldHostID || hostID == newHostID {
					// Skip the migrated node (already checked above)
					continue
				}

				afterLoad, found := afterLoads[hostID]
				if !found {
					rack.Log().Warnf("Host ID %s found in before status but not in after status", hostID)
					continue
				}

				// Calculate load difference for this node
				var otherDiffPercent float64
				if beforeLoad > 0 {
					otherDiffPercent = (float64(afterLoad) - float64(beforeLoad)) / float64(beforeLoad) * 100
				} else if afterLoad > 0 {
					otherDiffPercent = 100.0
				}

				absOtherDiff := otherDiffPercent
				if absOtherDiff < 0 {
					absOtherDiff = -absOtherDiff
				}

				if absOtherDiff > otherNodesThresholdPercent {
					rack.Log().Warnf("Other node %s load difference (%.2f%%) exceeds threshold (%.2f%%)",
						hostID, otherDiffPercent, otherNodesThresholdPercent)
					hasIssues = true
					// Don't break - continue checking all nodes to log all problems
				} else {
					rack.Log().Infof("Other node %s load difference (%.2f%%) within threshold (%.2f%%)",
						hostID, otherDiffPercent, otherNodesThresholdPercent)
				}
			}
		}
	}

	// Return result based on whether we found any issues
	if hasIssues {
		rack.Log().Warnf("Found load imbalances - waiting for cluster to stabilize")
		return as.Break()
	}

	rack.Log().Infof("All nodes load differences within acceptable thresholds")
	return as.Pass()
}

// parseLoadFromStatus extracts load (in bytes) for a specific host ID from nodetool status output
func parseLoadFromStatus(status string, hostID string) (int64, error) {
	// Parse status output line by line
	// Format: "UN  10.244.1.15  1.5 GiB  256  rack1  a3b5c7d9-..."
	lines := strings.Split(status, "\n")
	for _, line := range lines {
		if !strings.Contains(line, hostID) {
			continue
		}

		// Parse load field (3rd field after status and IP)
		fields := strings.Fields(line)
		if len(fields) < 6 {
			continue
		}

		loadStr := fields[2] // e.g. "1.5"
		unitStr := fields[3] // e.g. "GiB"

		// Combine load and unit, parse to bytes
		loadBytes, err := parseLoadToBytes(loadStr + " " + unitStr)
		if err != nil {
			return 0, fmt.Errorf("failed to parse load '%s %s': %w", loadStr, unitStr, err)
		}

		return loadBytes, nil
	}

	return 0, fmt.Errorf("host ID %s not found in status output", hostID)
}

// parseLoadToBytes converts load string like "1.5 GiB" or "150 MiB" to bytes
func parseLoadToBytes(loadStr string) (int64, error) {
	// Handle "Unknown" or empty
	if loadStr == "" || loadStr == "Unknown" || loadStr == "0 B" {
		return 0, nil
	}

	// Parse value and unit
	parts := strings.Fields(loadStr)
	if len(parts) != 2 {
		return 0, fmt.Errorf("invalid load format: %s", loadStr)
	}

	value := 0.0
	_, err := fmt.Sscanf(parts[0], "%f", &value)
	if err != nil {
		return 0, fmt.Errorf("invalid load value: %s", parts[0])
	}

	unit := parts[1]
	multiplier := int64(1)

	switch unit {
	case "B", "bytes":
		multiplier = 1
	case "KiB", "KB":
		multiplier = 1024
	case "MiB", "MB":
		multiplier = 1024 * 1024
	case "GiB", "GB":
		multiplier = 1024 * 1024 * 1024
	case "TiB", "TB":
		multiplier = 1024 * 1024 * 1024 * 1024
	default:
		return 0, fmt.Errorf("unknown unit: %s", unit)
	}

	return int64(value * float64(multiplier)), nil
}

// parseAllLoadsFromStatus parses all node loads from nodetool status output
// Returns map of hostID -> load in bytes
func parseAllLoadsFromStatus(status string) (map[string]int64, error) {
	loads := make(map[string]int64)

	// Parse status output line by line
	// Format: "UN  10.244.1.15  67.2 GiB  256  rack1  a3b5c7d9-e1f2-4a5b-8c9d-0e1f2a3b4c5d"
	lines := strings.Split(status, "\n")
	for _, line := range lines {
		// Skip header lines and empty lines
		if strings.HasPrefix(line, "Status=") || strings.HasPrefix(line, "--") ||
			strings.HasPrefix(line, "Datacenter:") || strings.TrimSpace(line) == "" {
			continue
		}

		// Parse line fields
		fields := strings.Fields(line)
		if len(fields) < 6 {
			// Not enough fields, skip
			continue
		}

		// Extract host ID (last field)
		hostID := fields[len(fields)-1]

		// Extract load (fields 2 and 3: "67.2" and "GiB")
		loadStr := fields[2]
		unitStr := fields[3]

		// Parse to bytes
		loadBytes, err := parseLoadToBytes(loadStr + " " + unitStr)
		if err != nil {
			// Skip this line if parse fails
			continue
		}

		loads[hostID] = loadBytes
	}

	if len(loads) == 0 {
		return nil, fmt.Errorf("no valid load entries found in status output")
	}

	return loads, nil
}

// markPodAsMigrated marks the current pod as successfully migrated
func markPodAsMigrated(rack view.RackView, podName string) as.StepResult {
	if podName == "" {
		return as.Pass()
	}

	podState := rack.RackStatus().StorageMigrationState.Pods[podName]
	if podState.Migrated {
		return as.Pass()
	}

	podState.Migrated = true
	rack.RackStatus().StorageMigrationState.Pods[podName] = podState

	rack.Log().Infof("Pod %s successfully migrated", podName)
	return as.Break()
}

// recreateStatefulSetAfterAllMigrated recreates the StatefulSet after all pods are migrated
// This restores normal StatefulSet management of the pods with new storage configuration
func recreateStatefulSetAfterAllMigrated(ctx context.Context, rack view.RackView, stsClient stsClient.StsClient,
	newStorageClass string, newDataCapacity resource.Quantity) as.StepResult {

	// Check if all pods are migrated first
	if rack.RackStatus().StorageMigrationState == nil || !rack.RackStatus().StorageMigrationState.AllPodsMigrated() {
		// Not all pods migrated yet, skip
		return as.Pass()
	}

	// Check if StatefulSet already exists
	if rack.LivingStatefulSet() != nil {
		rack.Log().Info("StatefulSet already exists, skipping recreation")
		return as.Pass()
	}

	// Get the StatefulSet from snapshot
	statefulSet, err := sc.UnmarshallSnapshottedStatefulSet(rack)
	if err != nil {
		return as.Error(fmt.Errorf("failed to unmarshal snapshotted StatefulSet: %w", err))
	}

	// Update StatefulSet VolumeClaimTemplates with new storage class and capacity
	// Find the "data" PVC template
	for i := range statefulSet.Spec.VolumeClaimTemplates {
		if statefulSet.Spec.VolumeClaimTemplates[i].Name == "data" {
			// Update storage class
			statefulSet.Spec.VolumeClaimTemplates[i].Spec.StorageClassName = &newStorageClass

			// Update capacity
			statefulSet.Spec.VolumeClaimTemplates[i].Spec.Resources.Requests[corev1.ResourceStorage] = newDataCapacity

			rack.Log().Infof("Updated StatefulSet VolumeClaimTemplate 'data': storageClass=%s, capacity=%s",
				newStorageClass, newDataCapacity.String())
			break
		}
	}

	// Create the StatefulSet to restore normal management
	err = stsClient.CreateStatefulSet(ctx, statefulSet)
	if err != nil {
		return as.Error(fmt.Errorf("failed to recreate StatefulSet: %w", err))
	}

	rack.Log().Infof("Recreated StatefulSet %s with new storage configuration (class=%s, capacity=%s)",
		statefulSet.Name, newStorageClass, newDataCapacity.String())
	return as.Break()
}

// waitForStatefulSetReadyAfterRecreation waits for the StatefulSet to be fully ready after recreation
// After recreating the STS, pods will restart (migration init container removed)
// We need to wait for the STS to be updated and all pods to be ready
func waitForStatefulSetReadyAfterRecreation(ctx context.Context, rack view.RackView, cc *api.CassandraCluster,
	stsClient stsClient.StsClient, podsClient pods.PodsClient) as.StepResult {

	// Only run this check if all pods are migrated
	if rack.RackStatus().StorageMigrationState == nil || !rack.RackStatus().StorageMigrationState.AllPodsMigrated() {
		return as.Pass()
	}

	// Get StatefulSet
	statefulSet := rack.LivingStatefulSet()
	if statefulSet == nil {
		rack.Log().Info("Waiting for StatefulSet to be created")
		return as.Break()
	}

	// Check if StatefulSet is ready (all replicas are ready)
	if statefulSet.Status.ReadyReplicas != *statefulSet.Spec.Replicas {
		rack.Log().Infof("Waiting for StatefulSet to be ready: %d/%d pods ready",
			statefulSet.Status.ReadyReplicas, *statefulSet.Spec.Replicas)
		return as.Break()
	}

	// Verify all pods are actually ready and don't have migration init container anymore
	namespace := cc.Namespace
	labels := rack.GetLabelsForCassandraDCRack(cc)
	podList, err := podsClient.ListPods(ctx, namespace, labels)
	if err != nil {
		return as.Error(fmt.Errorf("failed to list pods: %w", err))
	}

	for _, pod := range podList.Items {
		// Check if pod is Ready
		if !cassandrapod.IsReady(&pod) {
			rack.Log().Infof("Waiting for pod %s to be Ready after STS recreation", pod.Name)
			return as.Break()
		}

		// Check if migration init container is gone (pod was recreated without it)
		if pod.Annotations != nil && pod.Annotations["cassandraclusters.db.orange.com/storage-migration"] == "true" {
			rack.Log().Infof("Waiting for pod %s to be recreated without migration init container", pod.Name)
			return as.Break()
		}
	}

	rack.Log().Infof("StatefulSet fully ready after recreation: %d/%d pods ready and clean",
		statefulSet.Status.ReadyReplicas, *statefulSet.Spec.Replicas)
	return as.Pass()
}

// checkAndFinalizeMigration checks if all pods are migrated and finalizes if done
func checkAndFinalizeMigration(rack view.RackView) as.StepResult {
	if rack.RackStatus().StorageMigrationState == nil {
		return as.Error(errors.New("migration state not initialized"))
	}

	// Check if all pods are migrated
	totalPods := len(rack.RackStatus().StorageMigrationState.Pods)

	if !rack.RackStatus().StorageMigrationState.AllPodsMigrated() {
		// Count migrated pods for logging
		migratedCount := 0
		for _, podState := range rack.RackStatus().StorageMigrationState.Pods {
			if podState.Migrated {
				migratedCount++
			}
		}
		rack.Log().Infof("Migration in progress: %d/%d pods migrated", migratedCount, totalPods)
		return as.Break()
	}

	// All pods migrated, finalize
	rack.Log().Infof("All %d pods migrated successfully, finalizing migration action", totalPods)
	return sc.FinalizeMigrationAction(rack.RackStatus(), api.ActionStorageMigration)
}
