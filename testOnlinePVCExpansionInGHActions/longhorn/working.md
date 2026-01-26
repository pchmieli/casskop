apt-get update
apt-get install -y open-iscsi
#systemctl enable --now iscsid
/usr/sbin/iscsid

mkdir -p /var/lib/longhorn
chmod 777 /var/lib/longhorn

k3d cluster create k3s-default \
  --image rancher/k3s:v1.30.2-k3s1 \
  --k3s-arg "--disable=local-storage@server:0" \
  --volume /var/lib/longhorn:/var/lib/longhorn@server:0


# Fix mount propagation
docker exec k3d-k3s-default-server-0 sh -c "mount --make-rshared /"



docker exec k3d-k3s-default-server-0 sh -c '
cd /tmp && \
wget http://dl-cdn.alpinelinux.org/alpine/v3.19/main/x86_64/open-iscsi-2.1.9-r3.apk && \
tar -xzf open-iscsi-2.1.9-r3.apk && \
cp -r usr/* /usr/ && \
cp -r etc/* /etc/ 2>/dev/null || true && \
mkdir -p /var/lock/iscsi /var/lib/iscsi && \
/usr/sbin/iscsid
'



helm repo add longhorn https://charts.longhorn.io
helm repo update
helm install longhorn longhorn/longhorn \
--namespace longhorn-system \
--create-namespace

