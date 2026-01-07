package nodeoperations

import (
	"fmt"
	"math/big"
	"sort"

	"github.com/swarvanusg/go_jolokia"
)

//TODO later: maybe whole jolokia related code may be moved here

//TODO: check if credential needed each time it is initalized (reuse code from node_operations)

func NewNodeOp(client *go_jolokia.JolokiaClient, host string) StorageMigrationNodeOperations {
	return &storageMigrationNodeOperations{
		client: client,
		host:   host,
	}
}

//TODO: eventually review this interface, maybe some code become not needed

type StorageMigrationNodeOperations interface {
	GetRackStatus(rackName string) (NodeStatuses, error)
	RemoveNode(hostID string) error
	GetNetstats() (string, error)

	// GetLocalHostID returns the host ID of the local node via JMX
	GetLocalHostID() (string, error)

	// TriggerRepairAsync starts a repair on the node and returns a command id.
	// It uses StorageService.repairAsync (if supported by the Cassandra version).
	TriggerRepairAsync(primaryRange bool) (string, error)

	// GetNonLocalKeyspaces returns keyspaces excluding local system keyspaces.
	GetNonLocalKeyspaces() ([]string, error)

	// HasStreamingSessions returns true if the node currently has active streaming sessions.
	// Used to monitor repair progress.
	HasStreamingSessions() (bool, error)

	// GetTokenRanges returns token ring information for all nodes (similar to nodetool ring)
	GetTokenRanges() (string, error)

	// GetNodetoolStatus returns cluster status information (similar to nodetool status)
	GetNodetoolStatus() (string, error)
}

type NodeStatuses []NodeStatus

// GetUpNodes returns only nodes with Status "Up"
func (s NodeStatuses) GetUpNodes() NodeStatuses {
	result := make(NodeStatuses, 0)
	for _, node := range s {
		if node.Status == "Up" {
			result = append(result, node)
		}
	}
	return result
}

// GetDownNodes returns only nodes with Status "Down"
func (s NodeStatuses) GetDownNodes() NodeStatuses {
	result := make(NodeStatuses, 0)
	for _, node := range s {
		if node.Status == "Down" {
			result = append(result, node)
		}
	}
	return result
}

func (s NodeStatuses) GetJoiningNodes() NodeStatuses {
	result := make(NodeStatuses, 0)
	for _, node := range s {
		if node.State == "Joining" {
			result = append(result, node)
		}
	}
	return result
}

func (s NodeStatuses) GetUpNormalNodes() NodeStatuses {
	result := make(NodeStatuses, 0)
	for _, node := range s {
		if node.Status == "Up" && node.State == "Normal" {
			result = append(result, node)
		}
	}
	return result
}

// GetIps returns a slice of IP addresses from all nodes
func (s NodeStatuses) GetIps() []string {
	ips := make([]string, 0, len(s))
	for _, node := range s {
		ips = append(ips, node.IP)
	}
	return ips
}

type NodeStatus struct {
	Status string
	State  string
	IP     string
	HostID string
	Rack   string
}

type storageMigrationNodeOperations struct {
	client *go_jolokia.JolokiaClient
	host   string
}

// RemoveNode removes a node from the cluster by its host ID using nodetool removenode
// This is typically used to clean up ghost nodes (nodes that are down and won't return)
func (s *storageMigrationNodeOperations) RemoveNode(hostID string) error {
	// Execute removeNode operation via Jolokia
	result, err := s.client.ExecuteOperation(
		"org.apache.cassandra.db:type=StorageService",
		"removeNode",
		[]interface{}{hostID},
		"")
	if err != nil {
		return fmt.Errorf("failed to execute removeNode for %s: %w", hostID, err)
	}

	if result.Error != "" {
		return fmt.Errorf("removeNode error for %s: %s", hostID, result.Error)
	}

	return nil
}

// GetLocalHostID returns the host ID of the local Cassandra node via JMX
// This is equivalent to executing "nodetool info" and parsing the ID field
func (s *storageMigrationNodeOperations) GetLocalHostID() (string, error) {
	// Query the LocalHostId attribute from StorageService MBean
	request := go_jolokia.NewJolokiaRequest(go_jolokia.READ,
		"org.apache.cassandra.db:type=StorageService", nil, "LocalHostId")
	result, err := s.client.ExecuteReadRequest(request)
	if err != nil {
		return "", fmt.Errorf("failed to get LocalHostId: %w", err)
	}
	if result.Error != "" {
		return "", fmt.Errorf("LocalHostId error: %s", result.Error)
	}

	hostID, ok := result.Value.(string)
	if !ok {
		return "", fmt.Errorf("unexpected type for LocalHostId: %T", result.Value)
	}

	return hostID, nil
}

// GetNetstats returns streaming statistics from the Cassandra node via Jolokia
// It queries the StreamManager MBean which provides detailed information about active streams
func (s *storageMigrationNodeOperations) GetNetstats() (string, error) {
	request := go_jolokia.NewJolokiaRequest(go_jolokia.READ,
		"org.apache.cassandra.net:type=StreamManager", nil, "CurrentStreams")
	result, err := s.client.ExecuteReadRequest(request)
	if err != nil {
		return "", fmt.Errorf("failed to get CurrentStreams: %w", err)
	}
	if result.Error != "" {
		return "", fmt.Errorf("CurrentStreams error: %s", result.Error)
	}

	// Parse the streaming sessions
	streams, ok := result.Value.([]interface{})
	if !ok {
		return "", fmt.Errorf("unexpected type for CurrentStreams: %T", result.Value)
	}

	if len(streams) == 0 {
		return "Not sending any streams.\nNot receiving any streams.", nil
	}

	// Format output similar to nodetool netstats
	var output string
	for i, stream := range streams {
		streamMap, ok := stream.(map[string]interface{})
		if !ok {
			continue
		}

		// Extract stream information
		description := getStringValue(streamMap, "description")
		planID := getStringValue(streamMap, "planId")
		totalTxBytes := getInt64Value(streamMap, "totalTxBytes")
		totalRxBytes := getInt64Value(streamMap, "totalRxBytes")
		currentTxBytes := getInt64Value(streamMap, "currentTxBytes")
		currentRxBytes := getInt64Value(streamMap, "currentRxBytes")

		output += fmt.Sprintf("Stream #%d: %s (Plan ID: %s)\n", i+1, description, planID)

		if totalTxBytes > 0 {
			output += fmt.Sprintf("  Sending %d bytes (current: %d bytes)\n", totalTxBytes, currentTxBytes)
		}
		if totalRxBytes > 0 {
			output += fmt.Sprintf("  Receiving %d bytes (current: %d bytes)\n", totalRxBytes, currentRxBytes)
		}

		// Parse sessions
		sessions, ok := streamMap["sessions"].([]interface{})
		if ok {
			for _, session := range sessions {
				sessionMap, ok := session.(map[string]interface{})
				if !ok {
					continue
				}

				peer := getStringValue(sessionMap, "peer")
				state := getStringValue(sessionMap, "state")
				output += fmt.Sprintf("    Peer: %s, State: %s\n", peer, state)
			}
		}
	}

	return output, nil
}

// Helper functions to safely extract values from map[string]interface{}
func getStringValue(m map[string]interface{}, key string) string {
	if val, ok := m[key].(string); ok {
		return val
	}
	return ""
}

func getInt64Value(m map[string]interface{}, key string) int64 {
	if val, ok := m[key].(float64); ok {
		return int64(val)
	}
	if val, ok := m[key].(int64); ok {
		return val
	}
	if val, ok := m[key].(int); ok {
		return int64(val)
	}
	return 0
}

// GetRackStatus returns the status of all nodes in a specific rack
// It uses a single Jolokia batch request to efficiently query:
// - HostIdMap: Maps IP addresses to host IDs
// - LiveNodes: List of live node IPs
// - UnreachableNodes: List of unreachable node IPs
// - JoiningNodes, LeavingNodes, MovingNodes: Lists of nodes in various states
// Then makes individual getRack calls for each node (these could be batched too if needed)
func (s *storageMigrationNodeOperations) GetRackStatus(rackName string) (NodeStatuses, error) {
	// Use batch request to get all basic info in one HTTP call
	hostIDMap, liveNodes, unreachableNodes, joiningNodes, leavingNodes, movingNodes, err := s.getClusterInfoBatch()
	if err != nil {
		return nil, fmt.Errorf("failed to get cluster info: %w", err)
	}

	// Build status map: IP -> "Up" or "Down"
	statusMap := make(map[string]string)
	for _, ip := range liveNodes {
		statusMap[ip] = "Up"
	}
	for _, ip := range unreachableNodes {
		statusMap[ip] = "Down"
	}

	// Build state map: IP -> "Normal", "Joining", "Leaving", or "Moving"
	stateMap := make(map[string]string)
	for _, ip := range joiningNodes {
		stateMap[ip] = "Joining"
	}
	for _, ip := range leavingNodes {
		stateMap[ip] = "Leaving"
	}
	for _, ip := range movingNodes {
		stateMap[ip] = "Moving"
	}

	// Get rack information for each node
	var result []NodeStatus
	for ip, hostID := range hostIDMap {
		rack, err := s.getRackForEndpoint(ip)
		if err != nil {
			// If we can't get rack info, skip this node or use empty rack
			rack = ""
		}

		// Filter by rack name if specified
		if rackName != "" && rack != rackName {
			continue
		}

		status := statusMap[ip]
		if status == "" {
			// Node is not in live or unreachable list, mark as Unknown
			status = "Unknown"
		}

		state := stateMap[ip]
		if state == "" {
			// Node is not in any special state list, so it's Normal
			state = "Normal"
		}

		result = append(result, NodeStatus{
			Status: status,
			State:  state,
			IP:     ip,
			HostID: hostID,
			Rack:   rack,
		})
	}

	return result, nil
}

// getClusterInfoBatch gets all cluster info with individual requests
// Returns: hostIDMap, liveNodes, unreachableNodes, joiningNodes, leavingNodes, movingNodes, error
//
// NOTE: Despite the name, this currently uses 6 sequential HTTP requests because
// the go_jolokia library doesn't expose Jolokia's batch request API.
//
// FUTURE OPTIMIZATION: Jolokia supports batch requests via HTTP POST with JSON array:
// POST /jolokia/ with body: [
//
//	{"type":"read","mbean":"org.apache.cassandra.db:type=StorageService","attribute":"HostIdMap"},
//	{"type":"read","mbean":"org.apache.cassandra.db:type=StorageService","attribute":"LiveNodes"},
//	{"type":"read","mbean":"org.apache.cassandra.db:type=StorageService","attribute":"UnreachableNodes"},
//	{"type":"read","mbean":"org.apache.cassandra.db:type=StorageService","attribute":"JoiningNodes"},
//	{"type":"read","mbean":"org.apache.cassandra.db:type=StorageService","attribute":"LeavingNodes"},
//	{"type":"read","mbean":"org.apache.cassandra.db:type=StorageService","attribute":"MovingNodes"}
//
// ]
// This would reduce 6 HTTP round-trips to 1, significantly improving performance.
// To implement: either contribute batch support to go_jolokia or use direct HTTP client.
func (s *storageMigrationNodeOperations) getClusterInfoBatch() (map[string]string, []string, []string, []string, []string, []string, error) {
	// Get HostIdMap
	hostIDRequest := go_jolokia.NewJolokiaRequest(go_jolokia.READ,
		"org.apache.cassandra.db:type=StorageService", nil, "HostIdMap")
	hostIDResult, err := s.client.ExecuteReadRequest(hostIDRequest)
	if err != nil {
		return nil, nil, nil, nil, nil, nil, fmt.Errorf("failed to get HostIdMap: %w", err)
	}
	if hostIDResult.Error != "" {
		return nil, nil, nil, nil, nil, nil, fmt.Errorf("HostIdMap error: %s", hostIDResult.Error)
	}

	hostIDMap := make(map[string]string)
	if m, ok := hostIDResult.Value.(map[string]interface{}); ok {
		for ip, hostID := range m {
			if str, ok := hostID.(string); ok {
				hostIDMap[ip] = str
			}
		}
	} else {
		return nil, nil, nil, nil, nil, nil, fmt.Errorf("unexpected type for HostIdMap: %T", hostIDResult.Value)
	}

	// Get LiveNodes
	liveNodes, err := s.getNodeList("LiveNodes")
	if err != nil {
		return nil, nil, nil, nil, nil, nil, err
	}

	// Get UnreachableNodes
	unreachableNodes, err := s.getNodeList("UnreachableNodes")
	if err != nil {
		return nil, nil, nil, nil, nil, nil, err
	}

	// Get JoiningNodes
	joiningNodes, err := s.getNodeList("JoiningNodes")
	if err != nil {
		return nil, nil, nil, nil, nil, nil, err
	}

	// Get LeavingNodes
	leavingNodes, err := s.getNodeList("LeavingNodes")
	if err != nil {
		return nil, nil, nil, nil, nil, nil, err
	}

	// Get MovingNodes
	movingNodes, err := s.getNodeList("MovingNodes")
	if err != nil {
		return nil, nil, nil, nil, nil, nil, err
	}

	return hostIDMap, liveNodes, unreachableNodes, joiningNodes, leavingNodes, movingNodes, nil
}

// getNodeList is a helper to get a list of node IPs from a StorageService attribute
func (s *storageMigrationNodeOperations) getNodeList(attributeName string) ([]string, error) {
	request := go_jolokia.NewJolokiaRequest(go_jolokia.READ,
		"org.apache.cassandra.db:type=StorageService", nil, attributeName)
	result, err := s.client.ExecuteReadRequest(request)
	if err != nil {
		return nil, fmt.Errorf("failed to get %s: %w", attributeName, err)
	}
	if result.Error != "" {
		return nil, fmt.Errorf("%s error: %s", attributeName, result.Error)
	}

	nodes := make([]string, 0)
	if slice, ok := result.Value.([]interface{}); ok {
		for _, v := range slice {
			if str, ok := v.(string); ok {
				nodes = append(nodes, str)
			}
		}
		return nodes, nil
	}

	return nil, fmt.Errorf("unexpected type for %s: %T", attributeName, result.Value)
}

// getRackForEndpoint returns the rack name for a given endpoint IP
func (s *storageMigrationNodeOperations) getRackForEndpoint(endpoint string) (string, error) {
	// Use EndpointSnitchInfo MBean to get rack information
	result, err := s.client.ExecuteOperation(
		"org.apache.cassandra.db:type=EndpointSnitchInfo",
		"getRack",
		[]interface{}{endpoint},
		"")
	if err != nil {
		return "", err
	}
	if result.Error != "" {
		return "", fmt.Errorf("jolokia error: %s", result.Error)
	}

	if rack, ok := result.Value.(string); ok {
		return rack, nil
	}

	return "", fmt.Errorf("unexpected response type for getRack: %T", result.Value)
}

var localSystemKeyspaces = map[string]struct{}{
	"system":        {},
	"system_schema": {},
}

func (s *storageMigrationNodeOperations) GetNonLocalKeyspaces() ([]string, error) {
	request := go_jolokia.NewJolokiaRequest(go_jolokia.READ,
		"org.apache.cassandra.db:type=StorageService", nil, "Keyspaces")
	result, err := s.client.ExecuteReadRequest(request)
	if err != nil {
		return nil, fmt.Errorf("failed to get Keyspaces on %s: %w", s.host, err)
	}
	if result.Error != "" {
		return nil, fmt.Errorf("Keyspaces error on %s: %s", s.host, result.Error)
	}

	values, ok := result.Value.([]interface{})
	if !ok {
		return nil, fmt.Errorf("unexpected type for Keyspaces on %s: %T", s.host, result.Value)
	}

	out := make([]string, 0, len(values))
	for _, v := range values {
		ks, ok := v.(string)
		if !ok {
			continue
		}
		if _, isLocal := localSystemKeyspaces[ks]; isLocal {
			continue
		}
		out = append(out, ks)
	}
	return out, nil
}

func (s *storageMigrationNodeOperations) TriggerRepairAsync(primaryRange bool) (string, error) {
	options := map[string]string{
		"primaryRange": fmt.Sprintf("%t", primaryRange),
	}

	// Preferred signature for many versions (incl. yours): repairAsync(String, Map)
	// Some Cassandra versions NPE when keyspace is empty (all keyspaces). If that happens,
	// fall back to repairing non-local keyspaces one by one.
	result, err := s.client.ExecuteOperation(
		"org.apache.cassandra.db:type=StorageService",
		"repairAsync(java.lang.String,java.util.Map)",
		[]interface{}{"", options},
		"")
	if err == nil && result.Error == "" {
		if result.Value == nil {
			return "", nil
		}
		return fmt.Sprintf("%v", result.Value), nil
	}

	// Fallback 1: repairAsync(Map)
	fallback, fbErr := s.client.ExecuteOperation(
		"org.apache.cassandra.db:type=StorageService",
		"repairAsync(java.util.Map)",
		[]interface{}{options},
		"")
	if fbErr == nil && fallback.Error == "" {
		if fallback.Value == nil {
			return "", nil
		}
		return fmt.Sprintf("%v", fallback.Value), nil
	}

	// Fallback 2: per-keyspace repairAsync(String,Map)
	keyspaces, ksErr := s.GetNonLocalKeyspaces()
	if ksErr != nil {
		if err != nil {
			return "", fmt.Errorf("repairAsync failed on %s: %w", s.host, err)
		}
		return "", fmt.Errorf("repairAsync error on %s: %s", s.host, result.Error)
	}
	if len(keyspaces) == 0 {
		return "", fmt.Errorf("repairAsync failed on %s and no keyspaces found to fallback", s.host)
	}

	var lastCmdID string
	for _, ks := range keyspaces {
		r, e := s.client.ExecuteOperation(
			"org.apache.cassandra.db:type=StorageService",
			"repairAsync(java.lang.String,java.util.Map)",
			[]interface{}{ks, options},
			"")
		if e != nil {
			return "", fmt.Errorf("repairAsync(%s) failed on %s: %w", ks, s.host, e)
		}
		if r.Error != "" {
			return "", fmt.Errorf("repairAsync(%s) error on %s: %s", ks, s.host, r.Error)
		}
		if r.Value != nil {
			lastCmdID = fmt.Sprintf("%v", r.Value)
		}
	}

	return lastCmdID, nil
}

func (s *storageMigrationNodeOperations) HasStreamingSessions() (bool, error) {
	// CurrentStreams includes repair streaming and bootstrap streaming.
	request := go_jolokia.NewJolokiaRequest(go_jolokia.READ,
		"org.apache.cassandra.net:type=StreamManager", nil, "CurrentStreams")
	result, err := s.client.ExecuteReadRequest(request)
	if err != nil {
		return false, fmt.Errorf("failed to get CurrentStreams: %w", err)
	}
	if result.Error != "" {
		return false, fmt.Errorf("CurrentStreams error: %s", result.Error)
	}

	streams, ok := result.Value.([]interface{})
	if !ok {
		// In some versions it may return map/other. Treat unknown as "streaming" to be safe.
		if result.Value == nil {
			return false, nil
		}
		return true, nil
	}

	return len(streams) > 0, nil
}

// GetTokenRanges returns token ring information similar to "nodetool ring"
// This shows token ownership across all nodes in the cluster with rack information
// Output is sorted by token (numeric order)
func (s *storageMigrationNodeOperations) GetTokenRanges() (string, error) {
	// Get token ownership information from StorageService
	request := go_jolokia.NewJolokiaRequest(go_jolokia.READ,
		"org.apache.cassandra.db:type=StorageService", nil, "TokenToEndpointMap")
	result, err := s.client.ExecuteReadRequest(request)
	if err != nil {
		return "", fmt.Errorf("failed to get TokenToEndpointMap: %w", err)
	}
	if result.Error != "" {
		return "", fmt.Errorf("TokenToEndpointMap error: %s", result.Error)
	}

	tokenMap, ok := result.Value.(map[string]interface{})
	if !ok {
		return "", fmt.Errorf("unexpected type for TokenToEndpointMap: %T", result.Value)
	}

	// Get rack information for each endpoint
	rackMap := make(map[string]string) // IP -> Rack
	endpointSet := make(map[string]bool)
	for _, endpoint := range tokenMap {
		if ip, ok := endpoint.(string); ok {
			endpointSet[ip] = true
		}
	}

	for ip := range endpointSet {
		rack, err := s.getRackForEndpoint(ip)
		if err != nil {
			rack = "Unknown"
		}
		rackMap[ip] = rack
	}

	// Convert to slice for sorting by token (numeric)
	type tokenEntry struct {
		token    string
		endpoint string
		rack     string
	}
	var entries []tokenEntry
	for token, endpoint := range tokenMap {
		ip := fmt.Sprintf("%v", endpoint)
		entries = append(entries, tokenEntry{
			token:    token,
			endpoint: ip,
			rack:     rackMap[ip],
		})
	}

	// Sort by token (as big integer)
	sort.Slice(entries, func(i, j int) bool {
		// Parse tokens as big integers for proper numeric comparison
		ti := new(big.Int)
		tj := new(big.Int)
		ti.SetString(entries[i].token, 10)
		tj.SetString(entries[j].token, 10)
		return ti.Cmp(tj) < 0
	})

	// Format output similar to nodetool ring
	output := "Token                    Endpoint         Rack\n"
	output += "----------------------------------------------------------------\n"

	for _, entry := range entries {
		output += fmt.Sprintf("%-24s %-16s %s\n", entry.token, entry.endpoint, entry.rack)
	}

	return output, nil
}

// GetNodetoolStatus returns cluster status information similar to "nodetool status"
// This shows each node's state (UN, DN, etc.), rack, load, and token count
// Output is sorted by rack then by IP address
func (s *storageMigrationNodeOperations) GetNodetoolStatus() (string, error) {
	// Get cluster info
	hostIDMap, liveNodes, unreachableNodes, joiningNodes, leavingNodes, movingNodes, err := s.getClusterInfoBatch()
	if err != nil {
		return "", fmt.Errorf("failed to get cluster info: %w", err)
	}

	// Build status map
	statusMap := make(map[string]string)
	liveSet := make(map[string]bool)

	for _, ip := range liveNodes {
		liveSet[ip] = true
		statusMap[ip] = "UN" // Up Normal (default)
	}

	for _, ip := range unreachableNodes {
		statusMap[ip] = "DN" // Down Normal
	}

	for _, ip := range joiningNodes {
		if liveSet[ip] {
			statusMap[ip] = "UJ" // Up Joining
		} else {
			statusMap[ip] = "DJ" // Down Joining
		}
	}

	for _, ip := range leavingNodes {
		if liveSet[ip] {
			statusMap[ip] = "UL" // Up Leaving
		} else {
			statusMap[ip] = "DL" // Down Leaving
		}
	}

	for _, ip := range movingNodes {
		if liveSet[ip] {
			statusMap[ip] = "UM" // Up Moving
		} else {
			statusMap[ip] = "DM" // Down Moving
		}
	}

	// Get load and token count for each node
	type nodeInfo struct {
		ip     string
		status string
		rack   string
		load   string
		tokens int
		hostID string
	}

	var nodes []nodeInfo
	for ip, hostID := range hostIDMap {
		// Get rack
		rack, err := s.getRackForEndpoint(ip)
		if err != nil {
			rack = "Unknown"
		}

		// Get load (data size)
		load := s.getLoadForEndpoint(ip)

		// Get token count
		tokenCount := s.getTokenCountForEndpoint(ip)

		nodes = append(nodes, nodeInfo{
			ip:     ip,
			status: statusMap[ip],
			rack:   rack,
			load:   load,
			tokens: tokenCount,
			hostID: hostID,
		})
	}

	// Sort by rack then by IP
	sort.Slice(nodes, func(i, j int) bool {
		if nodes[i].rack != nodes[j].rack {
			return nodes[i].rack < nodes[j].rack
		}
		return nodes[i].ip < nodes[j].ip
	})

	// Format output similar to nodetool status
	output := "Status=Up/Down |/ State=Normal/Leaving/Joining/Moving\n"
	output += "--  Address          Load       Tokens  Rack     Host ID\n"
	output += "--------------------------------------------------------------------------------\n"

	for _, node := range nodes {
		output += fmt.Sprintf("%-3s %-15s  %-10s %-7d %-8s %s\n",
			node.status, node.ip, node.load, node.tokens, node.rack, node.hostID)
	}

	return output, nil
}

// getLoadForEndpoint gets the data load for a specific endpoint
func (s *storageMigrationNodeOperations) getLoadForEndpoint(ip string) string {
	// Query LoadMap which maps IP -> load in bytes
	request := go_jolokia.NewJolokiaRequest(go_jolokia.READ,
		"org.apache.cassandra.db:type=StorageService", nil, "LoadMap")
	result, err := s.client.ExecuteReadRequest(request)
	if err != nil {
		return "Unknown"
	}
	if result.Error != "" {
		return "Unknown"
	}

	loadMap, ok := result.Value.(map[string]interface{})
	if !ok {
		return "Unknown"
	}

	loadStr, ok := loadMap[ip].(string)
	if !ok {
		return "0 B"
	}

	return loadStr
}

// getTokenCountForEndpoint counts how many tokens an endpoint owns
func (s *storageMigrationNodeOperations) getTokenCountForEndpoint(ip string) int {
	// Query TokenToEndpointMap
	request := go_jolokia.NewJolokiaRequest(go_jolokia.READ,
		"org.apache.cassandra.db:type=StorageService", nil, "TokenToEndpointMap")
	result, err := s.client.ExecuteReadRequest(request)
	if err != nil {
		return 0
	}
	if result.Error != "" {
		return 0
	}

	tokenMap, ok := result.Value.(map[string]interface{})
	if !ok {
		return 0
	}

	count := 0
	for _, endpoint := range tokenMap {
		if endpointStr, ok := endpoint.(string); ok && endpointStr == ip {
			count++
		}
	}
	return count
}
