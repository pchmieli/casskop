CLUSTER="k3s-default"

# Delete existing cluster
k3d cluster delete "$CLUSTER"

# Create a directory for sharing
mkdir -p /mnt/k3d-shared
chmod 777 /mnt/k3d-shared/

# Create required directories on host
mkdir -p /var/lib/kubelet
chmod 755 /var/lib/kubelet
# Make /var/lib/kubelet a shared mount on the host
mount --make-rshared /var/lib/kubelet 2>/dev/null || true

mkdir -p /tmp/k3d-longhorn
chmod 777 /tmp/k3d-longhorn

# Create cluster with shared mount propagation
GW_IP="$(ip route | awk '/default/ {print $3; exit}')"
echo GW_IP=$GW_IP
k3d cluster create "$CLUSTER" \
--image rancher/k3s:v1.24.13-k3s1 \
--api-port "${GW_IP}:6443" \
--k3s-arg "--tls-san=${GW_IP}@server:0" \
--agents 2 \
--k3s-arg '--disable=traefik@server:*' \
--no-rollback

# Wait for cluster
kubectl wait --for=condition=Ready nodes --all --timeout=60s

# Configure mount propagation on all nodes
for node in k3d-k3s-default-server-0 k3d-k3s-default-agent-0 k3d-k3s-default-agent-1; do
  echo "Configuring mount propagation on $node..."
  docker exec $node sh -c '
    # Make root filesystem shared
    mount --make-rshared /

    # Create directory and bind-mount it to itself, then make it shared
    mkdir -p /var/lib/longhorn
    mount --bind /var/lib/longhorn /var/lib/longhorn
    mount --make-rshared /var/lib/longhorn
'
done

# Verify mount propagation with alternative commands
for node in k3d-k3s-default-server-0 k3d-k3s-default-agent-0 k3d-k3s-default-agent-1; do
  echo "=== $node ==="
  docker exec $node sh -c "mount | grep /var/lib/longhorn"
  # Or check if it's listed as a mount point
  docker exec $node sh -c "cat /proc/mounts | grep longhorn"
done

# Check mount propagation flags from /proc/self/mountinfo
for node in k3d-k3s-default-server-0 k3d-k3s-default-agent-0 k3d-k3s-default-agent-1; do
  echo "=== $node ==="
  docker exec $node sh -c "cat /proc/self/mountinfo | grep longhorn"
done

#k3d cluster create "$CLUSTER"   --image rancher/k3s:v1.24.13-k3s1   --api-port "${GW_IP}:6443"   --k3s-arg "--tls-san=${GW_IP}@server:0"



# Deploy iSCSI support
kubectl apply -f longhorn2/storage-installer.yaml

# Wait for iSCSI pods to be ready
kubectl -n kube-system wait --for=condition=Ready pod -l app=storage-installer --timeout=60s

# Verify iscsid is running
kubectl -n kube-system logs -l app=storage-installer

# Verify iscsid is running inside the pod
kubectl -n kube-system exec -it $(kubectl -n kube-system get pod -l app=storage-installer -o jsonpath='{.items[0].metadata.name}') -- ps aux | grep -E "iscsid|rpcbind"





# Add Longhorn Helm repository
helm repo add longhorn https://charts.longhorn.io
helm repo update

# Uninstall existing Longhorn
helm uninstall longhorn -n longhorn-system

# Install Longhorn
helm install longhorn longhorn/longhorn \
--namespace longhorn-system \
--create-namespace \
--version 1.7.2 \
--set defaultSettings.v2DataEngine=true \
--set csi.kubeletRootDir=/var/lib/kubelet \
--set defaultSettings.defaultDataEngine=v2 \
-f longhorn2/values.yaml

# Get the DaemonSet spec to see all volume mounts
[//]: # (kubectl -n longhorn-system get daemonset longhorn-manager -o json > /tmp/longhorn-manager.yaml)

# Edit and remove all mountPropagation fields

[//]: # (kubectl -n longhorn-system get daemonset longhorn-manager -o json | \)
[//]: # (    jq 'del&#40;.spec.template.spec.containers[].volumeMounts[].mountPropagation&#41;' > \)
[//]: # (    /tmp/longhorn-manager2.yaml)
[//]: # (kubectl apply -f /tmp/longhorn-manager2.yaml)

# Wait for rollout
kubectl -n longhorn-system rollout status daemonset/longhorn-manager

# Check pods
kubectl -n longhorn-system get pods -l app=longhorn-manager

# Wait for Longhorn to be ready
kubectl -n longhorn-system rollout status deployment/longhorn-driver-deployer
kubectl -n longhorn-system rollout status daemonset/longhorn-manager