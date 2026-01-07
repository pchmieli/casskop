#!/bin/bash

echo "Checking cleanup status on all pods..."
echo "========================================="

for rack in 1 2 3; do
  for pod in 0 1 2; do
    pod_name="cassandra-cluster-dc1-rack${rack}-${pod}"
    echo -n "${pod_name}: "

    if kubectl exec -n prod-doaks-cassandra -c cassandra ${pod_name} -- ps aux | grep -q "[c]leanup"; then
      echo "RUNNING"
    else
      echo "FINISHED"
    fi
  done
done