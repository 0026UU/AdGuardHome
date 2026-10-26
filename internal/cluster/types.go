package cluster

import "time"

// NodeStatus is the public runtime state of one cluster node.
type NodeStatus struct {
	Name          string    `json:"name"`
	APIBase       string    `json:"api_base"`
	Online        bool      `json:"online"`
	LastHeartbeat time.Time `json:"last_heartbeat"`
	RTTMillis     int64     `json:"rtt_ms"`
	Version       string    `json:"version"`
	PendingSync   int       `json:"pending_sync"`
	Degraded      bool      `json:"degraded"`
	Local         bool      `json:"local"`
}

// Status is a secret-free snapshot of the cluster and its nodes.
type Status struct {
	Enabled       bool         `json:"enabled"`
	Mode          Mode         `json:"mode"`
	Name          string       `json:"name"`
	Nodes         []NodeStatus `json:"nodes"`
	DegradedNodes int          `json:"degraded_nodes"`
	Config        *Config      `json:"config"`
}

type nodeState struct {
	status          NodeStatus
	failures        int
	alignmentNeeded bool
}

func cloneConfig(conf *Config) *Config {
	if conf == nil {
		return nil
	}

	clone := *conf
	clone.Members = append([]Member(nil), conf.Members...)

	return &clone
}
