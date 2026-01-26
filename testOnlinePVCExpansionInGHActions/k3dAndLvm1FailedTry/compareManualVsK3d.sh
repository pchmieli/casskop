A="k3s-manual"

# k3d cluster params
CLUSTER="k3s-default"
IMAGE="k3s-with-lvm2:v1.24.13-k3s1"

mounts() {
  docker inspect "$1" --format '{{range .Mounts}}{{println .Source "->" .Destination "mode=" .Mode "rw=" .RW "prop=" .Propagation}}{{end}}' | sort
}

hc() {
  docker inspect "$1" --format \
'Privileged={{.HostConfig.Privileged}}
PidMode={{.HostConfig.PidMode}} IpcMode={{.HostConfig.IpcMode}}
CgroupnsMode={{.HostConfig.CgroupnsMode}} CgroupParent={{.HostConfig.CgroupParent}}
ReadonlyRootfs={{.HostConfig.ReadonlyRootfs}}
SecurityOpt={{json .HostConfig.SecurityOpt}}
CapAdd={{json .HostConfig.CapAdd}} CapDrop={{json .HostConfig.CapDrop}}
Tmpfs={{json .HostConfig.Tmpfs}}
Binds={{json .HostConfig.Binds}}
Mounts={{json .HostConfig.Mounts}}
RestartPolicy={{json .HostConfig.RestartPolicy}}'
}

echo "== A (manual) exists? =="
docker inspect "$A" >/dev/null

echo "== A: quick startup (entrypoint+cmd) =="
docker inspect "$A" --format 'A Entrypoint={{json .Config.Entrypoint}} Cmd={{json .Config.Cmd}}'
echo

echo "== A: env (sorted) =="
docker inspect "$A" --format '{{range .Config.Env}}{{println .}}{{end}}' | sort
echo

echo "== A: mounts (sorted) =="
mounts "$A"
echo

echo "== A: host config =="
hc "$A"
echo

echo "== A: network/ports =="
docker inspect "$A" --format 'A NetworkMode={{.HostConfig.NetworkMode}} Ports={{json .NetworkSettings.Ports}}'
echo

echo "== A: k3s process argv (if running) =="
docker exec "$A" sh -lc 'ps auxww | grep -E "[k]3s( |$)" || true' 2>/dev/null || true
echo

echo "== A: recent logs =="
docker logs --tail=200 "$A" 2>/dev/null || true
echo
echo "============================================================"
echo

#cleanup() {
#  k3d cluster delete "$CLUSTER" >/dev/null 2>&1 || true
#}
#trap cleanup EXIT

#echo "== Create cluster via k3d (then inspect B) =="
#k3d cluster create "$CLUSTER" --image "$IMAGE" --no-rollback >/dev/null

B="k3d-${CLUSTER}-server-0"
docker inspect "$B" >/dev/null

echo "== B (k3d): quick startup (entrypoint+cmd) =="
docker inspect "$B" --format 'B Entrypoint={{json .Config.Entrypoint}} Cmd={{json .Config.Cmd}}'
echo

echo "== Diff: env (A vs B) =="
comm -3 \
  <(docker inspect "$A" --format '{{range .Config.Env}}{{println .}}{{end}}' | sort) \
  <(docker inspect "$B" --format '{{range .Config.Env}}{{println .}}{{end}}' | sort) \
  || true
echo

echo "== Diff: mounts (A vs B) =="
comm -3 <(mounts "$A") <(mounts "$B") || true
echo

echo "== Diff: host config (A vs B) =="
diff -u <(hc "$A") <(hc "$B") || true
echo

echo "== B: network/ports =="
docker inspect "$B" --format 'B NetworkMode={{.HostConfig.NetworkMode}} Ports={{json .NetworkSettings.Ports}}'
echo

echo "== B: k3s process argv (if running) =="
docker exec "$B" sh -lc 'ps auxww | grep -E "[k]3s( |$)" || true' 2>/dev/null || true
echo

echo "== B: recent logs =="
docker logs --tail=200 "$B" 2>/dev/null || true
