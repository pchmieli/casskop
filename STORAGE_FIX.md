# GitHub Actions Storage Issue - Diagnosis and Fix

## Problem Summary

The kuttl `storage-upsize` test is failing in GitHub Actions with:
- Error: `0/1 nodes are available: 1 node(s) did not have enough free storage`
- Warning: `InvalidDiskCapacity invalid capacity 0 on image filesystem`
- PVC stuck in `Pending` state
- Pod `cassandra-e2e-dc1-rack1-0` not starting due to scheduling failure

## Root Cause

The k3s kubelet is incorrectly detecting **0 capacity** on the image filesystem, which causes:
1. Kubernetes scheduler to reject pod placement (thinks there's no storage)
2. PVC cannot bind (WaitForFirstConsumer binding mode)
3. Test timeout

Additionally, OpenEBS LVM operator was failing with mount propagation errors in k3d.

This is likely due to:
- k3s default eviction thresholds being too aggressive (imagefs.available<1%, nodefs.available<1%)
- kubelet not properly detecting the filesystem where containerd stores images
- Initial kubelet startup before LVM is configured may cache incorrect capacity
- OpenEBS `mountPropagation: "Bidirectional"` incompatible with k3d's container environment

## Changes Made

### 1. **Relaxed Kubelet Eviction Thresholds** (`.github/workflows/e2e-tests.yml`)

Changed k3d setup to use less aggressive eviction settings:
```yaml
--k3s-arg "--kubelet-arg=eviction-hard=imagefs.available<1%,nodefs.available<1%@server:0"
--k3s-arg "--kubelet-arg=eviction-minimum-reclaim=imagefs.available=5%,nodefs.available=5%@server:0"
--k3s-arg "--kubelet-arg=image-service-endpoint=unix:///run/k3s/containerd/containerd.sock@server:0"
```

**Why**: The original thresholds (5% hard, 10% reclaim) were too aggressive for CI environments with limited storage. The new 1%/5% thresholds still protect against full disks but are more realistic.

### 2. **Added Kubelet Restart After LVM Setup**

After LVM setup completes, the workflow now:
1. Restarts k3s: `docker exec $NODE_CONTAINER sh -c 'kill -HUP 1'`
2. Waits for cluster to stabilize: `sleep 30`
3. Waits for node ready: `kubectl wait --for=condition=ready node --all`

**Why**: Kubelet caches filesystem capacity at startup. If it starts before LVM is configured, it may report incorrect values. Restarting forces a fresh capacity detection.

### 3. **Added Comprehensive Background Diagnostics**

New step `Start background pod diagnostics` that monitors every 20 seconds:
- Cassandra pod status and events
- PVC binding status
- PV creation
- Node conditions (DiskPressure, MemoryPressure)
- Node capacity and allocatable resources
- Recent cluster events

**Why**: Provides real-time visibility into what's happening during test execution, making it easier to diagnose failures.

### 5. **Fixed OpenEBS LVM Operator Installation**

The OpenEBS LVM operator manifest is now:
1. Downloaded to a temporary file
2. Modified to remove `mountPropagation: "Bidirectional"` (incompatible with k3d)
3. Applied to the cluster
4. Properly waited on using `kubectl wait` and `kubectl rollout status`

**Why**: k3d containers don't support bidirectional mount propagation. The original direct apply from URL was failing with mount propagation errors, preventing OpenEBS from starting.

### 6. **Enhanced Final Diagnostics Output**

Added detailed diagnostics in the `Install OpenEBS LVM LocalPV` step:
- Host and node filesystem status
- Node capacity and allocatable resources
- Node conditions
- Kubelet config inspection
- LVM volume group status
- Storage class listing

**Why**: Comprehensive state snapshot to understand cluster configuration before test starts.

### 7. **Added Diagnostics Dump Step**

New `Dump background diagnostics` step (runs always, even on failure):
- Outputs complete background monitoring log
- Lists all pods, PVCs, PVs across all namespaces
- Shows all cluster events
- Provides full node description

**Why**: Ensures we capture complete diagnostic info even when tests fail.

## Expected Outcome

After these changes:
1. ✅ k3s kubelet should properly detect available storage
2. ✅ No more "InvalidDiskCapacity invalid capacity 0" errors
3. ✅ Pod scheduler should find available storage on the node
4. ✅ PVC should bind successfully to the pod
5. ✅ Cassandra pod should start successfully
6. ✅ Rich diagnostics available for any future issues

## How to Verify

Run the workflow and check:
1. Node events should show no "InvalidDiskCapacity" warnings
2. `kubectl describe nodes` should show non-zero ephemeral-storage capacity
3. Background diagnostics should show PVC transitioning to Bound
4. Pod should transition to Running state
5. Test should pass

## Additional Notes

- The k3d cluster is configured with 72GB root filesystem with ~22GB available
- LVM is configured with a 3GB loop device for testing
- OpenEBS LVM LocalPV uses WaitForFirstConsumer binding mode
- The test requires 1Gi storage for Cassandra data volume
