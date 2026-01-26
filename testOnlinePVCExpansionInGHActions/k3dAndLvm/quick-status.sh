#!/bin/bash
# Quick reference commands for LVM on k3d

# Colors for output
GREEN='\033[0;32m'
BLUE='\033[0;34m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

echo -e "${BLUE}========================================${NC}"
echo -e "${BLUE}K3d LVM Quick Reference${NC}"
echo -e "${BLUE}========================================${NC}"
echo ""

# Function to run LVM command on node
run_lvm_cmd() {
  local cmd=$1
  local pod=$(kubectl get pods -n lvm-system -l app=lvm-installer -o jsonpath='{.items[0].metadata.name}' 2>/dev/null)

  if [ -z "$pod" ]; then
    echo -e "${YELLOW}No LVM installer pod found${NC}"
    return 1
  fi

  kubectl exec -n lvm-system $pod -- nsenter --mount=/proc/1/ns/mnt -- sh -c "
    export LD_LIBRARY_PATH=/usr/local/lib:\$LD_LIBRARY_PATH
    export PATH=/usr/local/sbin:/usr/local/bin:\$PATH
    $cmd
  " 2>/dev/null
}

# Check if cluster exists
if ! kubectl cluster-info &>/dev/null; then
  echo -e "${YELLOW}No k3d cluster found. Create one first:${NC}"
  echo "  k3d cluster create k3s-default"
  exit 1
fi

# Show cluster info
echo -e "${GREEN}Cluster:${NC}"
kubectl get nodes -o wide
echo ""

# Show LVM DaemonSet status
echo -e "${GREEN}LVM Installer Status:${NC}"
kubectl get daemonset,pods -n lvm-system 2>/dev/null || echo "  Not installed. Run: kubectl apply -f k3dAndLvm/lvm-installer.yaml"
echo ""

# Show volume groups
echo -e "${GREEN}Volume Groups:${NC}"
run_lvm_cmd "vgs" || echo "  Unable to retrieve VG information"
echo ""

# Show physical volumes
echo -e "${GREEN}Physical Volumes:${NC}"
run_lvm_cmd "pvs" || echo "  Unable to retrieve PV information"
echo ""

# Show logical volumes (if any)
echo -e "${GREEN}Logical Volumes:${NC}"
run_lvm_cmd "lvs" 2>/dev/null || echo "  No logical volumes created yet"
echo ""

# Show OpenEBS LVM components
echo -e "${GREEN}OpenEBS LVM Components:${NC}"
kubectl get pods -n kube-system -l 'openebs.io/component-name in (openebs-lvm-controller,openebs-lvm-node)' 2>/dev/null || echo "  Not installed. Run: kubectl apply -f https://openebs.github.io/charts/lvm-operator.yaml"
echo ""

# Show StorageClasses
echo -e "${GREEN}Storage Classes:${NC}"
kubectl get storageclass 2>/dev/null || echo "  None found"
echo ""

# Show PVCs
echo -e "${GREEN}Persistent Volume Claims:${NC}"
kubectl get pvc -A 2>/dev/null || echo "  None found"
echo ""

# Show PVs
echo -e "${GREEN}Persistent Volumes:${NC}"
kubectl get pv 2>/dev/null || echo "  None found"
echo ""

echo -e "${BLUE}========================================${NC}"
echo -e "${BLUE}Common Commands:${NC}"
echo -e "${BLUE}========================================${NC}"
echo ""
echo "View LVM logs:"
echo "  kubectl logs -n lvm-system -l app=lvm-installer -f"
echo ""
echo "Verify LVM setup:"
echo "  ./k3dAndLvm/verify-lvm-setup.sh"
echo ""
echo "Run LVM command on node:"
echo '  POD=$(kubectl get pods -n lvm-system -l app=lvm-installer -o jsonpath='\''{.items[0].metadata.name}'\'')'
echo '  kubectl exec -n lvm-system $POD -- nsenter --mount=/proc/1/ns/mnt -- sh -c \\'
echo '    "export LD_LIBRARY_PATH=/usr/local/lib:\$LD_LIBRARY_PATH; export PATH=/usr/local/sbin:/usr/local/bin:\$PATH; vgs"'
echo ""
echo "Deploy test application:"
echo "  kubectl apply -f k3dAndLvm/test.yaml"
echo "  kubectl get pvc lvm-pvc"
echo "  kubectl logs -l app=test-lvm -f"
echo ""
