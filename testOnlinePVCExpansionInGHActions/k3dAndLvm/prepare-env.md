# ON WSL:

docker pull ubuntu:22.04
docker run --rm -it --privileged \
-v "$(pwd):/work" \
-v /var/run/docker.sock:/var/run/docker.sock \
-w /work \
ubuntu:22.04 bash


# in container

# Update and install prerequisites
apt-get update
apt-get install -y \
curl \
wget \
apt-transport-https \
ca-certificates \
software-properties-common \
gnupg \
lvm2 \
thin-provisioning-tools \
docker.io \
net-tools \
lsof \
vim

# Install kubectl
curl -LO "https://dl.k8s.io/release/$(curl -L -s https://dl.k8s.io/release/stable.txt)/bin/linux/amd64/kubectl"
chmod +x kubectl
mv kubectl /usr/local/bin/

# Install kind
curl -s https://raw.githubusercontent.com/k3d-io/k3d/main/install.sh | TAG=v5.4.0 bash

# Install Helm v3.8.1 (matching GH Actions)
wget https://get.helm.sh/helm-v3.8.1-linux-amd64.tar.gz
tar -zxvf helm-v3.8.1-linux-amd64.tar.gz
mv linux-amd64/helm /usr/local/bin/
rm -rf linux-amd64 helm-v3.8.1-linux-amd64.tar.gz

# Install kubectl-kuttl v0.11.0 (matching GH Actions)
curl -L https://github.com/kudobuilder/kuttl/releases/download/v0.11.0/kubectl-kuttl_0.11.0_linux_x86_64 -o /usr/local/bin/kubectl-kuttl
chmod +x /usr/local/bin/kubectl-kuttl
alias kuttl=kubectl-kuttl

# Verify versions
helm version
kuttl version