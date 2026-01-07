# TODO: feature roadmap
1. simplest approach - recreate whole rack with new storage class and/or capacity
    1. test it on 1 node per rack first
    2. then properly, with 2-3 nodes per rack (check potential issues with pvcs being assigned/pvs to specific nodes, some nodes may disappear in the meantime)
       1. should join_ring=false be set? will be revealed in tests with larger number of nodes per rack
2. generate some data (small amount) + its verification after recreating the rack -> find a way to test that exactly whole data is replicated on all racks
3. more complex - node by node replacement with preserving tokens (more steps, more checks, more complex logic)
    1. check how it works when there are 1 or more nodes per racks (1 means that whole rack would be off)
4. verify not only data but also token ranges
5. even more complex - mixed approach - recreate part of the rack, replace part of the rack node by node
6. performance testing - measure time taken for each approach, amount of data transferred over the network, impact on cluster performance during the operation


# Design decisions:
1. [ToBeMade] should we handle changing migration parameter during operation
2. [ToBeMade] what migration strategies should we support (1 pod at once, x pods at once, whole rack at once?)



# TODO: important issues to solve
1. how to verify that pod is correctly joint and we can proceed with next ones?
2. how to handle situation when old and new pod has the same IP?
- reproduce this scenario without other changes, check if it really makes problem
3. something is wrong, maybe the last_applied handling was not transferred as it was in storage_upsize
```
[statefulset.go:152::github.com/cscetbon/casskop/controllers/cassandracluster.logTemplateChange()] Feb 12 15:22:32.430 [D] Generated patch is: {
  "metadata": {
    "annotations": {
      "banzaicloud.com/last-applied": "UEs...AAA"
    }
  }
}
```
4. runbooks for corner cases
- interrupting migration to do an update etc.
5. backup during migration... definitely need to delete backup sts/pods so they don't hold the disks!
6. questions to answer
- how much time to migrate X nodes with Y GB on each node?
- how much disk overhead on each node needed if max load is Y GB?
- how live load affects migration time and necessary disk overhead?

# SUMMARY
## 1 - recreate whole rack
#### problems
- unclear why data is being copied after removing rack1, why should rack2&3 have 1.5 replicas?
- possibly related to the above, migration on 697 cluster hung for several hours (approx. 100G data per node)
    ```
  likely related...
  java.lang.RuntimeException: Not enough space for compaction, estimated sstables = 1, expected write size = 19785334743
  because suddenly accepting 50% of data requires space for compaction

  THIS IS DISQUALIFYING IN PRODUCTION CONTEXT

        OTHER REASONS
            - we only have 2 replicas, if something happens it could be bad
            - we're cutting 1/3 of processing power
  ```
- not enough bulletproof for prod
  - we only have 2 living replicas for some time, if something happens it could be disaster
  - we're cutting 1/3 of processing power
  - combination of two above - one risk intensifies the other
- implementation problems (might be improved)
  - noderemove part is blocking
  - repair handling is uncertain, i.e. monitoring based on parsing some text results (CurrentStreams)
- sometimes data disappears... maybe errors somewhere in repair implementation...
   - but it was a weird setup, a few files deleted manually because there was a compaction problem, might have destroyed data consistency and that's why the keyspace was lost
