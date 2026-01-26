#!/bin/bash
# Clean up failed OpenEBS installation and reinstall with k3d-compatible manifest

echo "=========================================="
echo "OpenEBS LVM Cleanup and Reinstall"
echo "=========================================="
echo ""

# Delete existing OpenEBS LVM resources if they exist
echo "Cleaning up any existing OpenEBS LVM resources..."
kubectl delete -f k3dAndLvm/openebs-lvm-operator.yaml --ignore-not-found=true 2>/dev/null || true

# Wait a moment for cleanup
echo "Waiting for cleanup to complete..."
sleep 5

# Verify cleanup
echo "Checking for remaining OpenEBS pods..."
kubectl get pods -n kube-system -l 'openebs.io/component-name in (openebs-lvm-controller,openebs-lvm-node)' 2>/dev/null || echo "  No OpenEBS pods found (cleanup complete)"
echo ""

# Apply the modified manifest
echo "Installing OpenEBS LVM operator (k3d-compatible version)..."
kubectl apply -f k3dAndLvm/openebs-lvm-operator.yaml

echo ""
echo "Waiting for OpenEBS LVM controller to be ready..."
kubectl wait --for=condition=ready pod -l app=openebs-lvm-controller -n kube-system --timeout=300s 2>&1 || {
  echo "Controller not ready yet. Checking status..."
  kubectl get pods -n kube-system -l app=openebs-lvm-controller
}

echo ""
echo "Waiting for OpenEBS LVM node DaemonSet to be ready..."
kubectl rollout status daemonset/openebs-lvm-node -n kube-system --timeout=300s 2>&1 || {
  echo "Node DaemonSet not ready yet. Checking status..."
  kubectl get daemonset -n kube-system openebs-lvm-node
}

echo ""
echo "=========================================="
echo "OpenEBS LVM Status"
echo "=========================================="
kubectl get sts,ds,pods -n kube-system | grep openebs-lvm || echo "No OpenEBS LVM resources found"

echo ""
echo "If pods are running successfully, you can now:"
echo "1. Apply the StorageClass: kubectl apply -f k3dAndLvm/test.yaml"
echo "2. Check PVC creation: kubectl get pvc"
echo ""
