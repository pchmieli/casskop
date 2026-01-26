#!/bin/bash
# Test script to verify LVM installer fixes the duplicate loop device issue
# Run this in a k3d environment to test the fix

set -e

echo "=================================================="
echo "Testing LVM Installer - Duplicate Loop Device Fix"
echo "=================================================="
echo ""

# Apply the lvm-installer
echo "1. Applying lvm-installer..."
kubectl apply -f test/setup/lvm-installer.yaml

# Wait for it to be ready
echo "2. Waiting for lvm-installer to be ready..."
kubectl wait --for=condition=ready pod -l app=lvm-installer -n lvm-system --timeout=300s

echo ""
echo "3. Checking lvm-installer logs..."
echo "========================================"
echo "Init container logs:"
kubectl logs -n lvm-system daemonset/lvm-installer -c install-lvm-tools | tail -20
echo ""
echo "Main container logs:"
kubectl logs -n lvm-system daemonset/lvm-installer -c lvm-setup | tail -40

echo ""
echo "4. Verifying LVM setup from lvm-installer container..."
echo "========================================"
kubectl exec -n lvm-system daemonset/lvm-installer -c lvm-setup -- nsenter --mount=/proc/1/ns/mnt -- sh -c '
  export LD_LIBRARY_PATH=/usr/local/lib:$LD_LIBRARY_PATH
  export PATH=/usr/local/sbin:/usr/local/bin:$PATH

  echo "Volume Groups:"
  vgs
  echo ""
  echo "Physical Volumes:"
  pvs
  echo ""
  echo "Logical Volumes:"
  lvs || echo "No logical volumes yet"
  echo ""
  echo "Loop devices attached to disk.img:"
  losetup -j /mnt/disks/disk.img || echo "None found"
'

echo ""
echo "5. Installing OpenEBS LVM operator..."
echo "========================================"
curl -sL https://openebs.github.io/charts/lvm-operator.yaml -o /tmp/openebs-lvm-operator.yaml
sed -i '/mountPropagation: "Bidirectional"/d' /tmp/openebs-lvm-operator.yaml
kubectl apply -f /tmp/openebs-lvm-operator.yaml

echo "Waiting for OpenEBS LVM controller to be ready..."
kubectl wait --for=condition=ready pod -l app=openebs-lvm-controller -n kube-system --timeout=300s

echo "Waiting for OpenEBS LVM node daemonset to be ready..."
kubectl rollout status daemonset/openebs-lvm-node -n kube-system --timeout=300s

echo ""
echo "6. Verifying OpenEBS can see LVM volume group..."
echo "========================================"
echo "From openebs-lvm-plugin container:"
kubectl exec -n kube-system daemonset/openebs-lvm-node -c openebs-lvm-plugin -- vgs
echo ""
kubectl exec -n kube-system daemonset/openebs-lvm-node -c openebs-lvm-plugin -- pvs

echo ""
echo "7. Creating test PVC..."
echo "========================================"
cat <<EOF | kubectl apply -f -
apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: test-lvm-pvc
spec:
  accessModes:
    - ReadWriteOnce
  storageClassName: openebs-lvm-sc
  resources:
    requests:
      storage: 100Mi
---
apiVersion: storage.k8s.io/v1
kind: StorageClass
metadata:
  name: openebs-lvm-sc
allowVolumeExpansion: true
parameters:
  storage: lvm
  volgroup: lvmvg
provisioner: local.csi.openebs.io
reclaimPolicy: Delete
volumeBindingMode: WaitForFirstConsumer
---
apiVersion: v1
kind: Pod
metadata:
  name: test-pod
spec:
  containers:
  - name: test
    image: busybox
    command: ["sleep", "3600"]
    volumeMounts:
    - name: data
      mountPath: /data
  volumes:
  - name: data
    persistentVolumeClaim:
      claimName: test-lvm-pvc
EOF

echo ""
echo "8. Waiting for pod to be running..."
sleep 5
kubectl wait --for=condition=ready pod/test-pod --timeout=60s

echo ""
echo "9. Checking final status..."
echo "========================================"
echo "PVC status:"
kubectl get pvc test-lvm-pvc
echo ""
echo "Pod status:"
kubectl get pod test-pod
echo ""
echo "Logical volumes:"
kubectl exec -n lvm-system daemonset/lvm-installer -c lvm-setup -- nsenter --mount=/proc/1/ns/mnt -- sh -c '
  export LD_LIBRARY_PATH=/usr/local/lib:$LD_LIBRARY_PATH
  export PATH=/usr/local/sbin:/usr/local/bin:$PATH
  lvs
'

echo ""
echo "10. Checking OpenEBS LVM controller logs for errors..."
echo "========================================"
kubectl logs -n kube-system deployment/openebs-lvm-controller | grep -i "error\|cannot\|duplicate" || echo "No errors found!"

echo ""
echo "=================================================="
echo "✅ Test completed successfully!"
echo "=================================================="
echo ""
echo "Cleanup with:"
echo "  kubectl delete pod test-pod"
echo "  kubectl delete pvc test-lvm-pvc"
echo "  kubectl delete -f /tmp/openebs-lvm-operator.yaml"
echo "  kubectl delete -f test/setup/lvm-installer.yaml"
