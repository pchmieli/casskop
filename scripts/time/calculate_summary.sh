#!/bin/bash

# Script to calculate average migration metrics from the generated reports
# Produces a summary with key performance indicators

if [ $# -lt 3 ]; then
    echo "Usage: $0 <phase_durations_file> <pod_switch_times_file> <output_file> [status_file]"
    exit 1
fi

PHASE_FILE="$1"
SWITCH_FILE="$2"
OUTPUT_FILE="$3"
STATUS_FILE="$4"

if [ ! -f "$PHASE_FILE" ]; then
    echo "Error: Phase durations file not found: $PHASE_FILE"
    exit 1
fi

if [ ! -f "$SWITCH_FILE" ]; then
    echo "Error: Pod switch times file not found: $SWITCH_FILE"
    exit 1
fi

# Function to convert time string "XmY.Zs" to seconds
time_to_seconds() {
    echo "$1" | awk '{
        # Extract minutes and seconds
        match($0, /([0-9]+)m/, m);
        match($0, /([0-9]+\.[0-9]+)s/, s);
        mins = m[1];
        secs = s[1];
        print mins * 60 + secs;
    }'
}

# Calculate averages from phase durations
echo "Processing phase durations..."
PHASE_DATA=$(tail -n +4 "$PHASE_FILE" | grep -v "^=" | grep -v "Phase Definitions" | grep -v "^$" | grep -v "^\-\-" | while read line; do
    # Skip header and separator lines
    echo "$line" | grep -q "Pod Name" && continue
    echo "$line" | grep -q "Boot Time" && continue
    echo "$line" | grep -q "^-" && continue
    echo "$line" | grep -q "^=" && continue
    [ -z "$line" ] && continue

    # Extract times using awk
    echo "$line" | awk -F'|' '{
        if (NF >= 5) {
            boot = $2;
            join = $3;
            finalize = $4;
            gsub(/^[ \t]+|[ \t]+$/, "", boot);
            gsub(/^[ \t]+|[ \t]+$/, "", join);
            gsub(/^[ \t]+|[ \t]+$/, "", finalize);
            print boot "|" join "|" finalize;
        }
    }'
done)

# Calculate average Boot Time
AVG_BOOT=$(echo "$PHASE_DATA" | awk -F'|' '{
    split($1, t, "m");
    mins = t[1];
    split(t[2], s, "s");
    secs = s[1];
    total += mins * 60 + secs;
    count++;
}
END {
    if (count > 0) {
        avg = total / count;
        mins = int(avg / 60);
        secs = avg - (mins * 60);
        printf "%dm %.1fs", mins, secs;
    }
}')

# Calculate average Join Time
AVG_JOIN=$(echo "$PHASE_DATA" | awk -F'|' '{
    split($2, t, "m");
    mins = t[1];
    split(t[2], s, "s");
    secs = s[1];
    total += mins * 60 + secs;
    count++;
}
END {
    if (count > 0) {
        avg = total / count;
        mins = int(avg / 60);
        secs = avg - (mins * 60);
        printf "%dm %.1fs", mins, secs;
    }
}')

# Calculate average Finalize Time
AVG_FINALIZE=$(echo "$PHASE_DATA" | awk -F'|' '{
    split($3, t, "m");
    mins = t[1];
    split(t[2], s, "s");
    secs = s[1];
    total += mins * 60 + secs;
    count++;
}
END {
    if (count > 0) {
        avg = total / count;
        mins = int(avg / 60);
        secs = avg - (mins * 60);
        printf "%dm %.1fs", mins, secs;
    }
}')

# Calculate average Pod Switch Time (excluding lines where RACK FINALIZATION is in "To Pod" column)
echo "Processing pod switch times..."
AVG_POD_SWITCH=$(tail -n +4 "$SWITCH_FILE" | grep -v "^=" | grep -v "Switch Time Definition" | grep -v "Special Lines" | grep -v "^$" | grep -v "^\-\-" | grep -v "^From" | awk -F'|' '{
    if (NF >= 5) {
        # Only exclude if RACK FINALIZATION is in column 3 (To Pod), not column 2 (From Pod)
        to_pod = $3;
        gsub(/^[ \t]+|[ \t]+$/, "", to_pod);

        # Skip if this is a line TO rack finalization
        if (to_pod ~ /RACK FINALIZATION/) {
            next;
        }

        time = $5;
        gsub(/^[ \t]+|[ \t]+$/, "", time);
        if (time ~ /[0-9]+m/) {
            split(time, t, "m");
            mins = t[1];
            split(t[2], s, "s");
            secs = s[1];
            total += mins * 60 + secs;
            count++;
        }
    }
}
END {
    if (count > 0) {
        avg = total / count;
        mins = int(avg / 60);
        secs = avg - (mins * 60);
        printf "%dm %.1fs", mins, secs;
    }
}')

# Calculate average Rack Finalization Time (only lines TO rack finalization)
# Also count the number of racks to determine rack size
RACK_DATA=$(grep ">>> RACK FINALIZATION" "$SWITCH_FILE" | grep -v "^From" | awk -F'|' '{
    # Get the line that ends with RACK FINALIZATION (not starts with it)
    if ($3 ~ /RACK FINALIZATION/) {
        time = $5;
        gsub(/^[ \t]+|[ \t]+$/, "", time);
        if (time ~ /[0-9]+m/) {
            split(time, t, "m");
            mins = t[1];
            split(t[2], s, "s");
            secs = s[1];
            print mins * 60 + secs;
        }
    }
}')

AVG_RACK_FINALIZE=$(echo "$RACK_DATA" | awk '{
    total += $1;
    count++;
}
END {
    if (count > 0) {
        avg = total / count;
        mins = int(avg / 60);
        secs = avg - (mins * 60);
        printf "%dm %.1fs", mins, secs;
    }
}')

# Calculate rack size (total pods / number of racks)
NUM_RACKS=$(echo "$RACK_DATA" | wc -l)
NUM_PODS=$(echo "$PHASE_DATA" | wc -l)
RACK_SIZE=$((NUM_PODS / NUM_RACKS))

# Calculate per-pod rack finalization overhead
AVG_RACK_FINALIZE_PER_POD=$(echo "$RACK_DATA" | awk -v rack_size="$RACK_SIZE" '{
    total += $1;
    count++;
}
END {
    if (count > 0) {
        avg = total / count;
        per_pod = avg / rack_size;
        mins = int(per_pod / 60);
        secs = per_pod - (mins * 60);
        printf "%dm %.1fs", mins, secs;
    }
}')

# Calculate Average Pod Const Time (Boot + Finalize + Pod Switch, excluding rack finalization)
AVG_POD_CONST=$(echo "$AVG_BOOT $AVG_FINALIZE $AVG_POD_SWITCH" | awk '{
    # Input: "5m 58.3s 1m 25.2s 0m 8.3s"
    # Fields: $1="5m" $2="58.3s" $3="1m" $4="25.2s" $5="0m" $6="8.3s"

    # Parse boot time (fields 1 and 2)
    split($1, bt, "m");
    split($2, bs, "s");
    boot_secs = bt[1] * 60 + bs[1];

    # Parse finalize time (fields 3 and 4)
    split($3, ft, "m");
    split($4, fs, "s");
    finalize_secs = ft[1] * 60 + fs[1];

    # Parse switch time (fields 5 and 6)
    split($5, st, "m");
    split($6, ss, "s");
    switch_secs = st[1] * 60 + ss[1];

    total = boot_secs + finalize_secs + switch_secs;
    mins = int(total / 60);
    secs = total - (mins * 60);
    printf "%dm %.1fs", mins, secs;
}')

# Calculate average load per node if status file is provided
AVG_LOAD_GIB=""
AVG_JOIN_PER_GIB=""
if [ -n "$STATUS_FILE" ] && [ -f "$STATUS_FILE" ]; then
    echo "Processing status file for load data..."
    AVG_LOAD_GIB=$(grep "UN" "$STATUS_FILE" | awk 'NF > 5 {
        load = $3;
        unit = $4;
        # Only process if unit is GiB
        if (unit ~ /GiB/) {
            total += load;
            count++;
        }
    }
    END {
        if (count > 0) {
            avg = total / count;
            printf "%.2f GiB", avg;
        }
    }')

    # Calculate Join Time per GiB if we have both join time and load
    if [ -n "$AVG_LOAD_GIB" ] && [ -n "$AVG_JOIN" ]; then
        # First convert join time to seconds
        # AVG_JOIN format is "9m 48.9s" - could be one or two fields depending on spacing
        JOIN_SECS=$(echo "$AVG_JOIN" | sed 's/m/ /;s/s//' | awk '{
            if (NF == 2) {
                # Format: "9" "48.9" (after sed replacement)
                mins = $1;
                secs = $2;
            } else {
                # Format: "9 48.9" all in $1
                split($0, parts, " ");
                mins = parts[1];
                secs = parts[2];
            }
            print mins * 60 + secs;
        }')

        # Then extract just the number from load
        LOAD_NUM=$(echo "$AVG_LOAD_GIB" | awk '{print $1}')

        # Now calculate per GiB
        AVG_JOIN_PER_GIB=$(echo "$JOIN_SECS $LOAD_NUM" | awk '{
            join_secs = $1;
            load = $2;

            if (load > 0) {
                per_gib = join_secs / load;
                result_mins = int(per_gib / 60);
                result_secs = per_gib - (result_mins * 60);
                printf "%dm %.2fs", result_mins, result_secs;
            }
        }')
    fi
fi

# Generate report
{
    echo "===================================================================================================="
    echo "MIGRATION PERFORMANCE SUMMARY"
    echo "===================================================================================================="
    echo ""
    echo "Generated: $(date)"
    echo ""
    echo "===================================================================================================="
    echo "AVERAGE METRICS"
    echo "===================================================================================================="
    echo ""
    echo "1. AVERAGE POD CONSTANT TIME (Pod Overhead)"
    echo "   Components: Boot Time + Finalize Time + Pod Switch Time"
    echo "   Excludes: Data migration (Join Time) and Rack Finalization"
    echo "   -------------------------------------------------------------------------"
    echo "   Time: $AVG_POD_CONST"
    echo ""
    echo "   Breakdown:"
    echo "     - Boot Time (PVC creation + pod start):     $AVG_BOOT"
    echo "     - Finalize Time (repair + verification):    $AVG_FINALIZE"
    echo "     - Pod Switch Time (status updates):         $AVG_POD_SWITCH"
    echo ""
    echo "===================================================================================================="
    echo ""
    echo "2. AVERAGE POD DATA MIGRATION TIME (Join Time)"
    echo "   Components: Cassandra startup + cluster join + data streaming"
    echo "   This is the variable part depending on data volume"
    echo "   -------------------------------------------------------------------------"
    echo "   Time: $AVG_JOIN"
    if [ -n "$AVG_LOAD_GIB" ]; then
        echo "   Average Load per Node: $AVG_LOAD_GIB"
        if [ -n "$AVG_JOIN_PER_GIB" ]; then
            echo "   Time per GiB:          $AVG_JOIN_PER_GIB"
        fi
    fi
    echo ""
    echo "===================================================================================================="
    echo ""
    echo "3. AVERAGE RACK FINALIZATION OVERHEAD"
    echo "   Components: Final rack operations after all pods migrated"
    echo "   Measured from last pod migration to rack finalization message"
    echo "   Note: This overhead is caused mainly by pod restarts (removing init-container)"
    echo "         and scales with rack size"
    echo "   -------------------------------------------------------------------------"
    echo "   Total Rack Finalization Time: $AVG_RACK_FINALIZE"
    echo "   Rack Size (pods per rack):    $RACK_SIZE"
    echo "   Per-Pod Overhead:             $AVG_RACK_FINALIZE_PER_POD"
    echo ""
    echo "===================================================================================================="
    echo "INTERPRETATION"
    echo "===================================================================================================="
    echo ""
    echo "Total time per pod (excluding first pod bootstrap):"
    echo "  = Pod Constant Time + Data Migration Time"
    echo "  = $AVG_POD_CONST + $AVG_JOIN"
    echo ""
    if [ -n "$AVG_JOIN_PER_GIB" ]; then
        echo "With data volume awareness:"
        echo "  = Pod Constant Time + (Time per GiB * Load in GiB)"
        echo "  = $AVG_POD_CONST + ($AVG_JOIN_PER_GIB * Load)"
        echo ""
    fi
    echo "Total time per rack (N pods):"
    echo "  = (Pod Constant Time + Data Migration Time) * N + Rack Finalization Per-Pod * N"
    echo "  = ($AVG_POD_CONST + $AVG_JOIN) * N + $AVG_RACK_FINALIZE_PER_POD * N"
    echo "  = ($AVG_POD_CONST + $AVG_JOIN + $AVG_RACK_FINALIZE_PER_POD) * N"
    echo ""
    if [ -n "$AVG_JOIN_PER_GIB" ]; then
        echo "With data volume awareness:"
        echo "  = (Pod Constant Time + Time per GiB * Load + Rack Finalization Per-Pod) * N"
        echo "  = ($AVG_POD_CONST + $AVG_JOIN_PER_GIB * Load + $AVG_RACK_FINALIZE_PER_POD) * N"
        echo ""
    fi
    echo "  For current rack size ($RACK_SIZE pods):"
    echo "  = ($AVG_POD_CONST + $AVG_JOIN + $AVG_RACK_FINALIZE_PER_POD) * $RACK_SIZE"
    if [ -n "$AVG_LOAD_GIB" ] && [ -n "$AVG_JOIN_PER_GIB" ]; then
        echo "  = ($AVG_POD_CONST + $AVG_JOIN_PER_GIB * $AVG_LOAD_GIB + $AVG_RACK_FINALIZE_PER_POD) * $RACK_SIZE"
    fi
    echo ""
    echo "Key Insights:"
    echo "  - Pod Constant Time is fixed per pod (infrastructure overhead)"
    if [ -n "$AVG_JOIN_PER_GIB" ]; then
        echo "  - Data Migration Time = $AVG_JOIN_PER_GIB per GiB (scales linearly with data volume)"
    else
        echo "  - Data Migration Time varies with data volume"
    fi
    echo "  - Rack Finalization scales with rack size (~$AVG_RACK_FINALIZE_PER_POD per pod)"
    echo ""
    echo "===================================================================================================="
} | tee "$OUTPUT_FILE"

echo ""
echo "Summary saved to: $OUTPUT_FILE"
