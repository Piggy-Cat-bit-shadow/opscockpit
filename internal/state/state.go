// Package state defines the state.json schema (the Runtime Digital Twin) and
// provides validation, an atomic writer, and stale detection.
//
// state.json only ever holds the current runtime state. It is not a database
// and keeps no history. The schema deliberately contains no field that could
// carry credentials: no password, token, secret, uuid, private key, api key,
// cookie, or credential field exists anywhere in the model.
package state

import (
	"time"
)

// SchemaVersion is bumped when the JSON layout changes incompatibly.
const SchemaVersion = 1

// Overall health statuses.
const (
	StatusHealthy  = "healthy"
	StatusWarning  = "warning"
	StatusFailed   = "failed"
	StatusUnknown  = "unknown"
	StatusStale    = "stale"
)

// State is the root of state.json.
type State struct {
	SchemaVersion    int       `json:"schema_version"`
	GeneratedAt      time.Time `json:"generated_at"`
	CollectorVersion string    `json:"collector_version"`
	CollectDurationMs int64    `json:"collect_duration_ms"`

	Host     Host     `json:"host"`
	Services []Service `json:"services"`
	Health   Health   `json:"health"`
	Topology Topology `json:"topology"`
}

// Host is the machine-level snapshot.
type Host struct {
	Hostname      string  `json:"hostname"`
	UptimeSeconds float64 `json:"uptime_seconds"`
	CPU           CPUInfo `json:"cpu"`
	Memory        MemInfo `json:"memory"`
	Swap          MemInfo `json:"swap"`
	Disk          DiskInfo `json:"disk"`
	Load          LoadInfo `json:"load"`
}

// CPUInfo is a CPU snapshot.
type CPUInfo struct {
	Cores   int     `json:"cores"`
	Percent float64 `json:"percent"` // 0..100 over the collection window
}

// MemInfo is a memory snapshot in bytes.
type MemInfo struct {
	Total   int64   `json:"total_bytes"`
	Used    int64   `json:"used_bytes"`
	Percent float64 `json:"percent"` // 0..100
}

// DiskInfo is a filesystem snapshot for the root mount.
type DiskInfo struct {
	MountPoint string  `json:"mount_point"`
	Total      int64   `json:"total_bytes"`
	Used       int64   `json:"used_bytes"`
	Percent    float64 `json:"percent"` // 0..100
}

// LoadInfo is a load-average snapshot.
type LoadInfo struct {
	Load1  float64 `json:"load1"`
	Load5  float64 `json:"load5"`
	Load15 float64 `json:"load15"`
}

// Service is the runtime digital-twin entry for one registered business
// service. Runtime truth (unit state, memory, listeners) comes from the
// collectors; name, unit and restart permission come from services.yaml.
type Service struct {
	ID             string       `json:"id"`
	Name           string       `json:"name"`
	Status         string       `json:"status"`
	Unit           string       `json:"unit,omitempty"`
	UnitState      string       `json:"unit_state,omitempty"`
	Version        string       `json:"version,omitempty"`
	Memory         *MemoryInfo  `json:"memory,omitempty"`
	ConfigPath     string       `json:"config_path,omitempty"`
	ConfigExists   bool         `json:"config_exists"`
	RestartEnabled bool         `json:"restart_enabled"`
	Listeners      []Listener   `json:"listeners,omitempty"`
	Health         *HealthInfo  `json:"health,omitempty"`
}

// MemoryInfo is a per-service memory snapshot.
type MemoryInfo struct {
	RSSBytes int64  `json:"rss_bytes"`
	Source   string `json:"source"` // cgroup_memory_current | proc_rss
}

// HealthInfo records why a service reached its status.
type HealthInfo struct {
	// Problems is a list of human-readable reasons, kept short.
	Problems []string `json:"problems,omitempty"`
	// Details can carry extra structured info (e.g. a required listener found).
	Details []string `json:"details,omitempty"`
}

// Listener is one runtime socket owned by a service.
type Listener struct {
	Protocol string `json:"protocol"` // tcp | udp
	Port     int    `json:"port"`
	Address  string `json:"address"` // bind address, e.g. 0.0.0.0, ::, 127.0.0.1
	Internal bool   `json:"internal"`
	PID      int    `json:"pid,omitempty"`
	Process  string `json:"process,omitempty"`
}

// Health is the machine-wide health summary.
type Health struct {
	Status   string `json:"status"`
	Stale    bool   `json:"stale"`
	AgeSeconds int64 `json:"age_seconds"`

	ServicesHealthy int    `json:"services_healthy"`
	ServicesWarning int    `json:"services_warning"`
	ServicesFailed  int    `json:"services_failed"`
	ServicesUnknown int    `json:"services_unknown"`
	Message         string `json:"message,omitempty"`
}

// Topology is the port-centric runtime topology.
type Topology struct {
	Nodes []Node `json:"nodes"`
	Edges []Edge `json:"edges"`
}

// NodeType is the topological node kind. The frontend only understands these
// four kinds plus the internet root; it knows nothing about individual
// services.
const (
	NodeInternet  = "internet"
	NodePort      = "port"
	NodeProtocol  = "protocol"
	NodeService   = "service"
)

// Node is one node in the port tree.
type Node struct {
	ID        string `json:"id"`
	Type      string `json:"type"`
	Label     string `json:"label"`
	ServiceID string `json:"service_id,omitempty"`
	Protocol  string `json:"protocol,omitempty"`
	Port      int    `json:"port,omitempty"`
	Status    string `json:"status,omitempty"`
}

// Edge is one directed edge in the port tree.
type Edge struct {
	ID       string    `json:"id"`
	Source   string    `json:"source"`
	Target   string    `json:"target"`
	Evidence *Evidence `json:"evidence,omitempty"`
}

// Evidence records why an edge exists. "Source" here is the evidence kind
// (runtime_listener, systemd, nginx_proxy_pass, docker_port, manual_override),
// not the edge's source node.
type Evidence struct {
	Source     string `json:"source"`
	Confidence string `json:"confidence"` // confirmed | configured | inferred
}

// Evidence kinds and confidence levels.
const (
	EvidenceRuntimeListener = "runtime_listener"
	EvidenceSystemd         = "systemd"
	EvidenceNginxProxyPass  = "nginx_proxy_pass"
	EvidenceDockerPort      = "docker_port"
	EvidenceManualOverride  = "manual_override"
	EvidenceDependency      = "dependency"

	ConfidenceConfirmed = "confirmed"
	ConfidenceConfigured = "configured"
	ConfidenceInferred   = "inferred"
)

// Now returns the current UTC time, exposed so tests can stub time.
var Now = func() time.Time { return time.Now().UTC() }

// New returns a State with the schema version and collector version stamped.
func New(collectorVersion string) *State {
	return &State{
		SchemaVersion:    SchemaVersion,
		GeneratedAt:      Now(),
		CollectorVersion: collectorVersion,
	}
}
