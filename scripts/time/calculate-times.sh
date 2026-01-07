#!/bin/bash

while getopts "d:l:p:s:" opt; do
  case $opt in
    d) output_dir="$OPTARG" ;;
    l) log_file="$OPTARG" ;;
    p) prefix="$OPTARG" ;;
    s) status_file="$OPTARG" ;;
    *) echo "Usage: $0 -d <output_directory> -l <log_file> -p <prefix> [-s <status_file>]" ;;
  esac
done

# Validate required parameters
if [ -z "$output_dir" ] || [ -z "$log_file" ] || [ -z "$prefix" ]; then
    echo "Error: Missing required parameters"
    echo "Usage: $0 -d <output_directory> -l <log_file> -p <prefix> [-s <status_file>]"
    exit 1
fi

if [ ! -f "$log_file" ]; then
    echo "Error: Log file not found: $log_file"
    exit 1
fi

if [ ! -d "$output_dir" ]; then
    mkdir -p "$output_dir"
fi

script_dir="$(dirname "$(realpath "$0")")"

echo "=========================================="
echo "Measuring Rack Migration Times"
echo "=========================================="
$script_dir/measure_rack_times.sh "$log_file"
echo ""


echo "=========================================="
echo "Measuring Pod Migration Times"
echo "=========================================="
$script_dir/measure_pod_times.sh "$log_file" | tee "$output_dir/${prefix}_pod_times.txt"
echo ""

echo "=========================================="
echo "Calculating Phase Durations"
echo "=========================================="
$script_dir/calculate_phase_durations.sh "$output_dir/${prefix}_pod_times.txt" | tee "$output_dir/${prefix}_phase_durations.txt"
echo ""

echo "=========================================="
echo "Calculating Pod Switch Times"
echo "=========================================="
$script_dir/calculate_pod_switch_times.sh "$output_dir/${prefix}_pod_times.txt" "$log_file" | tee "$output_dir/${prefix}_pod_switch_times.txt"
echo ""

echo "=========================================="
echo "Calculating Summary Metrics"
echo "=========================================="
if [ -n "$status_file" ]; then
    $script_dir/calculate_summary.sh "$output_dir/${prefix}_phase_durations.txt" "$output_dir/${prefix}_pod_switch_times.txt" "$output_dir/${prefix}_summary.txt" "$status_file"
else
    $script_dir/calculate_summary.sh "$output_dir/${prefix}_phase_durations.txt" "$output_dir/${prefix}_pod_switch_times.txt" "$output_dir/${prefix}_summary.txt"
fi
echo ""

echo "=========================================="
echo "Results saved to:"
echo "  - $output_dir/${prefix}_pod_times.txt"
echo "  - $output_dir/${prefix}_phase_durations.txt"
echo "  - $output_dir/${prefix}_pod_switch_times.txt"
echo "  - $output_dir/${prefix}_summary.txt"
echo "=========================================="
