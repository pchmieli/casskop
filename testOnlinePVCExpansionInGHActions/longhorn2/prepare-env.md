#ON WSL:

#docker pull ubuntu:22.04
#docker run --rm -it \
#-v "$(pwd):/work" \
#-v /var/run/docker.sock:/var/run/docker.sock \
#-w /work \
#ubuntu:22.04 bash




apt-get update
apt-get install -y --no-install-recommends ca-certificates curl git jq bash coreutils docker.io
curl -s https://raw.githubusercontent.com/k3d-io/k3d/main/install.sh | TAG=v5.4.0 bash
curl -L https://get.helm.sh/helm-v3.8.1-linux-amd64.tar.gz -o /tmp/helm.tgz
tar -C /tmp -xzf /tmp/helm.tgz
install -m 0755 /tmp/linux-amd64/helm /usr/local/bin/helm
KUTTL_VERSION=0.11.0
curl -L "https://github.com/kudobuilder/kuttl/releases/download/v${KUTTL_VERSION}/kubectl-kuttl_${KUTTL_VERSION}_linux_i386" -o kuttl
chmod u+x kuttl
export PATH="/work:$PATH"

#TODO - dev tools
apt-get install -y vim
apt-get install -y --no-install-recommends iproute2
curl -L "https://dl.k8s.io/release/v1.24.13/bin/linux/amd64/kubectl" -o /usr/local/bin/kubectl
chmod +x /usr/local/bin/kubectl
