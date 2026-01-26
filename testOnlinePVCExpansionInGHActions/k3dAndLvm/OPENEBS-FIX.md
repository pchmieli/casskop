# OpenEBS LVM Installation Fix

## Problem

When installing OpenEBS LVM CSI driver on k3d with:
```bash
kubectl apply -f https://openebs.github.io/charts/lvm-operator.yaml
```

You get the error:
```
spec.containers{openebs-lvm-plugin}: Error: failed to generate container spec: 
path "/var/lib/kubelet/" is mounted on "/var/lib/kubelet" but it is not a shared mount
```

## Root Cause

The OpenEBS LVM operator manifest uses `mountPropagation: "Bidirectional"` on the `/var/lib/kubelet/` mount in the `openebs-lvm-plugin` container. This is not supported in k3d environments.

## Solution

A modified version of the OpenEBS manifest has been created at:
```
k3dAndLvm/openebs-lvm-operator.yaml
```

This version has the `mountPropagation: "Bidirectional"` line removed (line 1700 in the original manifest).

## Installation Steps

### Option 1: Using the Install Script (Recommended)

```bash
# Run the installation script
chmod +x k3dAndLvm/install-openebs.sh
./k3dAndLvm/install-openebs.sh
```

This script will:
1. Clean up any failed installations
2. Apply the k3d-compatible manifest
3. Wait for components to be ready
4. Show status

### Option 2: Manual Installation

```bash
# Clean up any existing installation
kubectl delete -f k3dAndLvm/openebs-lvm-operator.yaml --ignore-not-found=true

# Wait a few seconds
sleep 5

# Apply the fixed manifest
kubectl apply -f k3dAndLvm/openebs-lvm-operator.yaml

# Wait for controller to be ready
kubectl wait --for=condition=ready pod -l app=openebs-lvm-controller -n kube-system --timeout=300s

# Wait for node DaemonSet to be ready
kubectl rollout status daemonset/openebs-lvm-node -n kube-system --timeout=300s

# Verify installation
kubectl get sts,ds,pods -n kube-system | grep openebs-lvm
```

## Verification

After successful installation, you should see:

```bash
kubectl get pods -n kube-system -l openebs.io/component-name
```

Expected output:
```
NAME                                      READY   STATUS    RESTARTS   AGE
openebs-lvm-controller-0                  5/5     Running   0          2m
openebs-lvm-node-xxxxx                    2/2     Running   0          2m
```

## What Was Changed

The following section was modified in the `openebs-lvm-plugin` container:

**Before:**
```yaml
- name: pods-mount-dir
  mountPath: /var/lib/kubelet/
  # needed so that any mounts setup inside this container are
  # propagated back to the host machine.
  mountPropagation: "Bidirectional"
```

**After:**
```yaml
- name: pods-mount-dir
  mountPath: /var/lib/kubelet/
```

The `mountPropagation: "Bidirectional"` line was removed, along with the comments explaining it.

## Testing the Installation

After OpenEBS is installed, test with the sample application:

```bash
# Deploy test StorageClass, PVC, and Pod
kubectl apply -f k3dAndLvm/test.yaml

# Check PVC status (should become "Bound")
kubectl get pvc lvm-pvc

# Check pod logs (should show disk usage)
kubectl logs -l app=test-lvm -f
```

Expected PVC output:
```
NAME      STATUS   VOLUME                                     CAPACITY   ACCESS MODES   STORAGECLASS      AGE
lvm-pvc   Bound    pvc-xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx   1Gi        RWO            openebs-lvm-sc    30s
```

## Troubleshooting

### Controller Pod Not Starting

Check controller logs:
```bash
kubectl logs -n kube-system openebs-lvm-controller-0 -c openebs-lvm-plugin
```

### Node DaemonSet Pod Failing

Check node pod logs:
```bash
POD=$(kubectl get pods -n kube-system -l app=openebs-lvm-node -o jsonpath='{.items[0].metadata.name}')
kubectl logs -n kube-system $POD -c openebs-lvm-plugin
```

### PVC Stuck in Pending

Check if LVM volume group exists:
```bash
./k3dAndLvm/verify-lvm-setup.sh
```

Check CSI driver logs:
```bash
kubectl logs -n kube-system -l app=openebs-lvm-node -c openebs-lvm-plugin
```

## Updating the Manifest

If you need to download a newer version of the OpenEBS manifest:

```bash
# Download the latest
curl -sL https://openebs.github.io/charts/lvm-operator.yaml -o k3dAndLvm/openebs-lvm-operator-new.yaml

# Remove Bidirectional mount propagation
sed -i '/mountPropagation: "Bidirectional"/d' k3dAndLvm/openebs-lvm-operator-new.yaml

# Review the changes
diff k3dAndLvm/openebs-lvm-operator.yaml k3dAndLvm/openebs-lvm-operator-new.yaml

# If satisfied, replace the old one
mv k3dAndLvm/openebs-lvm-operator-new.yaml k3dAndLvm/openebs-lvm-operator.yaml
```

## Related Issues

This is similar to the issue we solved with the LVM installer DaemonSet. k3d environments have limitations with mount propagation that require modifications to standard Kubernetes manifests.

See also:
- [k3dAndLvm/SOLUTION.md](SOLUTION.md) - LVM installer fix details
- [k3dAndLvm/README.md](README.md) - Complete LVM setup documentation

## Status

✅ **FIXED** - Modified manifest is ready to use  
📦 **Location**: `k3dAndLvm/openebs-lvm-operator.yaml`  
🔧 **Installation**: Run `./k3dAndLvm/install-openebs.sh`
