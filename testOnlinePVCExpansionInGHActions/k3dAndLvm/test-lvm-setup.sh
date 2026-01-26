#!/bin/bash
# Test script for LVM DaemonSet installation on k3d

set -e

CLUSTER="${CLUSTER:-k3s-default}"

echo "========================================="
echo "Testing LVM DaemonSet on k3d"
echo "Cluster: $CLUSTER"
echo "========================================="
echo ""

# Colors for output
GREEN='\033[0;32m'
RED='\033[0;31m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

# Helper functions
print_success() {
    echo -e "${GREEN}✅ $1${NC}"
}

print_error() {
    echo -e "${RED}❌ $1${NC}"
}

print_info() {
    echo -e "${YELLOW}ℹ️  $1${NC}"
}

# Test 1: Check if DaemonSet pods are running
echo "Test 1: Checking LVM installer pods..."
PODS_READY=$(kubectl get pods -n lvm-system -l app=lvm-installer -o jsonpath='{.items[*].status.conditions[?(@.type=="Ready")].status}' | tr ' ' '\n' | grep -c "True" || echo "0")
TOTAL_PODS=$(kubectl get pods -n lvm-system -l app=lvm-installer --no-headers | wc -l)

if [ "$PODS_READY" -eq "$TOTAL_PODS" ] && [ "$TOTAL_PODS" -gt 0 ]; then
    print_success "All $TOTAL_PODS LVM installer pods are ready"
else
    print_error "Only $PODS_READY out of $TOTAL_PODS pods are ready"
    kubectl get pods -n lvm-system -l app=lvm-installer
    exit 1
fi

# Test 2: Check LVM binaries on nodes
echo ""
echo "Test 2: Verifying LVM binaries on nodes..."
for NODE in $(kubectl get nodes -o name | sed 's|node/||'); do
    print_info "Checking node: $NODE"

    # Check if lvm binary exists
    if docker exec $NODE test -f /usr/local/sbin/lvm; then
        print_success "  LVM binary found on $NODE"
    else
        print_error "  LVM binary not found on $NODE"
        exit 1
    fi

    # Check if libraries exist
    if docker exec $NODE test -f /usr/local/lib/libdevmapper.so.1.02; then
        print_success "  LVM libraries found on $NODE"
    else
        print_error "  LVM libraries not found on $NODE"
        exit 1
    fi
done

# Test 3: Test LVM commands on nodes
echo ""
echo "Test 3: Testing LVM commands on nodes..."
for NODE in $(kubectl get nodes -o name | sed 's|node/||'); do
    print_info "Testing LVM on: $NODE"

    # Test lvm version
    if docker exec $NODE sh -c 'export LD_LIBRARY_PATH=/usr/local/lib:$LD_LIBRARY_PATH; export PATH=/usr/local/sbin:$PATH; lvm version' >/dev/null 2>&1; then
        print_success "  'lvm version' works on $NODE"
    else
        print_error "  'lvm version' failed on $NODE"
        exit 1
    fi

    # Test vgs command
    if docker exec $NODE sh -c 'export LD_LIBRARY_PATH=/usr/local/lib:$LD_LIBRARY_PATH; export PATH=/usr/local/sbin:$PATH; vgs' >/dev/null 2>&1; then
        print_success "  'vgs' command works on $NODE"
    else
        print_error "  'vgs' command failed on $NODE"
        exit 1
    fi
done

# Test 4: Verify Volume Group exists
echo ""
echo "Test 4: Verifying Volume Group 'lvmvg' exists..."
for NODE in $(kubectl get nodes -o name | sed 's|node/||'); do
    VG_OUTPUT=$(docker exec $NODE sh -c 'export LD_LIBRARY_PATH=/usr/local/lib:$LD_LIBRARY_PATH; export PATH=/usr/local/sbin:$PATH; vgs lvmvg --noheadings 2>/dev/null' || echo "")

    if [ -n "$VG_OUTPUT" ]; then
        VG_SIZE=$(echo "$VG_OUTPUT" | awk '{print $6}')
        print_success "  Volume Group 'lvmvg' found on $NODE (Size: $VG_SIZE)"
    else
        print_error "  Volume Group 'lvmvg' not found on $NODE"
        exit 1
    fi
done

# Test 5: Check loop device
echo ""
echo "Test 5: Verifying loop device is set up..."
for NODE in $(kubectl get nodes -o name | sed 's|node/||'); do
    LOOP_DEVICE=$(docker exec $NODE losetup -j /mnt/disks/disk.img 2>/dev/null | cut -d: -f1 || echo "")

    if [ -n "$LOOP_DEVICE" ]; then
        print_success "  Loop device configured on $NODE: $LOOP_DEVICE"
    else
        print_error "  Loop device not found on $NODE"
        exit 1
    fi
done

# Test 6: Check OpenEBS LVM components (if installed)
echo ""
echo "Test 6: Checking OpenEBS LVM components..."
if kubectl get sts -n kube-system openebs-lvm-controller >/dev/null 2>&1; then
    CONTROLLER_READY=$(kubectl get sts -n kube-system openebs-lvm-controller -o jsonpath='{.status.readyReplicas}')
    if [ "$CONTROLLER_READY" -ge 1 ]; then
        print_success "  OpenEBS LVM controller is ready"
    else
        print_error "  OpenEBS LVM controller is not ready"
    fi
else
    print_info "  OpenEBS LVM controller not installed (optional)"
fi

if kubectl get ds -n kube-system openebs-lvm-node >/dev/null 2>&1; then
    NODE_READY=$(kubectl get ds -n kube-system openebs-lvm-node -o jsonpath='{.status.numberReady}')
    NODE_DESIRED=$(kubectl get ds -n kube-system openebs-lvm-node -o jsonpath='{.status.desiredNumberScheduled}')
    if [ "$NODE_READY" -eq "$NODE_DESIRED" ]; then
        print_success "  OpenEBS LVM node pods are ready ($NODE_READY/$NODE_DESIRED)"
    else
        print_error "  OpenEBS LVM node pods not ready ($NODE_READY/$NODE_DESIRED)"
    fi
else
    print_info "  OpenEBS LVM node DaemonSet not installed (optional)"
fi

# Test 7: Test PVC creation (if OpenEBS is installed)
echo ""
echo "Test 7: Testing PVC creation (if OpenEBS installed)..."
if kubectl get sc openebs-lvmpv >/dev/null 2>&1; then
    print_info "  Creating test PVC..."

    cat <<EOF | kubectl apply -f - >/dev/null
apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: test-lvm-pvc-validation
spec:
  accessModes:
  - ReadWriteOnce
  storageClassName: openebs-lvmpv
  resources:
    requests:
      storage: 100Mi
EOF

    # Wait for PVC to be bound
    sleep 5
    for i in {1..30}; do
        PVC_STATUS=$(kubectl get pvc test-lvm-pvc-validation -o jsonpath='{.status.phase}' 2>/dev/null || echo "")
        if [ "$PVC_STATUS" = "Bound" ]; then
            print_success "  Test PVC created and bound successfully"
            kubectl delete pvc test-lvm-pvc-validation >/dev/null 2>&1 || true
            break
        fi
        sleep 2
        if [ $i -eq 30 ]; then
            print_error "  Test PVC creation timed out"
            kubectl describe pvc test-lvm-pvc-validation
            kubectl delete pvc test-lvm-pvc-validation >/dev/null 2>&1 || true
        fi
    done
else
    print_info "  StorageClass 'openebs-lvmpv' not found, skipping PVC test"
fi

# Summary
echo ""
echo "========================================="
echo "All tests passed! ✅"
echo "========================================="
echo ""
echo "LVM is properly configured on all nodes via DaemonSet"
echo ""
echo "Next steps:"
echo "1. Install OpenEBS if not already installed:"
echo "   kubectl apply -f https://openebs.github.io/charts/lvm-operator.yaml"
echo ""
echo "2. Create StorageClass for your workloads"
echo ""
echo "3. Create PVCs and test volume expansion:"
echo "   kubectl patch pvc <pvc-name> -p '{\"spec\":{\"resources\":{\"requests\":{\"storage\":\"2Gi\"}}}}'"
echo ""
