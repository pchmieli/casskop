#!/bin/bash

for rack in 1 2 3; do
  for pod in 0 1 2; do
    pod_name="cassandra-cluster-dc1-rack${rack}-${pod}"
    echo "Starting cleanup on ${pod_name}"
    kubectl exec -n prod-doaks-cassandra -c cassandra ${pod_name} -- bash -c "nohup nodetool cleanup > /var/lib/cassandra/cleanup.log 2>&1 &"
  done
done

echo "Cleanup started on all pods"