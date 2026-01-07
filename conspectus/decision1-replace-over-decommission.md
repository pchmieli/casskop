# TODO: decisions, #1 replace over decommission
(algorithm for node by node replacement with preserving tokens)

Use the replace feature instead of decommission:

1. Per Node:
   1. Delete pod N from rack A (ensure it won't restart)
   2. Delete its PVC
   3. Create new PVC with new storage class/capacity
   4. Start Cassandra with: -Dcassandra.replace_address_first_boot=OLD_IP
   5. Cassandra streams only that node's data (preserves token ranges)
   6. No cluster-wide token rebalancing happens
   7. Move to next node
2. Why This is Better:
   1. Token ranges stay exactly the same
   2. Only streams data for that specific node
   3. No cascading token recalculations
   4. Much less data movement overall
3. For 3-Rack Scenario:
   1. Replace 1 node at a time across all racks (round-robin)
   2. Or replace all nodes in one rack sequentially, then move to next rack
   3. Both work because replacement preserves tokens

