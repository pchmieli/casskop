# LVM Setup on k3d Using DaemonSet

This approach installs LVM tools on k3d nodes using a Kubernetes DaemonSet instead of directly installing via `docker exec`. This is more Kubernetes-native and works better in environments where direct node access is limited.

## How It Works

### 1. **Init Container: Install LVM Tools**
   - Uses Alpine Linux as a source for LVM binaries
   - Installs: `lvm2`, `device-mapper`, `thin-provisioning-tools`, filesystem tools
   - Copies all binaries and libraries to `/usr/local/{bin,sbin,lib}` on the host

### 2. **Main Container: Setup LVM Volumes**
   - Configures library paths in the host namespace
   - Creates loop device for storage (`/mnt/disks/disk.img`)
   - Creates LVM physical volume (PV)
   - Creates LVM volume group (VG) named `lvmvg`
   - Monitors LVM status

## Why DaemonSet Instead of Direct Installation?

### Advantages:
1. **No package manager required** - k3s nodes don't have `apt`, `apk`, or `yum`
2. **Kubernetes-native** - Uses standard Kubernetes resources
3. **Repeatable** - Easy to apply/remove with `kubectl`
4. **CI/CD friendly** - Works in GitHub Actions and other automated environments
5. **Monitoring** - Container can monitor and report LVM status
6. **Crash recovery** - Kubernetes will restart the pod if it fails

### Challenges Solved:
- ✅ No package manager on k3s nodes
- ✅ Library dependencies properly copied
- ✅ Persistent across node restarts (via hostPath)
- ✅ Works with k3s minimal environment

## Usage

### Quick Start
```bash
# Create k3d cluster
GW_IP="$(ip route | awk '/default/ {print $3; exit}')"
k3d cluster create k3s-default \
  --image rancher/k3s:v1.24.13-k3s1 \
  --api-port "${GW_IP}:6443" \
  --k3s-arg "--tls-san=${GW_IP}@server:0"

# Wait for cluster
kubectl wait --for=condition=ready nodes --all --timeout=300s

# Install LVM via DaemonSet
kubectl apply -f k3dAndLvm/lvm-installer.yaml

# Wait for installation
kubectl wait --for=condition=ready pod -l app=lvm-installer -n lvm-system --timeout=300s --all

# Verify LVM is working
kubectl logs -n lvm-system -l app=lvm-installer -c lvm-setup --tail=50
```

### Verify Installation

Check that LVM commands work on nodes:
```bash
# Get a node name
NODE=$(kubectl get nodes -o name | head -1 | sed 's|node/||')

# Test LVM commands
docker exec $NODE sh -c 'export LD_LIBRARY_PATH=/usr/local/lib:$LD_LIBRARY_PATH; export PATH=/usr/local/sbin:$PATH; vgs'

# Expected output:
#   VG    #PV #LV #SN Attr   VSize  VFree
#   lvmvg   1   0   0 wz--n- <10.00g <10.00g
```

### Install OpenEBS LVM LocalPV

After LVM tools are installed:
```bash
# Install OpenEBS LVM operator
kubectl apply -f https://openebs.github.io/charts/lvm-operator.yaml

# Wait for OpenEBS components
kubectl wait --for=condition=ready pod -l app=openebs-lvm-controller -n kube-system --timeout=300s
kubectl rollout status daemonset/openebs-lvm-node -n kube-system --timeout=300s

# Verify
kubectl get sts,ds -n kube-system | grep openebs-lvm
```

### Create StorageClass

OpenEBS LVM operator should create this automatically, but if needed:
```yaml
apiVersion: storage.k8s.io/v1
kind: StorageClass
metadata:
  name: openebs-lvm
provisioner: local.csi.openebs.io
allowVolumeExpansion: true
parameters:
  storage: "lvm"
  volgroup: "lvmvg"
```

### Test PVC Creation and Expansion

```bash
# Create test PVC
cat <<EOF | kubectl apply -f -
apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: test-lvm-pvc
spec:
  accessModes:
  - ReadWriteOnce
  storageClassName: openebs-lvm
  resources:
    requests:
      storage: 1Gi
EOF

# Create test pod
cat <<EOF | kubectl apply -f -
apiVersion: v1
kind: Pod
metadata:
  name: test-lvm-pod
spec:
  containers:
  - name: app
    image: busybox
    command: ['sh', '-c', 'while true; do df -h /data; sleep 300; done']
    volumeMounts:
    - name: data
      mountPath: /data
  volumes:
  - name: data
    persistentVolumeClaim:
      claimName: test-lvm-pvc
EOF

# Wait for pod
kubectl wait --for=condition=ready pod/test-lvm-pod --timeout=120s

# Check initial size
kubectl exec test-lvm-pod -- df -h /data

# Expand PVC (online, no pod restart required!)
kubectl patch pvc test-lvm-pvc -p '{"spec":{"resources":{"requests":{"storage":"2Gi"}}}}'

# Watch expansion
kubectl get pvc test-lvm-pvc -w

# Verify new size (without restarting pod)
kubectl exec test-lvm-pod -- df -h /data
```

## Troubleshooting

### Check DaemonSet logs
```bash
# Init container logs (installation)
kubectl logs -n lvm-system -l app=lvm-installer -c install-lvm-tools

# Main container logs (setup)
kubectl logs -n lvm-system -l app=lvm-installer -c lvm-setup
```

### Manually verify on node
```bash
NODE=$(kubectl get nodes -o name | head -1 | sed 's|node/||')

# Check binaries
docker exec $NODE ls -lh /usr/local/sbin/ | grep lvm

# Check libraries
docker exec $NODE ls -lh /usr/local/lib/ | grep -E "lvm|device|aio"

# Test LVM
docker exec $NODE sh -c 'export LD_LIBRARY_PATH=/usr/local/lib:$LD_LIBRARY_PATH; export PATH=/usr/local/sbin:$PATH; lvm version'
```

### Check LVM status
```bash
# Physical volumes
docker exec $NODE sh -c 'export LD_LIBRARY_PATH=/usr/local/lib:$LD_LIBRARY_PATH; export PATH=/usr/local/sbin:$PATH; pvs'

# Volume groups
docker exec $NODE sh -c 'export LD_LIBRARY_PATH=/usr/local/lib:$LD_LIBRARY_PATH; export PATH=/usr/local/sbin:$PATH; vgs'

# Logical volumes
docker exec $NODE sh -c 'export LD_LIBRARY_PATH=/usr/local/lib:$LD_LIBRARY_PATH; export PATH=/usr/local/sbin:$PATH; lvs'
```

### Common Issues

1. **"LVM command failed to execute"**
   - Check that libraries were copied: `kubectl logs -n lvm-system -l app=lvm-installer -c install-lvm-tools | grep "Copying shared libraries"`
   - Verify LD_LIBRARY_PATH is set correctly

2. **"Volume group lvmvg not found"**
   - Check main container logs: `kubectl logs -n lvm-system -l app=lvm-installer -c lvm-setup`
   - Verify loop device was created: `docker exec $NODE losetup -a`

3. **Pod stuck in Init**
   - Check init container logs for errors
   - May need more time for large binary copies

## Cleanup

```bash
# Remove test resources
kubectl delete pod test-lvm-pod
kubectl delete pvc test-lvm-pvc

# Remove OpenEBS
kubectl delete -f https://openebs.github.io/charts/lvm-operator.yaml

# Remove LVM installer
kubectl delete -f k3dAndLvm/lvm-installer.yaml

# Delete cluster
k3d cluster delete k3s-default
```

## Architecture

```
┌─────────────────────────────────────────┐
│ k3d Node (Alpine-based k3s)             │
│                                         │
│ ┌─────────────────────────────────────┐ │
│ │ LVM Installer Pod (DaemonSet)       │ │
│ │                                     │ │
│ │ Init: Install LVM Tools             │ │
│ │  • Download from Alpine repos       │ │
│ │  • Copy to /host/usr/local/*        │ │
│ │                                     │ │
│ │ Main: Setup LVM                     │ │
│ │  • Create loop device               │ │
│ │  • pvcreate, vgcreate               │ │
│ │  • Monitor status                   │ │
│ └─────────────────────────────────────┘ │
│                                         │
│ Host filesystem:                        │
│  /usr/local/sbin/  ← LVM binaries       │
│  /usr/local/lib/   ← Libraries          │
│  /mnt/disks/       ← Disk image         │
└─────────────────────────────────────────┘
         ↓
┌─────────────────────────────────────────┐
│ OpenEBS LVM LocalPV                     │
│  • Uses LVM tools from /usr/local/      │
│  • Creates LVs in "lvmvg" VG            │
│  • Provides CSI driver                  │
└─────────────────────────────────────────┘
         ↓
┌─────────────────────────────────────────┐
│ Application Pods                        │
│  • Mount PVCs backed by LVM             │
│  • Support online resize                │
└─────────────────────────────────────────┘
```

## Comparison with Direct Installation

| Aspect | Direct (docker exec) | DaemonSet |
|--------|---------------------|-----------|
| Package manager needed | ✅ Yes (apt/apk) | ❌ No |
| Works on k3s | ❌ No | ✅ Yes |
| Kubernetes-native | ❌ No | ✅ Yes |
| Monitoring | ❌ Manual | ✅ Built-in |
| CI/CD friendly | ⚠️ Limited | ✅ Yes |
| Crash recovery | ❌ Manual | ✅ Automatic |
| Persistent | ⚠️ Depends | ✅ Via hostPath |
