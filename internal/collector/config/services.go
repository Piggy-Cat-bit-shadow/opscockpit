// Package services loads the services.yaml registry.
//
// services.yaml is NOT a topology definition file. It only declares which
// business services are worth showing and gives the collectors hints: a systemd
// unit name, config path overrides, a version command, restart permission, and
// optional health requirements. Runtime truth (listeners, memory, status)
// always comes from the Linux runtime, never from this file.
package services

import (
	"errors"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Config is the root of services.yaml.
type Config struct {
	Services []Service `yaml:"services"`
}

// Service declares one business service worth showing.
type Service struct {
	ID             string         `yaml:"id"`
	Name           string         `yaml:"name"`
	Systemd        *SystemdConfig `yaml:"systemd,omitempty"`
	ConfigPaths    []string       `yaml:"config_paths,omitempty"`
	Version        *VersionConfig `yaml:"version,omitempty"`
	RestartEnabled bool           `yaml:"restart_enabled"`
	Health         *HealthConfig  `yaml:"health,omitempty"`
	Exposure       *ExposureConfig `yaml:"exposure,omitempty"`

	// StatusHint is populated by the collector at runtime (not from YAML).
	// It feeds topology node status dots. Not part of the config file.
	StatusHint string `yaml:"-"`
}

// ExposureConfig overrides runtime exposure classification for a service.
// Default mode is "auto" (runtime evidence decides); most services never set
// this. Fields are only used to override when the runtime evidence is
// ambiguous or wrong for a specific host.
type ExposureConfig struct {
	// Mode is one of: auto (default), public, internal, nat-target.
	//   auto       — runtime firewall + NAT evidence decides.
	//   public     — force this service's listeners to be treated as public
	//                even if firewall evidence is unknown/deny.
	//   internal   — force this service's listeners internal (never public).
	//   nat-target — a listener that is a NAT REDIRECT target must NOT become
	//                a top-level port on its own (see NAT target suppression).
	Mode string `yaml:"mode"`
	// ForceDirectPublic, when true, explicitly promotes a NAT-target listener
	// to a direct public top-level port in addition to its NAT ingress.
	ForceDirectPublic bool `yaml:"force_direct_public"`
	// ExposeDirect is an alias for ForceDirectPublic.
	ExposeDirect bool `yaml:"expose_direct"`
}

// SystemdConfig carries the unit name for a service.
type SystemdConfig struct {
	Unit string `yaml:"unit"`
}

// VersionConfig describes how to read the service's version. Command is an argv
// vector — never a shell string. Timeout bounds the execution.
type VersionConfig struct {
	Command []string     `yaml:"command"`
	Timeout DurationOpts `yaml:"timeout"`
}

// HealthConfig carries optional health hints.
type HealthConfig struct {
	// RequiredListeners, when set, must all be found in the runtime listener
	// set for the service to be considered healthy. An active unit with a
	// missing required listener is a failed service.
	RequiredListeners []ListenerRequirement `yaml:"required_listeners,omitempty"`
}

// ListenerRequirement names a listener the service must be bound to.
type ListenerRequirement struct {
	Port     int    `yaml:"port"`
	Protocol string `yaml:"protocol"`
}

// DurationOpts is a YAML duration that accepts "5s", "1m", "500ms" or a bare
// number of seconds.
type DurationOpts time.Duration

// UnmarshalYAML implements yaml.Unmarshaler.
func (d *DurationOpts) UnmarshalYAML(value *yaml.Node) error {
	if value.Kind != yaml.ScalarNode {
		return fmt.Errorf("duration must be a scalar, got kind %d", value.Kind)
	}
	s := strings.TrimSpace(value.Value)
	if s == "" {
		*d = 0
		return nil
	}
	if n, err := parseSeconds(s); err == nil {
		*d = DurationOpts(n * float64(time.Second))
		return nil
	}
	dur, err := time.ParseDuration(s)
	if err != nil {
		return fmt.Errorf("invalid duration %q: %w", s, err)
	}
	*d = DurationOpts(dur)
	return nil
}

func parseSeconds(s string) (float64, error) {
	// Reject obvious non-numbers quickly (e.g. "5s") by scanning the leading
	// float and confirming it consumed the whole string.
	end := 0
	var f float64
	for _, r := range s {
		if (r >= '0' && r <= '9') || r == '.' || r == '-' || r == '+' || r == 'e' || r == 'E' {
			end++
			continue
		}
		break
	}
	if end == 0 || end != len(s) {
		return 0, errors.New("not a plain number")
	}
	var err error
	f, err = strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, err
	}
	return f, nil
}

// Load reads and parses a services.yaml file from disk.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read services config %s: %w", path, err)
	}
	return Parse(data)
}

// Parse parses services.yaml content.
func Parse(data []byte) (*Config, error) {
	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse services config: %w", err)
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// Validate checks structural invariants. It does not talk to the runtime.
func (c *Config) Validate() error {
	seen := make(map[string]bool, len(c.Services))
	for i, s := range c.Services {
		if s.ID == "" {
			return fmt.Errorf("services[%d]: id is required", i)
		}
		if !validID(s.ID) {
			return fmt.Errorf("services[%d]: invalid id %q (allowed: a-z0-9._-)", i, s.ID)
		}
		if s.Name == "" {
			return fmt.Errorf("services[%d] %q: name is required", i, s.ID)
		}
		if seen[s.ID] {
			return fmt.Errorf("services[%d]: duplicate service id %q", i, s.ID)
		}
		seen[s.ID] = true
		if s.Systemd != nil && s.Systemd.Unit == "" {
			return fmt.Errorf("services[%d] %q: systemd.unit must not be empty", i, s.ID)
		}
		if s.Version != nil {
			if len(s.Version.Command) == 0 {
				return fmt.Errorf("services[%d] %q: version.command must not be empty", i, s.ID)
			}
			if s.Version.Timeout < 0 {
				return fmt.Errorf("services[%d] %q: version.timeout must be >= 0", i, s.ID)
			}
		}
		if s.Health != nil {
			for _, r := range s.Health.RequiredListeners {
				if r.Port <= 0 || r.Port > 65535 {
					return fmt.Errorf("services[%d] %q: health.required_listeners has invalid port %d", i, s.ID, r.Port)
				}
				if r.Protocol != "tcp" && r.Protocol != "udp" {
					return fmt.Errorf("services[%d] %q: health.required_listeners protocol %q must be tcp or udp", i, s.ID, r.Protocol)
				}
			}
		}
		if s.Exposure != nil {
			switch s.Exposure.Mode {
			case "", "auto", "public", "internal", "nat-target":
			default:
				return fmt.Errorf("services[%d] %q: exposure.mode %q invalid (auto|public|internal|nat-target)", i, s.ID, s.Exposure.Mode)
			}
		}
	}
	return nil
}

func validID(id string) bool {
	if id == "" {
		return false
	}
	for _, r := range id {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '.', r == '_', r == '-':
		default:
			return false
		}
	}
	return true
}

// ByID returns the service with the given id, or nil.
func (c *Config) ByID(id string) *Service {
	for i := range c.Services {
		if c.Services[i].ID == id {
			return &c.Services[i]
		}
	}
	return nil
}

// SortedIDs returns the service ids in stable, sorted order.
func (c *Config) SortedIDs() []string {
	ids := make([]string, 0, len(c.Services))
	for _, s := range c.Services {
		ids = append(ids, s.ID)
	}
	sort.Strings(ids)
	return ids
}

// Unit returns the systemd unit for a service, or "".
func (s *Service) Unit() string {
	if s.Systemd == nil {
		return ""
	}
	return s.Systemd.Unit
}

// VersionCommand returns the argv for the version command, or nil.
func (s *Service) VersionCommand() []string {
	if s.Version == nil {
		return nil
	}
	return s.Version.Command
}

// VersionTimeout returns the configured version timeout, defaulting to 5s.
func (s *Service) VersionTimeout() time.Duration {
	if s.Version != nil && s.Version.Timeout > 0 {
		return time.Duration(s.Version.Timeout)
	}
	return 5 * time.Second
}

// FirstConfigPath returns the first declared config path, or "".
func (s *Service) FirstConfigPath() string {
	if len(s.ConfigPaths) == 0 {
		return ""
	}
	return s.ConfigPaths[0]
}

// ExposureMode returns the configured exposure mode ("auto" when unset).
func (s *Service) ExposureMode() string {
	if s.Exposure == nil || s.Exposure.Mode == "" {
		return "auto"
	}
	return s.Exposure.Mode
}

// ForceDirectPublic reports whether this service's NAT-target listeners must
// also appear as direct public top-level ports.
func (s *Service) ForceDirectPublic() bool {
	if s.Exposure == nil {
		return false
	}
	return s.Exposure.ForceDirectPublic || s.Exposure.ExposeDirect
}

// DefaultConfig returns the default services.yaml content used as an example.
func DefaultConfig() []byte {
	return []byte(`services:

  - id: nginx
    name: Nginx
    systemd:
      unit: nginx.service
    config_paths:
      - /etc/nginx/nginx.conf
    restart_enabled: true

  - id: hysteria2
    name: Hysteria2
    systemd:
      unit: hysteria-server.service
    config_paths:
      - /etc/hysteria/config.yaml
    version:
      command:
        - /usr/local/bin/hysteria
        - version
      timeout: 5s
    restart_enabled: true

  - id: xray
    name: Xray
    systemd:
      unit: xray.service
    config_paths:
      - /etc/xray/config.json
    version:
      command:
        - /usr/local/bin/xray
        - version
    restart_enabled: true
`)
}
