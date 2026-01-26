# K3d LVM Setup - Solution Documentation

## 📋 Documentation Index

- **[README.md](README.md)** (this file) - Complete setup guide and architecture
- **[SUCCESS.md](SUCCESS.md)** - 🎉 Complete success summary and test results
- **[SOLUTION.md](SOLUTION.md)** - Detailed problem analysis and fix summary
- **[OPENEBS-FIX.md](OPENEBS-FIX.md)** - OpenEBS LVM CSI driver fix for k3d compatibility
- **[working.md](working.md)** - Step-by-step cluster creation instructions
- **[prepare-env.md](prepare-env.md)** - Environment preparation guide
- **[lvm-installer.yaml](lvm-installer.yaml)** - The DaemonSet that makes LVM work
- **[openebs-lvm-operator.yaml](openebs-lvm-operator.yaml)** - Modified OpenEBS manifest (k3d-compatible)
- **[test.yaml](test.yaml)** - Sample StorageClass, PVC, and deployment
- **[verify-lvm-setup.sh](verify-lvm-setup.sh)** - Automated verification script
- **[install-openebs.sh](install-openebs.sh)** - OpenEBS installation script
- **[quick-status.sh](quick-status.sh)** - Quick status overview

## Overview

This directory contains a **working solution** for setting up LVM (Logical Volume Manager) on k3d clusters. The solution uses a DaemonSet to install LVM tools and create volume groups on all k3d nodes, enabling dynamic persistent volume provisioning with OpenEBS LVM CSI driver.

**Status**: ✅ **FULLY WORKING** as of January 19, 2026

## Problem Solved

K3d nodes (which run k3s in Docker containers) don't have LVM packages installed by default. This solution:
1. Installs LVM tools from Alpine Linux packages
2. Handles the **musl vs glibc** incompatibility between Alpine and k3s nodes
3. Creates a loop device-backed volume group for testing/development
4. Keeps LVM services running throughout the cluster lifecycle

## Key Technical Challenge: Dynamic Linker Compatibility

The main challenge was that Alpine-compiled binaries require the **musl** dynamic linker (`/lib/ld-musl-x86_64.so.1`), while most Linux distributions use **glibc**. Without the musl linker, binaries show "not found" even when they exist on the filesystem.

### Solution

The DaemonSet approach:
1. **Init container**: Installs LVM packages in Alpine and copies binaries, libraries, AND the musl dynamic linker to a shared volume
2. **Main container**: Copies everything to the host filesystem, including the critical musl linker to `/lib/`
3. **LVM setup**: Runs in the host mount namespace with proper environment variables

## Files

- **lvm-installer.yaml**: DaemonSet that installs and manages LVM on all nodes
- **test.yaml**: Sample StorageClass, PVC, and test deployment
- **verify-lvm-setup.sh**: Script to verify LVM is working correctly
- **working.md**: Step-by-step instructions for cluster setup
- **prepare-env.md**: Environment preparation documentation

## Quick Start

### 1. Create k3d Cluster

```bash
# Get Docker host gateway IP
GW_IP="$(ip route | awk '/default/ {print $3; exit}')"

# Create cluster
k3d cluster create k3s-default \
  --image rancher/k3s:v1.24.13-k3s1 \
  --api-port "${GW_IP}:6443" \
  --k3s-arg "--tls-san=${GW_IP}@server:0" \
  --no-rollback

# Wait for nodes to be ready
kubectl wait --for=condition=ready nodes --all --timeout=300s
```

### 2. Install LVM Tools

```bash
# Deploy the LVM installer DaemonSet
kubectl apply -f k3dAndLvm/lvm-installer.yaml

# Wait for pods to be ready
kubectl wait --for=condition=ready pod -l app=lvm-installer -n lvm-system --timeout=300s --all

# Verify LVM setup
./k3dAndLvm/verify-lvm-setup.sh
```

Expected output:
```
✅ LVM Setup Verification Complete!
  Volume group 'lvmvg' exists
  VG    #PV #LV #SN Attr   VSize   VFree
  lvmvg   1   0   0 wz--n- <10.00g <10.00g
```

### 3. Install OpenEBS LVM CSI Driver

**Important**: The standard OpenEBS manifest has been modified to work with k3d. See [OPENEBS-FIX.md](OPENEBS-FIX.md) for details.

```bash
# Option 1: Use the installation script (recommended)
chmod +x k3dAndLvm/install-openebs.sh
./k3dAndLvm/install-openebs.sh

# Option 2: Manual installation
kubectl apply -f k3dAndLvm/openebs-lvm-operator.yaml

# Wait for components to be ready
kubectl wait --for=condition=ready pod -l app=openebs-lvm-controller -n kube-system --timeout=300s
kubectl rollout status daemonset/openebs-lvm-node -n kube-system --timeout=300s
```

### 4. Test the Setup

```bash
# Deploy test resources (StorageClass, PVC, Pod)
kubectl apply -f k3dAndLvm/test.yaml

# Check PVC status
kubectl get pvc lvm-pvc
# Should show "Bound" status

# Check pod logs
kubectl logs -l app=test-lvm -f
# Should show disk usage of the mounted LVM volume
```

## How It Works

### Architecture

```
┌─────────────────────────────────────────────────────────────┐
│ k3d Node (k3s in Docker container)                          │
│                                                              │
│  ┌────────────────────────────────────────────────────────┐ │
│  │ Init Container: install-lvm-tools (Alpine 3.19)        │ │
│  │  - Install LVM packages from Alpine repo              │ │
│  │  - Copy binaries to /lvm-tools/sbin                   │ │
│  │  - Copy libraries to /lvm-tools/lib                   │ │
│  │  - Copy musl linker to /lvm-tools/lib                 │ │
│  └────────────────────────────────────────────────────────┘ │
│                            ↓                                │
│  ┌────────────────────────────────────────────────────────┐ │
│  │ Main Container: lvm-setup (Alpine 3.19)                │ │
│  │  - Copy tools from /lvm-tools to /host/usr/local/*    │ │
│  │  - Copy musl linker to /host/lib/                     │ │
│  │  - Create loop device (/mnt/disks/disk.img)           │ │
│  │  - Setup LVM: pvcreate → vgcreate lvmvg               │ │
│  │  - Monitor LVM status every 5 minutes                 │ │
│  └────────────────────────────────────────────────────────┘ │
│                            ↓                                │
│  ┌────────────────────────────────────────────────────────┐ │
│  │ Host Filesystem                                        │ │
│  │  /lib/ld-musl-x86_64.so.1 ← musl dynamic linker       │ │
│  │  /usr/local/sbin/lvm, pvcreate, vgcreate, etc.        │ │
│  │  /usr/local/lib/libdevmapper.so.*, libaio.so.*, etc.  │ │
│  │  /mnt/disks/disk.img ← 10GB loop device               │ │
│  │  /dev/loop0 ← mounted loop device                     │ │
│  └────────────────────────────────────────────────────────┘ │
│                            ↓                                │
│  ┌────────────────────────────────────────────────────────┐ │
│  │ LVM Configuration                                      │ │
│  │  Physical Volume: /dev/loop0                          │ │
│  │  Volume Group: lvmvg (10GB)                           │ │
│  │  Available for: OpenEBS LVM CSI driver                │ │
│  └────────────────────────────────────────────────────────┘ │
└─────────────────────────────────────────────────────────────┘
```

### Key Components

1. **Musl Dynamic Linker**: `/lib/ld-musl-x86_64.so.1` - Required for Alpine binaries
2. **LVM Binaries**: Installed in `/usr/local/sbin/` on the host
3. **LVM Libraries**: Installed in `/usr/local/lib/` on the host
4. **Loop Device**: `/dev/loop0` backed by `/mnt/disks/disk.img`
5. **Volume Group**: `lvmvg` - Used by OpenEBS LVM CSI driver

### Environment Variables

When running LVM commands on the host:
```bash
export LD_LIBRARY_PATH=/usr/local/lib:$LD_LIBRARY_PATH
export PATH=/usr/local/sbin:/usr/local/bin:$PATH
```

## Troubleshooting

### Check DaemonSet Status

```bash
kubectl get daemonset -n lvm-system
kubectl get pods -n lvm-system
```

### View Logs

```bash
# View main container logs
kubectl logs -n lvm-system -l app=lvm-installer

# View init container logs
kubectl logs -n lvm-system -l app=lvm-installer -c install-lvm-tools
```

### Verify LVM Manually

```bash
# Get pod name
POD=$(kubectl get pods -n lvm-system -l app=lvm-installer -o jsonpath='{.items[0].metadata.name}')

# Run LVM commands in the pod
kubectl exec -n lvm-system $POD -- nsenter --mount=/proc/1/ns/mnt -- sh -c '
  export LD_LIBRARY_PATH=/usr/local/lib:$LD_LIBRARY_PATH
  export PATH=/usr/local/sbin:/usr/local/bin:$PATH
  vgs
  pvs
  lvs
'
```

### Common Issues

#### "lvm: not found" even though binary exists
**Cause**: Musl dynamic linker not installed on host  
**Fix**: Ensure init container is copying `/lib/ld-musl-*.so.1` and main container is copying it to `/host/lib/`

#### "No such file or directory" for libraries
**Cause**: Libraries not in the expected path  
**Fix**: Verify `LD_LIBRARY_PATH=/usr/local/lib` is set when running LVM commands

#### Volume group not found
**Cause**: LVM setup didn't complete successfully  
**Fix**: Check logs for errors during loop device setup or vgcreate

## Development Notes

### Tested Versions

- k3s: v1.24.13-k3s1
- Alpine: 3.19
- LVM: 2.03.23
- OpenEBS LVM CSI: latest

### Loop Device Configuration

The DaemonSet creates a 10GB loop device for testing. For production:
- Use real block devices
- Modify the LVM setup section to use actual disks
- Update volume group size as needed

### Customization

To change the volume group name or size, edit `lvm-installer.yaml`:
```yaml
# Change volume group name
vgcreate lvmvg $LOOP_DEVICE  # Replace 'lvmvg' with your VG name

# Change disk size
truncate -s 10G /mnt/disks/disk.img  # Change '10G' to desired size
```

## References

- [OpenEBS LVM LocalPV](https://openebs.io/docs/user-guides/localpv-lvm)
- [k3d Documentation](https://k3d.io/)
- [Alpine Linux Packages](https://pkgs.alpinelinux.org/)
- [LVM2 Documentation](https://man7.org/linux/man-pages/man8/lvm.8.html)

## Credits

Developed to solve the challenge of running LVM-based storage in k3d development clusters without requiring privileged Docker builds or host system modifications.
