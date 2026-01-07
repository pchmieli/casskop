#!/bin/bash

#TODO: necessary
LOG_FILE="${1:-completeLog.log}"

if [ ! -f "$LOG_FILE" ]; then
    echo "Error: Log file not found: $LOG_FILE"
    exit 1
fi

echo "Rack Migration Time Measurements"
echo "================================="
echo ""

# Process each rack
for rack_num in 1 2 3; do
    rack="dc1-rack$rack_num"

    # Get first start time and last end time, stripping ANSI codes
    start=$(grep "Storage migration should be started" "$LOG_FILE" | grep "dc1-rack$rack_num" | head -1 | sed 's/\x1b\[[0-9;]*m//g')
    end=$(grep "pods migrated successfully" "$LOG_FILE" | grep "dc1-rack$rack_num" | tail -1 | sed 's/\x1b\[[0-9;]*m//g')

    if [ -z "$start" ] || [ -z "$end" ]; then
        echo "$rack: No migration data found"
        echo ""
        continue
    fi

    # Extract timestamp (format: "Feb DD HH:MM:SS.mmm" appears after "] " and before " [")
    start_ts=$(echo "$start" | sed -n 's/.*] \(Feb [0-9]* [0-9][0-9]:[0-9][0-9]:[0-9][0-9]\.[0-9]*\) \[.*/\1/p')
    end_ts=$(echo "$end" | sed -n 's/.*] \(Feb [0-9]* [0-9][0-9]:[0-9][0-9]:[0-9][0-9]\.[0-9]*\) \[.*/\1/p')

    echo "$rack:"
    echo "  Start: $start_ts"
    echo "  End:   $end_ts"

    # Parse timestamps for duration calculation using awk
    if [ -n "$start_ts" ] && [ -n "$end_ts" ]; then
        echo "$start_ts" "$end_ts" | awk '{
            # Parse start: Feb DD HH:MM:SS.mmm
            start_day = $2;
            split($3, st, ":");
            split(st[3], ss, ".");
            start_sec = start_day * 86400 + st[1] * 3600 + st[2] * 60 + ss[1] + ss[2]/1000;

            # Parse end: Feb DD HH:MM:SS.mmm
            end_day = $5;
            split($6, et, ":");
            split(et[3], es, ".");
            end_sec = end_day * 86400 + et[1] * 3600 + et[2] * 60 + es[1] + es[2]/1000;

            # Calculate duration
            diff = end_sec - start_sec;
            hours = int(diff / 3600);
            remainder = diff % 3600;
            minutes = int(remainder / 60);
            seconds = remainder % 60;

            printf "  Duration: %dh %dm %.3fs\n", hours, minutes, seconds;
        }'
    fi

    echo ""
done

echo "================================="
