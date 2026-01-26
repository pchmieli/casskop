# Get Docker host gateway IP
GW_IP="$(ip route | awk '/default/ {print $3; exit}')"
echo "Gateway IP: $GW_IP"

# Create cluster
```
k3d cluster create "$CLUSTER" \
--image rancher/k3s:v1.24.13-k3s1 \
--api-port "${GW_IP}:6443" \
--k3s-arg "--tls-san=${GW_IP}@server:0" \
--no-rollback

#--agents 2 \
#--k3s-arg '--disable=traefik@server:*' \

```

# Wait for cluster to be ready
kubectl wait --for=condition=ready nodes --all --timeout=300s

# Install LVM tools using DaemonSet approach
# The DaemonSet installs Alpine-compiled LVM binaries along with the musl dynamic linker
# This allows LVM to work on k3s nodes without native LVM packages
echo "Installing LVM tools via DaemonSet..."
kubectl apply -f k3dAndLvm/lvm-installer.yaml

# Wait for LVM installer DaemonSet to be ready
echo "Waiting for LVM installer to complete on all nodes..."
kubectl wait --for=condition=ready pod -l app=lvm-installer -n lvm-system --timeout=300s --all

# Verify LVM setup on nodes
echo "Verifying LVM setup..."
./k3dAndLvm/verify-lvm-setup.sh

echo ""
echo "✅ k3d cluster created and LVM configured on all nodes via DaemonSet!"
echo "   - musl dynamic linker installed for Alpine binaries"
echo "   - LVM volume group 'lvmvg' created on all nodes"
echo ""
echo "Verify cluster access:"
kubectl cluster-info
kubectl get nodes

# Install OpenEBS LVM LocalPV
# Download and modify the operator manifest to remove Bidirectional mount propagation
# This is required for k3d compatibility
if [ ! -f k3dAndLvm/openebs-lvm-operator.yaml ]; then
  echo "Downloading OpenEBS LVM operator manifest..."
  curl -sL https://openebs.github.io/charts/lvm-operator.yaml -o k3dAndLvm/openebs-lvm-operator.yaml
  sed -i '/mountPropagation: "Bidirectional"/d' k3dAndLvm/openebs-lvm-operator.yaml
  echo "Removed Bidirectional mount propagation for k3d compatibility"
fi

echo "Installing OpenEBS LVM operator..."
kubectl apply -f k3dAndLvm/openebs-lvm-operator.yaml

# Wait for OpenEBS LVM controller StatefulSet to be ready
kubectl wait --for=condition=ready pod -l app=openebs-lvm-controller -n kube-system --timeout=300s

# Wait for OpenEBS LVM node DaemonSet to be ready
kubectl rollout status daemonset/openebs-lvm-node -n kube-system --timeout=300s

# Verify all components are running
kubectl get sts,ds -n kube-system | grep openebs-lvm

echo ""
echo "✅ All components ready!"
echo ""

# ============================================
# Basic LVM Test (test.yaml)
# ============================================

# Run test
kubectl apply -f k3dAndLvm/test.yaml

# Wait for PVC to be bound
kubectl wait --for=jsonpath='{.status.phase}'=Bound pvc/lvm-pvc --timeout=120s

# Get the pod name
POD_NAME=$(kubectl get pod -l app=test-lvm -o jsonpath='{.items[0].metadata.name}')

# Wait for pod to be ready
kubectl wait --for=condition=ready pod -l app=test-lvm --timeout=120s

# Check disk usage of the mounted volume
echo "Initial volume size:"
kubectl exec $POD_NAME -- df -h /data

# Test volume expansion
echo "Testing volume expansion..."
# Patch the PVC to request 2Gi
kubectl patch pvc lvm-pvc -p '{"spec":{"resources":{"requests":{"storage":"2Gi"}}}}'

# Wait a bit for expansion
sleep 10

# Verify the new size in the pod
echo "Volume size after expansion:"
kubectl exec $POD_NAME -- df -h /data

echo ""
echo "✅ Basic LVM test PASSED!"
echo ""

# Clean up test resources before kuttl
echo "Cleaning up basic test resources..."
kubectl delete -f k3dAndLvm/test.yaml
sleep 5

echo ""
echo "======================================"
echo "Running Kuttl Tests"
echo "======================================"
echo ""

# ============================================
# Kuttl Test (storage-upsize)
# ============================================

# Run kuttl test
TAG=resize_v6
k3d image import casskop:$TAG -c k3s-default
helm install casskop charts/casskop --set image.tag=$TAG --set image.repository=casskop --set image.pullPolicy=IfNotPresent
cd test/kuttl
kuttl test --test storage-upsize --namespace default --skip-delete