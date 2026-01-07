#!/bin/bash
#
# show-migration-diagnostics.sh
#
# Displays storage migration diagnostics for a specific pod.
# Shows token ranges and nodetool status before/after migration with diffs.
#
# Usage: ./show-migration-diagnostics.sh <pod-name> [namespace] [cluster-name] [dc-rack]
#
# Examples:
#   ./show-migration-diagnostics.sh cassandra-cluster-dc1-rack1-0
#   ./show-migration-diagnostics.sh cassandra-cluster-dc1-rack1-0 prod-cassandra
#   ./show-migration-diagnostics.sh cassandra-cluster-dc1-rack1-0 prod-cassandra cassandra-cluster dc1-rack1

set -euo pipefail

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
CYAN='\033[0;36m'
NC='\033[0m' # No Color

# Parse arguments
POD_NAME="${1:-}"
NAMESPACE="${2:-}"
CLUSTER_NAME="${3:-}"
DC_RACK="${4:-}"

if [ -z "$POD_NAME" ]; then
    echo -e "${RED}Error: Pod name is required${NC}"
    echo "Usage: $0 <pod-name> [namespace] [cluster-name] [dc-rack]"
    exit 1
fi

# Auto-detect namespace if not provided
if [ -z "$NAMESPACE" ]; then
    NAMESPACE=$(kubectl get pod "$POD_NAME" -A -o jsonpath='{.metadata.namespace}' 2>/dev/null || echo "")
    if [ -z "$NAMESPACE" ]; then
        echo -e "${RED}Error: Could not find pod $POD_NAME in any namespace${NC}"
        exit 1
    fi
    echo -e "${CYAN}Auto-detected namespace: $NAMESPACE${NC}"
fi

# Auto-detect cluster name from pod labels if not provided
if [ -z "$CLUSTER_NAME" ]; then
    CLUSTER_NAME=$(kubectl get pod "$POD_NAME" -n "$NAMESPACE" -o jsonpath='{.metadata.labels.cassandracluster}' 2>/dev/null || echo "")
    if [ -z "$CLUSTER_NAME" ]; then
        echo -e "${RED}Error: Could not detect cluster name from pod labels${NC}"
        exit 1
    fi
    echo -e "${CYAN}Auto-detected cluster: $CLUSTER_NAME${NC}"
fi

# Auto-detect DC-Rack from pod labels if not provided
if [ -z "$DC_RACK" ]; then
    DC_RACK=$(kubectl get pod "$POD_NAME" -n "$NAMESPACE" -o jsonpath='{.metadata.labels.dc-rack}' 2>/dev/null || echo "")
    if [ -z "$DC_RACK" ]; then
        echo -e "${RED}Error: Could not detect dc-rack from pod labels${NC}"
        exit 1
    fi
    echo -e "${CYAN}Auto-detected dc-rack: $DC_RACK${NC}"
fi

echo ""
echo -e "${BLUE}========================================${NC}"
echo -e "${BLUE}Storage Migration Diagnostics${NC}"
echo -e "${BLUE}========================================${NC}"
echo -e "Pod:       ${GREEN}$POD_NAME${NC}"
echo -e "Namespace: ${GREEN}$NAMESPACE${NC}"
echo -e "Cluster:   ${GREEN}$CLUSTER_NAME${NC}"
echo -e "DC-Rack:   ${GREEN}$DC_RACK${NC}"
echo -e "${BLUE}========================================${NC}"
echo ""

# Fetch migration state from CRD
MIGRATION_STATE=$(kubectl get cassandraclusters.db.orange.com "$CLUSTER_NAME" -n "$NAMESPACE" \
    -o jsonpath="{.status.cassandraRackStatus."$DC_RACK".storageMigrationState.pods."$POD_NAME"}" 2>/dev/null || echo "")

if [ -z "$MIGRATION_STATE" ] || [ "$MIGRATION_STATE" == "null" ]; then
    echo -e "${RED}Error: No migration state found for pod $POD_NAME in rack $DC_RACK${NC}"
    echo ""
    echo "Possible reasons:"
    echo "  - Pod has not been migrated yet"
    echo "  - Migration has been finalized (state cleaned up)"
    echo "  - Pod name or DC-Rack is incorrect"
    exit 1
fi

# Parse migration state
TOKEN_RANGES_BEFORE=$(echo "$MIGRATION_STATE" | jq -r '.tokenRangesBeforeMigration // empty')
TOKEN_RANGES_AFTER=$(echo "$MIGRATION_STATE" | jq -r '.tokenRangesAfterMigration // empty')
STATUS_BEFORE=$(echo "$MIGRATION_STATE" | jq -r '.nodetoolStatusBeforeMigration // empty')
STATUS_AFTER=$(echo "$MIGRATION_STATE" | jq -r '.nodetoolStatusAfterMigration // empty')
MIGRATED=$(echo "$MIGRATION_STATE" | jq -r '.migrated // false')
OLD_IP=$(echo "$MIGRATION_STATE" | jq -r '.oldPodIp // "N/A"')
OLD_HOST_ID=$(echo "$MIGRATION_STATE" | jq -r '.oldHostId // "N/A"')

# Display migration status
echo -e "${YELLOW}Migration Status:${NC}"
echo -e "  Migrated:     $([ "$MIGRATED" == "true" ] && echo -e "${GREEN}Yes${NC}" || echo -e "${YELLOW}In Progress${NC}")"
echo -e "  Old Pod IP:   $OLD_IP"
echo -e "  Old Host ID:  $OLD_HOST_ID"
echo ""

# Check if diagnostics are available
if [ -z "$TOKEN_RANGES_BEFORE" ] && [ -z "$STATUS_BEFORE" ]; then
    echo -e "${YELLOW}Warning: No 'before migration' diagnostics captured yet${NC}"
fi

if [ -z "$TOKEN_RANGES_AFTER" ] && [ -z "$STATUS_AFTER" ]; then
    echo -e "${YELLOW}Warning: No 'after migration' diagnostics captured yet${NC}"
fi

# Create temp files for diffs
TEMP_DIR=$(mktemp -d)
trap 'rm -rf "$TEMP_DIR"' EXIT

TOKEN_BEFORE_FILE="$TEMP_DIR/token_before.txt"
TOKEN_AFTER_FILE="$TEMP_DIR/token_after.txt"
STATUS_BEFORE_FILE="$TEMP_DIR/status_before.txt"
STATUS_AFTER_FILE="$TEMP_DIR/status_after.txt"

echo "$TOKEN_RANGES_BEFORE" | sort > "$TOKEN_BEFORE_FILE"
echo "$TOKEN_RANGES_AFTER" | sort > "$TOKEN_AFTER_FILE"
echo "$STATUS_BEFORE" | sort > "$STATUS_BEFORE_FILE"
echo "$STATUS_AFTER" | sort > "$STATUS_AFTER_FILE"

# Display Token Ranges Comparison
echo -e "${CYAN}═══════════════════════════════════════${NC}"
echo -e "${CYAN}Token Ranges Comparison${NC}"
echo -e "${CYAN}═══════════════════════════════════════${NC}"
echo ""

if [ -n "$TOKEN_RANGES_BEFORE" ] && [ -n "$TOKEN_RANGES_AFTER" ]; then
    echo -e "${YELLOW}Diff (- before, + after):${NC}"
    diff -u "$TOKEN_BEFORE_FILE" "$TOKEN_AFTER_FILE" || true
    echo ""

    # Count changes
    ADDED=$(diff "$TOKEN_BEFORE_FILE" "$TOKEN_AFTER_FILE" | grep -c '^+' || echo "0")
    REMOVED=$(diff "$TOKEN_BEFORE_FILE" "$TOKEN_AFTER_FILE" | grep -c '^-' || echo "0")
    echo -e "${GREEN}Added lines:   $ADDED${NC}"
    echo -e "${RED}Removed lines: $REMOVED${NC}"
elif [ -n "$TOKEN_RANGES_BEFORE" ]; then
    echo -e "${YELLOW}Before Migration:${NC}"
    cat "$TOKEN_BEFORE_FILE"
    echo ""
    echo -e "${YELLOW}After migration data not available yet${NC}"
elif [ -n "$TOKEN_RANGES_AFTER" ]; then
    echo -e "${YELLOW}After Migration:${NC}"
    cat "$TOKEN_AFTER_FILE"
    echo ""
    echo -e "${YELLOW}Before migration data not available${NC}"
else
    echo -e "${YELLOW}No token range data available${NC}"
fi

echo ""

# Display Nodetool Status Comparison
echo -e "${CYAN}═══════════════════════════════════════${NC}"
echo -e "${CYAN}Nodetool Status Comparison${NC}"
echo -e "${CYAN}═══════════════════════════════════════${NC}"
echo ""

if [ -n "$STATUS_BEFORE" ] && [ -n "$STATUS_AFTER" ]; then
    echo -e "${YELLOW}Diff (- before, + after):${NC}"
    diff -u "$STATUS_BEFORE_FILE" "$STATUS_AFTER_FILE" || true
    echo ""

    # Count changes
    ADDED=$(diff "$STATUS_BEFORE_FILE" "$STATUS_AFTER_FILE" | grep -c '^+' || echo "0")
    REMOVED=$(diff "$STATUS_BEFORE_FILE" "$STATUS_AFTER_FILE" | grep -c '^-' || echo "0")
    echo -e "${GREEN}Added lines:   $ADDED${NC}"
    echo -e "${RED}Removed lines: $REMOVED${NC}"
elif [ -n "$STATUS_BEFORE" ]; then
    echo -e "${YELLOW}Before Migration:${NC}"
    cat "$STATUS_BEFORE_FILE"
    echo ""
    echo -e "${YELLOW}After migration data not available yet${NC}"
elif [ -n "$STATUS_AFTER" ]; then
    echo -e "${YELLOW}After Migration:${NC}"
    cat "$STATUS_AFTER_FILE"
    echo ""
    echo -e "${YELLOW}Before migration data not available${NC}"
else
    echo -e "${YELLOW}No nodetool status data available${NC}"
fi

echo ""
echo -e "${BLUE}========================================${NC}"
echo -e "${GREEN}Diagnostics retrieval complete${NC}"
echo -e "${BLUE}========================================${NC}"
