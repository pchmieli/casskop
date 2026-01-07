# Storage Migration Diagnostic Scripts

## show-migration-diagnostics.sh

Display storage migration diagnostics for a specific Cassandra pod, including token ranges and nodetool status before/after migration with visual diffs.

### Features

- **Auto-detection**: Automatically detects namespace, cluster name, and DC-rack from pod labels
- **Color-coded output**: Easy-to-read colored terminal output
- **Diff comparison**: Shows side-by-side comparison of token ranges and nodetool status
- **Change summary**: Counts added/removed lines in diffs
- **Error handling**: Clear error messages with troubleshooting hints

### Prerequisites

- `kubectl` configured with access to your cluster
- `jq` installed for JSON parsing
- Bash shell

### Usage

```bash
./show-migration-diagnostics.sh <pod-name> [namespace] [cluster-name] [dc-rack]
```

### Examples

**Basic usage** (auto-detects all parameters from pod):
```bash
./show-migration-diagnostics.sh cassandra-cluster-dc1-rack1-0
```

**Specify namespace**:
```bash
./show-migration-diagnostics.sh cassandra-cluster-dc1-rack1-0 prod-cassandra
```

**Full specification**:
```bash
./show-migration-diagnostics.sh cassandra-cluster-dc1-rack1-0 prod-cassandra cassandra-cluster dc1-rack1
```

### Output

The script displays:

1. **Migration Status**
   - Whether the pod has been migrated
   - Old pod IP and host ID

2. **Token Ranges Comparison**
   - Token ranges before migration
   - Token ranges after migration
   - Unified diff showing changes
   - Count of added/removed lines

3. **Nodetool Status Comparison**
   - Cluster status before migration
   - Cluster status after migration
   - Unified diff showing node state changes
   - Count of added/removed lines

### Example Output

```
========================================
Storage Migration Diagnostics
========================================
Pod:       cassandra-cluster-dc1-rack1-0
Namespace: prod-cassandra
Cluster:   cassandra-cluster
DC-Rack:   dc1-rack1
========================================

Migration Status:
  Migrated:     Yes
  Old Pod IP:   10.244.1.15
  Old Host ID:  a3b5c7d9-e1f2-4a5b-8c9d-0e1f2a3b4c5d

═══════════════════════════════════════
Token Ranges Comparison
═══════════════════════════════════════

Diff (- before, + after):
--- /tmp/tmp.xyz/token_before.txt
+++ /tmp/tmp.xyz/token_after.txt
@@ -5,7 +5,7 @@
-1234567890       10.244.1.15
+1234567890       10.244.1.42
...

Added lines:   12
Removed lines: 12

═══════════════════════════════════════
Nodetool Status Comparison
═══════════════════════════════════════

Diff (- before, + after):
--- /tmp/tmp.xyz/status_before.txt
+++ /tmp/tmp.xyz/status_after.txt
@@ -2,7 +2,7 @@
-UN  10.244.1.15   a3b5c7d9-e1f2-4a5b-8c9d-0e1f2a3b4c5d
+UN  10.244.1.42   a3b5c7d9-e1f2-4a5b-8c9d-0e1f2a3b4c5d
...

Added lines:   1
Removed lines: 1
```

### Interpreting Results

#### Token Ranges

- **Identical token ranges** (no changes): ✅ Perfect! Tokens preserved as expected
- **Same tokens, different IPs**: ✅ Expected - pod got new IP but kept tokens
- **Different tokens**: ⚠️ Investigate - token ranges changed during migration

#### Nodetool Status

- **Old IP → New IP** for same host ID: ✅ Expected behavior
- **Status changed DN → UN**: ✅ Node successfully joined cluster
- **New nodes appeared**: ⚠️ Check if expected or investigate
- **Nodes disappeared**: ⚠️ Investigate missing nodes

### Troubleshooting

**Error: "No migration state found"**
- Pod hasn't been migrated yet
- Migration has been finalized (state cleaned up)
- Wrong pod name or DC-rack specified

**Error: "Could not find pod"**
- Pod doesn't exist
- Wrong namespace
- Typo in pod name

**Warning: "No diagnostics captured yet"**
- Migration in progress
- Diagnostics capture failed (check operator logs)
- Pod not ready when diagnostics were attempted

### Data Source

The script fetches data from the CassandraCluster CRD status:
```
.status.cassandraRackStatus["<dc-rack>"].storageMigrationState.pods["<pod-name>"]
```

Fields used:
- `tokenRangesBeforeMigration`
- `tokenRangesAfterMigration`
- `nodetoolStatusBeforeMigration`
- `nodetoolStatusAfterMigration`
- `oldPodIp`
- `oldHostId`
- `migrated`

### Integration with Migration Process

Diagnostics are automatically captured during storage migration:
1. **Before Migration**: After recording old pod IP/host ID, before pod deletion
2. **After Migration**: After pod becomes Ready and UN, before repair

### Advanced Usage

**Monitor migration in real-time**:
```bash
watch -n 5 './show-migration-diagnostics.sh cassandra-cluster-dc1-rack1-0'
```

**Extract raw data for analysis**:
```bash
kubectl get cassandraclusters.db.orange.com cassandra-cluster -n prod-cassandra \
  -o jsonpath='{.status.cassandraRackStatus["dc1-rack1"].storageMigrationState}' | jq
```

**Check all pods in a rack**:
```bash
for pod in $(kubectl get pods -n prod-cassandra -l dc-rack=dc1-rack1 -o name); do
  echo "=== $pod ==="
  ./show-migration-diagnostics.sh $(basename $pod)
done
```

### See Also

- [STORAGE_MIGRATION_POD_TEMPLATE_IMPLEMENTATION.md](../conspectus/STORAGE_MIGRATION_POD_TEMPLATE_IMPLEMENTATION.md) - Implementation details
- [STORAGE_MIGRATION_TESTING_CHECKLIST.md](../conspectus/STORAGE_MIGRATION_TESTING_CHECKLIST.md) - Testing procedures
- [RACK_REPLACEMENT_STRATEGY.md](../conspectus/RACK_REPLACEMENT_STRATEGY.md) - Migration strategies
