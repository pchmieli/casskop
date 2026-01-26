#!/bin/bash

set -e

echo "=========================================="
echo "LVM Setup Verification Script"
echo "=========================================="
echo ""

# Check if lvm-system namespace exists
echo "✓ Checking lvm-system namespace..."
kubectl get namespace lvm-system &>/dev/null || {
  echo "✗ lvm-system namespace not found"
  exit 1
}
echo "  ✓ Namespace exists"
echo ""

# Check DaemonSet status
echo "✓ Checking LVM installer DaemonSet..."
DESIRED=$(kubectl get daemonset lvm-installer -n lvm-system -o jsonpath='{.status.desiredNumberScheduled}')
READY=$(kubectl get daemonset lvm-installer -n lvm-system -o jsonpath='{.status.numberReady}')

if [ "$DESIRED" -eq "$READY" ] && [ "$READY" -gt 0 ]; then
  echo "  ✓ DaemonSet ready: $READY/$DESIRED pods"
else
  echo "  ✗ DaemonSet not ready: $READY/$DESIRED pods"
  exit 1
fi
echo ""

# Check pod status
echo "✓ Checking LVM installer pods..."
kubectl get pods -n lvm-system -l app=lvm-installer
echo ""

# Verify LVM volume group on each node
echo "✓ Verifying LVM volume groups on nodes..."
PODS=$(kubectl get pods -n lvm-system -l app=lvm-installer -o jsonpath='{.items[*].metadata.name}')

for POD in $PODS; do
  echo "  Checking pod: $POD"
  VG_OUTPUT=$(kubectl exec -n lvm-system $POD -- nsenter --mount=/proc/1/ns/mnt -- sh -c 'export LD_LIBRARY_PATH=/usr/local/lib:$LD_LIBRARY_PATH; export PATH=/usr/local/sbin:/usr/local/bin:$PATH; vgs lvmvg 2>/dev/null' 2>/dev/null)

  if echo "$VG_OUTPUT" | grep -q "lvmvg"; then
    echo "    ✓ Volume group 'lvmvg' exists"
    echo "$VG_OUTPUT" | sed 's/^/      /'
  else
    echo "    ✗ Volume group 'lvmvg' not found"
    exit 1
  fi
done
echo ""

echo "=========================================="
echo "✅ LVM Setup Verification Complete!"
echo "=========================================="
echo ""
echo "Your LVM environment is ready to use."
echo ""
echo "Next steps:"
echo "1. Install OpenEBS LVM CSI driver (if not already installed)"
echo "2. Apply the StorageClass: kubectl apply -f k3dAndLvm/test.yaml"
echo "3. Create PVCs using the 'openebs-lvm-sc' StorageClass"
echo ""
echo "To test the setup right now:"
echo "  kubectl apply -f k3dAndLvm/test.yaml"
echo "  kubectl get pvc lvm-pvc"
echo "  kubectl get pods -l app=test-lvm"
echo ""
