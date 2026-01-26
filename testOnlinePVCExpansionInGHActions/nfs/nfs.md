[//]: # (# Run NFS server in Docker &#40;WSL2 compatible&#41;)

[//]: # (docker run -d \)

[//]: # (--name nfs-server \)

[//]: # (--privileged \)

[//]: # (-p 2049:2049 \)

[//]: # (-v /srv/nfs/k3s:/nfs \)

[//]: # (-e SHARED_DIRECTORY=/nfs \)

[//]: # (itsthenetwork/nfs-server-alpine:latest)



# Ensure directory exists with proper permissions
mkdir -p /srv/nfs/k3s
chmod 777 /srv/nfs/k3s


# Run NFS server in Docker on k3d network (WSL2 compatible)
docker run -d \
--name nfs-server \
--privileged \
--network k3d-k3s-default \
-v /srv/nfs/k3s:/nfs \
-e SHARED_DIRECTORY=/nfs \
itsthenetwork/nfs-server-alpine:latest

# Verify NFS server is running
docker ps | grep nfs-server

# Create k3d cluster
k3d cluster create "$CLUSTER"   --image rancher/k3s:v1.24.13-k3s1   --api-port "${GW_IP}:6443"   --k3s-arg "--tls-san=${GW_IP}@server:0"

# Get NFS server container IP

[//]: # (NFS_SERVER_IP=$&#40;docker inspect nfs-server | grep '"IPAddress"' | tail -1 | awk '{print $2}' | tr -d '",'&#41;)
[//]: # (echo "NFS Server IP: $NFS_SERVER_IP")

cat <<EOF > nfs-provisioner-values.yaml
nfs:
  server: $(docker inspect nfs-server | grep '"IPAddress"' | tail -1 | awk '{print $2}' | tr -d '",')
  path: /
  mountOptions:
    - vers=4.2
    - minorversion=2
    - proto=tcp
    - soft
EOF

# Reinstall with correct IP
#helm uninstall nfs-provisioner

[//]: # (helm install nfs-provisioner nfs-subdir-external-provisioner/nfs-subdir-external-provisioner \)
[//]: # (--set nfs.server=$NFS_SERVER_IP \)
[//]: # (--set nfs.path=/nfs \)
[//]: # (--set storageClass.allowVolumeExpansion=true)

# Reinstall with NFSv4 options
helm install nfs-provisioner nfs-subdir-external-provisioner/nfs-subdir-external-provisioner \
-f nfs-provisioner-values.yaml

[//]: # (# Install NFS provisioner)

[//]: # (helm repo add nfs-subdir-external-provisioner https://kubernetes-sigs.github.io/nfs-subdir-external-provisioner/)

[//]: # (helm install nfs-provisioner nfs-subdir-external-provisioner/nfs-subdir-external-provisioner \)

[//]: # (--set nfs.server=host.k3d.internal \)

[//]: # (--set nfs.path=/nfs \)

[//]: # (--set storageClass.allowVolumeExpansion=true)

# Verify
kubectl get storageclass
