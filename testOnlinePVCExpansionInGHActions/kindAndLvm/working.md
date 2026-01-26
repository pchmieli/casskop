# Step 2: Get Docker host gateway IP
GW_IP="$(ip route | awk '/default/ {print $3; exit}')"
echo "Gateway IP: $GW_IP"

# Create kind configuration file with API server exposed on gateway IP
cat <<EOF > kindAndLvm/kind-lvm-config.yaml
kind: Cluster
apiVersion: kind.x-k8s.io/v1alpha4
networking:
  apiServerAddress: "${GW_IP}"
  apiServerPort: 6443
nodes:
- role: control-plane
- role: worker
  extraMounts:
  - hostPath: /tmp/kind-worker1-lvm
    containerPath: /mnt/disks
- role: worker
  extraMounts:
  - hostPath: /tmp/kind-worker2-lvm
    containerPath: /mnt/disks
EOF

# Create temporary directories for kind worker nodes
mkdir -p /tmp/kind-worker1-lvm /tmp/kind-worker2-lvm

# Create kind cluster with LVM configuration
kind create cluster --config kindAndLvm/kind-lvm-config.yaml --name lvm-test

# Update kubeconfig to use gateway IP
[//]: # (kubectl config set-cluster kind-lvm-test --server=https://${GW_IP}:6443)

# Wait for cluster to be ready
kubectl wait --for=condition=ready nodes --all --timeout=300s

# Automated LVM setup on all worker nodes
WORKER_NODES=$(kubectl get nodes -o name | grep worker | sed 's|node/||')

for NODE in $WORKER_NODES; do
echo "Setting up LVM on $NODE..."

docker exec $NODE bash -c '
# Install LVM and dependencies
apt-get update -qq && apt-get install -y -qq lvm2 thin-provisioning-tools > /dev/null 2>&1

    # Ensure mount point exists
    mkdir -p /mnt/disks

    # Create a 10GB loop device (simulating a physical disk)
    truncate -s 10G /mnt/disks/disk.img
    LOOP_DEVICE=$(losetup -f)
    losetup $LOOP_DEVICE /mnt/disks/disk.img

    # Create LVM physical volume
    pvcreate $LOOP_DEVICE

    # Create LVM volume group named "lvmvg" (matches StorageClass)
    vgcreate lvmvg $LOOP_DEVICE

    echo "✅ LVM setup complete on $(hostname)"
    vgs
'
done

echo ""
echo "✅ kind cluster created and LVM configured on all worker nodes!"
echo ""
echo "Verify cluster access:"
kubectl cluster-info
kubectl get nodes

# Install OpenEBS LVM LocalPV
kubectl apply -f https://openebs.github.io/charts/lvm-operator.yaml

# Wait for OpenEBS LVM controller StatefulSet to be ready
kubectl wait --for=condition=ready pod -l app=openebs-lvm-controller -n kube-system --timeout=300s

# Wait for OpenEBS LVM node DaemonSet to be ready
kubectl rollout status daemonset/openebs-lvm-node -n kube-system --timeout=300s

# Verify all components are running
kubectl get sts,ds -n kube-system | grep openebs-lvm



[//]: # (# Run test)
[//]: # (kubectl apply -f kindAndLvm/test.yaml)
[//]: # (# Get the pod name)
[//]: # (POD_NAME=$&#40;kubectl get pod -l app=test-lvm -o jsonpath='{.items[0].metadata.name}'&#41;)
[//]: # (# Check disk usage of the mounted volume)
[//]: # (kubectl exec $POD_NAME -- df -h /data)

[//]: # (# Patch the PVC to request 2Gi)
[//]: # (kubectl patch pvc lvm-pvc -p '{"spec":{"resources":{"requests":{"storage":"2Gi"}}}}')
[//]: # (# Watch the PVC expansion progress)
[//]: # (kubectl get pvc lvm-pvc -w)
[//]: # (# After expansion completes, verify the new size in the pod)
[//]: # (kubectl exec $POD_NAME -- df -h /data)


# Run kuttl test
TAG=resize_v6
kind load docker-image casskop:$TAG --name=lvm-test
helm install casskop charts/casskop --set image.tag=$TAG --set image.repository=casskop --set image.pullPolicy=IfNotPresent
cd test/kuttl
kuttl test --test storage-upsize --namespace default --skip-delete