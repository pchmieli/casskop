#!/bin/bash

# Script to calculate phase durations from pod migration timestamps
# Reads the output from measure_pod_times.sh and calculates durations

if [ $# -lt 1 ]; then
    echo "Usage: $0 <pod_times_output_file>"
    exit 1
fi

INPUT_FILE="$1"

if [ ! -f "$INPUT_FILE" ]; then
    echo "Error: File not found: $INPUT_FILE"
    exit 1
fi

# Function to convert timestamp to seconds using awk (more reliable)
timestamp_to_seconds() {
    local ts="$1"
    # Use awk to parse and calculate in one go
    echo "$ts" | awk '{
        # Parse: "Feb DD HH:MM:SS.mmm"
        day = $2;
        split($3, time, ":");
        split(time[3], sec, ".");
        hours = time[1];
        mins = time[2];
        secs = sec[1];
        millis = sec[2];

        total = day * 86400 + hours * 3600 + mins * 60 + secs;
        if (millis != "") total = total + millis / 1000;
        printf "%.3f", total;
    }'
}

# Function to format duration
format_duration() {
    local total_secs="$1"
    echo "$total_secs" | awk '{
        mins = int($1 / 60);
        secs = $1 - (mins * 60);
        printf "%dm %.1fs", mins, secs;
    }'
}

echo "Pod Migration Phase Durations"
echo "===================================================================================================="
printf "%-35s | %-12s | %-12s | %-12s | %-12s\n" "Pod Name" "Boot Time" "Join Time" "Finalize" "Total"
echo "----------------------------------------------------------------------------------------------------"

# Skip header lines and process data
tail -n +4 "$INPUT_FILE" | while IFS='|' read -r pod_name template_dumped pod_ready node_un migrated _; do
    # Trim whitespace
    pod_name=$(echo "$pod_name" | xargs)
    template_dumped=$(echo "$template_dumped" | xargs)
    pod_ready=$(echo "$pod_ready" | xargs)
    node_un=$(echo "$node_un" | xargs)
    migrated=$(echo "$migrated" | xargs)

    # Skip separator lines, empty lines, and header repeats
    [ -z "$pod_name" ] && continue
    [ "$pod_name" = "Pod Name" ] && continue
    [[ "$pod_name" == *"="* ]] && continue
    [[ "$pod_name" =~ ^-+$ ]] && continue

    # Calculate durations if timestamps are available (not "-")
    if [ "$template_dumped" != "-" ] && [ "$pod_ready" != "-" ]; then
        t1=$(timestamp_to_seconds "$template_dumped")
        t2=$(timestamp_to_seconds "$pod_ready")
        diff=$(echo "$t1 $t2" | awk '{printf "%.3f", $2 - $1}')
        boot_time=$(format_duration "$diff")
    else
        boot_time="-"
    fi

    if [ "$pod_ready" != "-" ] && [ "$node_un" != "-" ]; then
        t2=$(timestamp_to_seconds "$pod_ready")
        t3=$(timestamp_to_seconds "$node_un")
        diff=$(echo "$t2 $t3" | awk '{printf "%.3f", $2 - $1}')
        join_time=$(format_duration "$diff")
    else
        join_time="-"
    fi

    if [ "$node_un" != "-" ] && [ "$migrated" != "-" ]; then
        t3=$(timestamp_to_seconds "$node_un")
        t4=$(timestamp_to_seconds "$migrated")
        diff=$(echo "$t3 $t4" | awk '{printf "%.3f", $2 - $1}')
        finalize_time=$(format_duration "$diff")
    else
        finalize_time="-"
    fi

    if [ "$template_dumped" != "-" ] && [ "$migrated" != "-" ]; then
        t1=$(timestamp_to_seconds "$template_dumped")
        t4=$(timestamp_to_seconds "$migrated")
        diff=$(echo "$t1 $t4" | awk '{printf "%.3f", $2 - $1}')
        total_time=$(format_duration "$diff")
    else
        total_time="-"
    fi

    printf "%-35s | %-12s | %-12s | %-12s | %-12s\n" "$pod_name" "$boot_time" "$join_time" "$finalize_time" "$total_time"
done

echo "===================================================================================================="
echo ""
echo "Phase Definitions:"
echo "  Boot Time:     Template Dumped → Pod Ready (PVC creation, pod start)"
echo "  Join Time:     Pod Ready → Node UN (Cassandra join, data streaming)"
echo "  Finalize Time: Node UN → Migrated (Repair, verification)"
echo "  Total:         Template Dumped → Migrated (Complete pod migration)"
