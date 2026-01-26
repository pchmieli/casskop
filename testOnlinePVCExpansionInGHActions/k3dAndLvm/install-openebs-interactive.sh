#!/bin/bash
# Quick commands to fix and install OpenEBS LVM on k3d

echo "======================================"
echo "OpenEBS LVM Installation for k3d"
echo "======================================"
echo ""

# Check if we're in the right directory
if [ ! -f "k3dAndLvm/openebs-lvm-operator.yaml" ]; then
  echo "❌ Error: openebs-lvm-operator.yaml not found"
  echo "   Please run this from the casskop root directory"
  exit 1
fi

# Verify the file has been fixed
if grep -q "Bidirectional" k3dAndLvm/openebs-lvm-operator.yaml; then
  echo "❌ Error: openebs-lvm-operator.yaml still contains 'Bidirectional'"
  echo "   Running fix..."
  sed -i '/mountPropagation: "Bidirectional"/d' k3dAndLvm/openebs-lvm-operator.yaml
  echo "✅ Fixed!"
else
  echo "✅ Manifest is already fixed (no Bidirectional mount propagation)"
fi

echo ""
echo "Ready to install. Choose an option:"
echo ""
echo "1. Clean install (removes existing and reinstalls)"
echo "2. Fresh install only (assumes nothing is installed)"
echo "3. Check status only"
echo ""
read -p "Enter choice (1-3): " choice

case $choice in
  1)
    echo ""
    echo "🧹 Cleaning up existing installation..."
    kubectl delete -f k3dAndLvm/openebs-lvm-operator.yaml --ignore-not-found=true 2>/dev/null
    sleep 5

    echo "📦 Installing OpenEBS LVM..."
    kubectl apply -f k3dAndLvm/openebs-lvm-operator.yaml

    echo ""
    echo "⏳ Waiting for controller..."
    kubectl wait --for=condition=ready pod -l app=openebs-lvm-controller -n kube-system --timeout=300s || echo "Controller not ready yet"

    echo ""
    echo "⏳ Waiting for node DaemonSet..."
    kubectl rollout status daemonset/openebs-lvm-node -n kube-system --timeout=300s || echo "DaemonSet not ready yet"
    ;;

  2)
    echo ""
    echo "📦 Installing OpenEBS LVM..."
    kubectl apply -f k3dAndLvm/openebs-lvm-operator.yaml

    echo ""
    echo "⏳ Waiting for controller..."
    kubectl wait --for=condition=ready pod -l app=openebs-lvm-controller -n kube-system --timeout=300s || echo "Controller not ready yet"

    echo ""
    echo "⏳ Waiting for node DaemonSet..."
    kubectl rollout status daemonset/openebs-lvm-node -n kube-system --timeout=300s || echo "DaemonSet not ready yet"
    ;;

  3)
    echo ""
    echo "📊 Checking status only..."
    ;;

  *)
    echo "Invalid choice. Exiting."
    exit 1
    ;;
esac

echo ""
echo "======================================"
echo "OpenEBS LVM Status"
echo "======================================"
echo ""
echo "StatefulSets and DaemonSets:"
kubectl get sts,ds -n kube-system | grep openebs-lvm || echo "  None found"
echo ""
echo "Pods:"
kubectl get pods -n kube-system -l 'openebs.io/component-name in (openebs-lvm-controller,openebs-lvm-node)' 2>/dev/null || echo "  None found"
echo ""

# Check if everything is running
CONTROLLER_READY=$(kubectl get pod -n kube-system -l app=openebs-lvm-controller -o jsonpath='{.items[0].status.containerStatuses[?(@.name=="openebs-lvm-plugin")].ready}' 2>/dev/null)
NODE_READY=$(kubectl get daemonset -n kube-system openebs-lvm-node -o jsonpath='{.status.numberReady}' 2>/dev/null)
NODE_DESIRED=$(kubectl get daemonset -n kube-system openebs-lvm-node -o jsonpath='{.status.desiredNumberScheduled}' 2>/dev/null)

if [ "$CONTROLLER_READY" = "true" ] && [ "$NODE_READY" = "$NODE_DESIRED" ] && [ "$NODE_READY" != "" ]; then
  echo "✅ OpenEBS LVM is fully operational!"
  echo ""
  echo "Next steps:"
  echo "  1. Apply test resources: kubectl apply -f k3dAndLvm/test.yaml"
  echo "  2. Check PVC: kubectl get pvc lvm-pvc"
  echo "  3. Check pod: kubectl get pods -l app=test-lvm"
else
  echo "⚠️  OpenEBS LVM is not fully ready yet"
  echo ""
  echo "To check logs:"
  echo "  Controller: kubectl logs -n kube-system openebs-lvm-controller-0 -c openebs-lvm-plugin"
  echo "  Node: kubectl logs -n kube-system -l app=openebs-lvm-node -c openebs-lvm-plugin"
fi

echo ""
