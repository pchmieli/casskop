#!/bin/bash

# Script to extract pod migration timestamps from log file
# Creates a table with: podName, podTemplateDumped, podReady, nodeUN, nodeMigrated

LOG_FILE="$1"

if [ -z "$LOG_FILE" ] || [ ! -f "$LOG_FILE" ]; then
    echo "Usage: $0 <log_file>"
    echo "Error: Log file not found or not specified: $LOG_FILE"
    exit 1
fi

# Temporary files for intermediate results
TMP_DUMP=$(mktemp)
TMP_READY=$(mktemp)
TMP_UN=$(mktemp)
TMP_MIGRATED=$(mktemp)

# Clean up temp files on exit
trap "rm -f $TMP_DUMP $TMP_READY $TMP_UN $TMP_MIGRATED" EXIT

# Extract pod template dump timestamps (first occurrence per pod)
grep "Dumped pod template from actual pod" "$LOG_FILE" | \
    sed 's/\[[0-9]*m//g' | \
    awk -F'] ' '{
        # Extract timestamp and pod name
        ts_part = $2;
        split(ts_part, a, " ");
        timestamp = a[1] " " a[2] " " a[3];

        # Extract pod name from message
        msg = $0;
        match(msg, /cassandra-cluster-[^ ]*-[0-9]+/);
        pod = substr(msg, RSTART, RLENGTH);

        if (pod != "" && !(pod in seen)) {
            seen[pod] = 1;
            print pod, timestamp;
        }
    }' > $TMP_DUMP

# Extract pod ready timestamps (last "Waiting for pod" = pod became ready)
grep "Waiting for pod" "$LOG_FILE" | grep "to become Ready" | \
    sed 's/\[[0-9]*m//g' | \
    awk -F'] ' '{
        # Extract timestamp
        ts_part = $2;
        split(ts_part, a, " ");
        timestamp = a[1] " " a[2] " " a[3];

        # Extract pod name
        msg = $0;
        match(msg, /cassandra-cluster-[^ ]*-[0-9]+/);
        pod = substr(msg, RSTART, RLENGTH);

        if (pod != "") {
            pods[pod] = timestamp;
        }
    }
    END {
        for (pod in pods) {
            print pod, pods[pod];
        }
    }' > $TMP_READY

# Extract node UN timestamps (first "is Ready and UN with new Host ID")
grep "is Ready and UN with new Host ID" "$LOG_FILE" | \
    sed 's/\[[0-9]*m//g' | \
    awk -F'] ' '{
        # Extract timestamp
        ts_part = $2;
        split(ts_part, a, " ");
        timestamp = a[1] " " a[2] " " a[3];

        # Extract pod name
        msg = $0;
        match(msg, /cassandra-cluster-[^ ]*-[0-9]+/);
        pod = substr(msg, RSTART, RLENGTH);

        if (pod != "" && !(pod in seen)) {
            seen[pod] = 1;
            print pod, timestamp;
        }
    }' > $TMP_UN

# Extract pod migrated timestamps (last occurrence per pod)
grep "successfully migrated" "$LOG_FILE" | \
    sed 's/\[[0-9]*m//g' | \
    awk -F'] ' '{
        # Extract timestamp
        ts_part = $2;
        split(ts_part, a, " ");
        timestamp = a[1] " " a[2] " " a[3];

        # Extract pod name
        msg = $0;
        match(msg, /cassandra-cluster-[^ ]*-[0-9]+/);
        pod = substr(msg, RSTART, RLENGTH);

        if (pod != "") {
            pods[pod] = timestamp;
        }
    }
    END {
        for (pod in pods) {
            print pod, pods[pod];
        }
    }' > $TMP_MIGRATED

# Combine all data and output as table
echo "Pod Migration Timestamps"
echo "========================================================================================================"
printf "%-35s | %-20s | %-20s | %-20s | %-20s\n" "Pod Name" "Template Dumped" "Pod Ready" "Node UN" "Migrated"
echo "--------------------------------------------------------------------------------------------------------"

# Get all unique pod names
all_pods=$(cat $TMP_DUMP $TMP_READY $TMP_UN $TMP_MIGRATED | awk '{print $1}' | sort -u)

for pod in $all_pods; do
    dump_ts=$(grep "^$pod " $TMP_DUMP | head -1 | cut -d' ' -f2-)
    ready_ts=$(grep "^$pod " $TMP_READY | head -1 | cut -d' ' -f2-)
    un_ts=$(grep "^$pod " $TMP_UN | head -1 | cut -d' ' -f2-)
    migrated_ts=$(grep "^$pod " $TMP_MIGRATED | head -1 | cut -d' ' -f2-)

    # Use "-" for missing timestamps
    [ -z "$dump_ts" ] && dump_ts="-"
    [ -z "$ready_ts" ] && ready_ts="-"
    [ -z "$un_ts" ] && un_ts="-"
    [ -z "$migrated_ts" ] && migrated_ts="-"

    printf "%-35s | %-20s | %-20s | %-20s | %-20s\n" "$pod" "$dump_ts" "$ready_ts" "$un_ts" "$migrated_ts"
done

echo "========================================================================================================"
