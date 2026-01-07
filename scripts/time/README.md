# Migration Time Measurement Scripts

This directory contains scripts to analyze Cassandra storage migration logs and extract timing information.

## Scripts

### 1. calculate-times.sh

Main orchestration script that runs all measurements and generates output files.

**Usage:**
```bash
./calculate-times.sh -d <output_directory> -l <log_file> -p <prefix>
```

**Parameters:**
- `-d` : Output directory where results will be saved
- `-l` : Path to the log file to analyze
- `-p` : Prefix for output file names

**Example:**
```bash
./calculate-times.sh -d ./results -l /path/to/completeLog.log -p test1
```

**Output Files:**
- `{prefix}_pod_times.txt` - Detailed pod migration timestamps table
- `{prefix}_phase_durations.txt` - Phase duration calculations
- `{prefix}_pod_switch_times.txt` - Pod switch/transition times
- `{prefix}_summary.txt` - Average metrics and performance summary ✨ **NEW**

### 2. measure_rack_times.sh

Measures the overall migration time for each rack.

**Usage:**
```bash
./measure_rack_times.sh <log_file>
```

**Output:**
- Displays start time, end time, and duration for each rack (rack1, rack2, rack3)
- Start time: First "Storage migration should be started" message
- End time: Last "pods migrated successfully" message

**Example Output:**
```
dc1-rack1:
  Start: Feb 17 22:38:35.340
  End:   Feb 17 23:43:24.470
  Duration: 1h 4m 49.130s
```

### 3. measure_pod_times.sh

Extracts detailed timestamps for each pod's migration phases.

**Usage:**
```bash
./measure_pod_times.sh <log_file>
```

**Output:**
A table with the following columns:
- **Pod Name**: Name of the pod being migrated
- **Template Dumped**: When pod template was dumped (first occurrence)
- **Pod Ready**: When pod became ready (last "Waiting for pod" message)
- **Node UN**: When node became UN/Normal (last "not UN yet" message)
- **Migrated**: When pod migration completed (last "successfully migrated" message)

**Example Output:**
```
Pod Migration Timestamps
========================================================================================================
Pod Name                            | Template Dumped      | Pod Ready            | Node UN              | Migrated            
--------------------------------------------------------------------------------------------------------
cassandra-cluster-dc1-rack1-0       | Feb 17 22:38:43.819  | Feb 17 22:55:43.640  | Feb 17 23:08:19.292  | Feb 17 23:08:46.991
cassandra-cluster-dc1-rack1-1       | Feb 17 23:08:51.648  | Feb 17 23:22:22.039  | Feb 17 23:22:26.092  | Feb 17 23:22:54.311
...
```

### 4. calculate_phase_durations.sh

Calculates the duration of each migration phase for every pod.

**Usage:**
```bash
./calculate_phase_durations.sh <pod_times_output_file>
```

**Input:**
Takes the output file from `measure_pod_times.sh` (or the `{prefix}_pod_times.txt` file)

**Output:**
A table showing duration for each migration phase:
- **Boot Time**: Template Dumped → Pod Ready (PVC creation, pod scheduling, container startup)
- **Join Time**: Pod Ready → Node UN (Cassandra startup, cluster join, data streaming)
- **Finalize Time**: Node UN → Migrated (Repair, verification, status updates)
- **Total**: Complete pod migration time

**Example Output:**
```
Pod Migration Phase Durations
====================================================================================================
Pod Name                            | Boot Time    | Join Time    | Finalize     | Total       
----------------------------------------------------------------------------------------------------
cassandra-cluster-dc1-rack1-0       | 16m 59.8s    | 12m 35.7s    | 0m 27.7s     | 30m 3.2s
cassandra-cluster-dc1-rack1-1       | 13m 30.4s    | 0m 4.1s      | 0m 28.2s     | 14m 2.7s
...
```

**Example Usage:**
```bash
# First, generate the pod times
./measure_pod_times.sh /path/to/log.log > pod_times.txt

# Then calculate phase durations
./calculate_phase_durations.sh pod_times.txt
```

### 5. calculate_pod_switch_times.sh

Calculates the time between pod migrations (rack finalization / pod switch time).

**Usage:**
```bash
./calculate_pod_switch_times.sh <pod_times_output_file>
```

**Input:**
Takes the output file from `measure_pod_times.sh` (or the `{prefix}_pod_times.txt` file)

**Output:**
A table showing the transition time between consecutive pod migrations:
- **From Pod**: Pod that just completed migration
- **Migrated Time**: When that pod finished migrating
- **To Pod**: Next pod starting migration
- **Template Dump Time**: When next pod's template was dumped
- **Switch Time**: Time difference between these events

**What It Measures:**
- **Within-rack switches**: Time between pods in the same rack (typically seconds)
- **Between-rack switches**: Time between last pod of one rack and first pod of next rack (typically minutes)
- Includes rack finalization, status updates, and preparation for next pod

**Example Output:**
```
Rack Finalization / Pod Switch Times
====================================================================================================
From Pod (Migrated)                 | Migrated Time        | To Pod (Template Dump)              | Template Dump Time   | Switch Time
----------------------------------------------------------------------------------------------------
cassandra-cluster-dc1-rack1-0       | Feb 17 23:08:46.991  | cassandra-cluster-dc1-rack1-1       | Feb 17 23:08:51.648  | 0m 4.7s
cassandra-cluster-dc1-rack1-1       | Feb 17 23:22:54.311  | cassandra-cluster-dc1-rack1-2       | Feb 17 23:23:01.572  | 0m 7.3s
cassandra-cluster-dc1-rack1-2       | Feb 17 23:38:27.570  | cassandra-cluster-dc1-rack2-0       | Feb 17 23:44:03.439  | 5m 35.9s
...
```

**Example Usage:**
```bash
# Standalone
./calculate_pod_switch_times.sh pod_times.txt

# Or as part of full analysis
./calculate-times.sh -d ./results -l /path/to/log.log -p test1
# Output will be in results/test1_pod_switch_times.txt
```

## Log Message Patterns

The scripts search for the following log patterns:

1. **Rack Migration Start**: `Storage migration should be started`
2. **Rack Migration Complete**: `pods migrated successfully, finalizing migration action`
3. **Pod Template Dump**: `Dumped pod template from actual pod`
4. **Pod Ready**: `Waiting for pod <name> to become Ready`
5. **Node UN**: `found in status but not UN yet`
6. **Pod Migrated**: `Pod <name> successfully migrated`

## Notes

- Timestamps are extracted with millisecond precision
- ANSI color codes in logs are automatically stripped
- For "Pod Ready" and "Node UN", the scripts use heuristics:
  - **Pod Ready**: Last occurrence of "Waiting for pod" means the pod became ready
  - **Node UN**: Last occurrence of "not UN yet" means the node became UN
- Missing timestamps are displayed as "-" in the output table

## Requirements

- bash
- Standard Unix tools: grep, sed, awk, cut, sort
- Log file must be in the casskop log format with timestamps

## Example Full Workflow

```bash
# Create output directory
mkdir -p /home/user/migration_results

# Run complete analysis
cd /home/adlex/workspaces/casskop/scripts/time
./calculate-times.sh \
  -d /home/user/migration_results \
  -l /path/to/completeLog.log \
  -p migration_test_1

# View results
cat /home/user/migration_results/migration_test_1_pod_times.txt
```

## Troubleshooting

- If no output is generated, check that the log file contains the expected message patterns
- Verify that the log file uses the standard casskop format with timestamps
- Ensure the script has execute permissions: `chmod +x *.sh`
