#!/bin/bash

# Script to calculate pod switch times (time between pod migration completion and next pod starting)
# This shows the "rack finalization / pod switch time"

if [ $# -lt 2 ]; then
    echo "Usage: $0 <pod_times_output_file> <log_file>"
    exit 1
fi

INPUT_FILE="$1"
LOG_FILE="$2"

if [ ! -f "$INPUT_FILE" ]; then
    echo "Error: Pod times file not found: $INPUT_FILE"
    exit 1
fi

if [ ! -f "$LOG_FILE" ]; then
    echo "Error: Log file not found: $LOG_FILE"
    exit 1
fi

# Function to convert timestamp to seconds using awk
timestamp_to_seconds() {
    local ts="$1"
    echo "$ts" | awk '{
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

echo "Rack Finalization / Pod Switch Times"
echo "===================================================================================================="
echo "Time between pod migration completion and next pod template dump"
echo "===================================================================================================="
printf "%-35s | %-20s | %-35s | %-20s | %-12s\n" "From Pod (Migrated)" "Migrated Time" "To Pod (Template Dump)" "Template Dump Time" "Switch Time"
echo "----------------------------------------------------------------------------------------------------"

# Extract pod data into arrays
declare -a pod_names
declare -a template_times
declare -a migrated_times

# Read and parse the file
tail -n +4 "$INPUT_FILE" | while IFS='|' read -r pod_name template_dumped pod_ready node_un migrated _; do
    # Trim whitespace
    pod_name=$(echo "$pod_name" | xargs)
    template_dumped=$(echo "$template_dumped" | xargs)
    migrated=$(echo "$migrated" | xargs)

    # Skip separator lines, empty lines, and header repeats
    [ -z "$pod_name" ] && continue
    [ "$pod_name" = "Pod Name" ] && continue
    [[ "$pod_name" == *"="* ]] && continue
    [[ "$pod_name" =~ ^-+$ ]] && continue

    # Store data
    echo "$pod_name|$template_dumped|$migrated"
done | {
    # Read into arrays and calculate
    prev_pod=""
    prev_migrated=""
    prev_rack=""
    last_rack_pod=""
    last_rack_migrated=""

    while IFS='|' read -r pod_name template_dumped migrated; do
        # Skip if missing data
        [ "$template_dumped" = "-" ] || [ "$migrated" = "-" ] && continue

        # Extract rack name from pod name (e.g., "cassandra-cluster-dc1-rack1-0" -> "rack1")
        current_rack=$(echo "$pod_name" | grep -oP 'rack\d+')

        # Check if we switched racks
        if [ -n "$prev_rack" ] && [ "$current_rack" != "$prev_rack" ]; then
            # Get rack finalization time from log
            rack_finalize_line=$(grep "pods migrated successfully, finalizing migration action" "$LOG_FILE" | grep "dc1-$prev_rack" | tail -1 | sed 's/\[[0-9]*m//g')

            if [ -n "$rack_finalize_line" ]; then
                rack_finalize_time=$(echo "$rack_finalize_line" | awk -F'] ' '{
                    ts_part = $2;
                    split(ts_part, a, " ");
                    print a[1] " " a[2] " " a[3];
                }')

                if [ -n "$rack_finalize_time" ] && [ -n "$last_rack_migrated" ]; then
                    t1=$(timestamp_to_seconds "$last_rack_migrated")
                    t2=$(timestamp_to_seconds "$rack_finalize_time")
                    finalize_time=$(echo "$t1 $t2" | awk '{printf "%.3f", $2 - $1}')
                    finalize_time_formatted=$(format_duration "$finalize_time")

                    printf "%-35s | %-20s | %-35s | %-20s | %-12s\n" \
                        "$last_rack_pod" "$last_rack_migrated" ">>> RACK FINALIZATION ($prev_rack) <<<" "$rack_finalize_time" "$finalize_time_formatted"
                fi
            fi
        fi

        # Calculate switch time if we have a previous pod in same rack
        if [ -n "$prev_pod" ] && [ "$current_rack" = "$prev_rack" ]; then
            t1=$(timestamp_to_seconds "$prev_migrated")
            t2=$(timestamp_to_seconds "$template_dumped")
            switch_time=$(echo "$t1 $t2" | awk '{printf "%.3f", $2 - $1}')
            switch_time_formatted=$(format_duration "$switch_time")

            printf "%-35s | %-20s | %-35s | %-20s | %-12s\n" \
                "$prev_pod" "$prev_migrated" "$pod_name" "$template_dumped" "$switch_time_formatted"
        elif [ -n "$prev_pod" ] && [ "$current_rack" != "$prev_rack" ]; then
            # Between-rack switch (after rack finalization line was printed above)
            t1_line=$(grep "pods migrated successfully, finalizing migration action" "$LOG_FILE" | grep "dc1-$prev_rack" | tail -1 | sed 's/\[[0-9]*m//g')
            if [ -n "$t1_line" ]; then
                t1_time=$(echo "$t1_line" | awk -F'] ' '{
                    ts_part = $2;
                    split(ts_part, a, " ");
                    print a[1] " " a[2] " " a[3];
                }')
                t1=$(timestamp_to_seconds "$t1_time")
                t2=$(timestamp_to_seconds "$template_dumped")
                switch_time=$(echo "$t1 $t2" | awk '{printf "%.3f", $2 - $1}')
                switch_time_formatted=$(format_duration "$switch_time")

                printf "%-35s | %-20s | %-35s | %-20s | %-12s\n" \
                    ">>> RACK FINALIZATION ($prev_rack) <<<" "$t1_time" "$pod_name" "$template_dumped" "$switch_time_formatted"
            fi
        fi

        # Store current for next iteration
        prev_pod="$pod_name"
        prev_migrated="$migrated"
        prev_rack="$current_rack"
        last_rack_pod="$pod_name"
        last_rack_migrated="$migrated"
    done

    # Handle the last rack finalization
    if [ -n "$prev_rack" ] && [ -n "$last_rack_migrated" ]; then
        rack_finalize_line=$(grep "pods migrated successfully, finalizing migration action" "$LOG_FILE" | grep "dc1-$prev_rack" | tail -1 | sed 's/\[[0-9]*m//g')

        if [ -n "$rack_finalize_line" ]; then
            rack_finalize_time=$(echo "$rack_finalize_line" | awk -F'] ' '{
                ts_part = $2;
                split(ts_part, a, " ");
                print a[1] " " a[2] " " a[3];
            }')

            if [ -n "$rack_finalize_time" ]; then
                t1=$(timestamp_to_seconds "$last_rack_migrated")
                t2=$(timestamp_to_seconds "$rack_finalize_time")
                finalize_time=$(echo "$t1 $t2" | awk '{printf "%.3f", $2 - $1}')
                finalize_time_formatted=$(format_duration "$finalize_time")

                printf "%-35s | %-20s | %-35s | %-20s | %-12s\n" \
                    "$last_rack_pod" "$last_rack_migrated" ">>> RACK FINALIZATION ($prev_rack) <<<" "$rack_finalize_time" "$finalize_time_formatted"
            fi
        fi
    fi
}

echo "===================================================================================================="
echo ""
echo "Switch Time Definition:"
echo "  Time from when a pod completes migration until the next pod's template is dumped and starts migration"
echo "  This includes any rack-level finalization, status updates, and preparation for the next pod"
echo ""
echo "Special Lines:"
echo "  '>>> RACK FINALIZATION (rackN) <<<' shows time from last pod migration to rack finalization message"
echo "  This represents the final rack-level operations after all pods in the rack are migrated"
