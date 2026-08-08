// Package docker inspects local Docker containers via a client interface so
// tests never need a real Docker daemon.
//
// Web serving never touches the Docker socket; only the collect path may talk
// to Docker, and that path is behind an abstraction.
package docker

import (
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

// Container is the minimal container snapshot we expose to state.json.
type Container struct {
	ID             string   `json:"id"`
	Name           string   `json:"name"`
	Image          string   `json:"image"`
	Status         string   `json:"status"`
	PublishedPorts []string `json:"published_ports,omitempty"` // "443/tcp" form
	MemoryBytes    int64    `json:"memory_bytes,omitempty"`
	Labels         map[string]string `json:"labels,omitempty"`
}

// Client abstracts container inspection.
type Client interface {
	// List returns all containers with their runtime facts.
	List(ctx context.Context) ([]Container, error)
}

// CLI runs `docker ps -a --no-trunc --format {{json .}}` per container.
type CLI struct {
	// Docker is the docker binary path; empty means "docker" on PATH.
	Docker string
}

// ExecClient is a thin docker-CLI client. It is intentionally simple: one
// `docker ps` listing, then one `docker inspect` per container for ports,
// memory and labels. On hosts without Docker this returns no containers and no
// error, so Docker never makes OpsCockpit fail.
type ExecClient struct {
	// Command runs argv; overridable for tests.
	Command func(ctx context.Context, argv []string) (string, error)
}

func (c ExecClient) run(ctx context.Context, argv []string) (string, error) {
	if c.Command != nil {
		return c.Command(ctx, argv)
	}
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// List returns containers via `docker ps`.
func (c ExecClient) List(ctx context.Context) ([]Container, error) {
	out, err := c.run(ctx, []string{"docker", "ps", "-a", "--no-trunc", "--format", "{{.ID}}|{{.Names}}|{{.Image}}|{{.Status}}"})
	if err != nil {
		return nil, nil // Docker unavailable → no containers, no error
	}
	var containers []Container
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "|", 4)
		if len(parts) < 4 {
			continue
		}
		ctn := Container{
			ID:     strings.TrimSpace(parts[0]),
			Name:   strings.TrimSpace(parts[1]),
			Image:  strings.TrimSpace(parts[2]),
			Status: strings.TrimSpace(parts[3]),
		}
		// Enrich with ports/memory/labels via inspect (best effort).
		if insp, ierr := c.inspect(ctx, ctn.ID); ierr == nil {
			ctn.PublishedPorts = insp.PublishedPorts
			ctn.MemoryBytes = insp.MemoryBytes
			ctn.Labels = insp.Labels
		}
		containers = append(containers, ctn)
	}
	return containers, nil
}

func (c ExecClient) inspect(ctx context.Context, id string) (Container, error) {
	out, err := c.run(ctx, []string{
		"docker", "inspect", id,
		"--format", `{{json .HostConfig.PortBindings}}|{{json .HostConfig.Memory}}|{{json .Config.Labels}}`,
	})
	if err != nil {
		return Container{}, err
	}
	res := Container{}
	parts := strings.SplitN(strings.TrimSpace(out), "|", 3)
	if len(parts) >= 1 {
		res.PublishedPorts = parsePortBindings(parts[0])
	}
	if len(parts) >= 2 {
		if v, err := strconv.ParseInt(strings.TrimSpace(parts[1]), 10, 64); err == nil {
			res.MemoryBytes = v
		}
	}
	if len(parts) >= 3 {
		res.Labels = parseLabels(parts[2])
	}
	return res, nil
}

// parsePortBindings turns a Go-map JSON string into "port/proto" strings.
func parsePortBindings(s string) []string {
	var out []string
	s = strings.TrimSpace(s)
	if s == "" || s == "{}" || s == "null" {
		return out
	}
	// {"443/tcp":[{"HostIp":"","HostPort":"443"}]}
	s = strings.Trim(s, "{}")
	for _, entry := range splitCommaNotInBrackets(s) {
		kv := strings.SplitN(entry, ":", 2)
		if len(kv) != 2 {
			continue
		}
		key := strings.Trim(kv[0], "\"")
		if strings.Contains(key, "/") {
			out = append(out, key)
		}
	}
	return out
}

func splitCommaNotInBrackets(s string) []string {
	var parts []string
	depth := 0
	start := 0
	for i, r := range s {
		switch r {
		case '[':
			depth++
		case ']':
			depth--
		case ',':
			if depth == 0 {
				parts = append(parts, s[start:i])
				start = i + 1
			}
		}
	}
	parts = append(parts, s[start:])
	return parts
}

// parseLabels parses a JSON object into a string map.
func parseLabels(s string) map[string]string {
	m := map[string]string{}
	s = strings.TrimSpace(s)
	if s == "" || s == "{}" || s == "null" {
		return m
	}
	s = strings.Trim(s, "{}")
	for _, entry := range splitCommaNotInBrackets(s) {
		kv := strings.SplitN(entry, ":", 2)
		if len(kv) != 2 {
			continue
		}
		key := strings.Trim(kv[0], "\"")
		val := strings.Trim(kv[1], "\"")
		m[key] = val
	}
	return m
}

// Helper for tests: NewExecClient with a canned command output.
func (c ExecClient) String() string { return fmt.Sprintf("docker-exec-client") }
