// Package nginx provides a minimal adapter over `nginx -T` output. It is not a
// full nginx parser — it only extracts the facts needed for topology:
//
//   - listen directives (port + protocol)
//   - proxy_pass targets (host:port) and the server block they live in
//
// All parsing is pure over fixture text; the caller decides how to obtain
// `nginx -T` output. Complex cases may be overridden via services.yaml
// topology hints instead of being inferred.
package nginx

import (
	"bufio"
	"regexp"
	"strconv"
	"strings"
)

// Listener is one listen directive.
type Listener struct {
	Port    int
	Address string // bind address, e.g. 0.0.0.0 or empty for default
	Protocol string // tcp | udp
	SSL     bool
}

// ProxyPass is one proxy_pass target within a server block.
type ProxyPass struct {
	// Target is "host:port" or "http://host:port" as written.
	Target string
	// UpstreamHost and UpstreamPort are parsed from Target.
	UpstreamHost string
	UpstreamPort int
	// ServerPort is the listen port of the enclosing server block, or 0.
	ServerPort int
}

// Config is the adapter result.
type Config struct {
	Listeners  []Listener
	ProxyPasses []ProxyPass
}

var (
	listenRe = regexp.MustCompile(`(?m)^\s*listen\s+([^;]+);`)
	proxyRe  = regexp.MustCompile(`(?m)^\s*proxy_pass\s+([^;]+);`)
	urlRe    = regexp.MustCompile(`(?i)^(?:http|https|uwsgi|fastcgi|grpc)://([^:/]+)(?::(\d+))?`)
	hostPortRe = regexp.MustCompile(`^([^:/]+):(\d+)$`)
)

// Parse processes `nginx -T` text.
func Parse(output string) Config {
	var cfg Config

	// Split into blocks: a listen starts a new server block context.
	lines := splitLines(output)
	var currentServerPort int

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if m := listenRe.FindStringSubmatch(trimmed); m != nil {
			addr := strings.TrimSpace(m[1])
			l, ok := parseListen(addr)
			if ok {
				cfg.Listeners = append(cfg.Listeners, l)
				if l.Protocol == "tcp" {
					currentServerPort = l.Port
				}
			}
			continue
		}
		if m := proxyRe.FindStringSubmatch(trimmed); m != nil {
			target := strings.TrimSpace(m[1])
			pp := ProxyPass{Target: target, ServerPort: currentServerPort}
			host, port, ok := parseTarget(target)
			if ok {
				pp.UpstreamHost = host
				pp.UpstreamPort = port
			}
			cfg.ProxyPasses = append(cfg.ProxyPasses, pp)
		}
	}

	return cfg
}

func splitLines(output string) []string {
	var lines []string
	sc := bufio.NewScanner(strings.NewReader(output))
	for sc.Scan() {
		lines = append(lines, sc.Text())
	}
	return lines
}

// parseListen parses a listen directive value, e.g. "443 ssl", "127.0.0.1:18444", "443 udp".
func parseListen(v string) (Listener, bool) {
	fields := strings.Fields(v)
	if len(fields) == 0 {
		return Listener{}, false
	}
	addr := fields[0]
	l := Listener{Protocol: "tcp"}
	for _, f := range fields[1:] {
		switch {
		case f == "ssl":
			l.SSL = true
		case f == "udp":
			l.Protocol = "udp"
		}
	}
	// addr may be "443" or "host:443" or "0.0.0.0:443" or "[::]:443".
	portStr := addr
	host := ""
	if idx := strings.LastIndex(addr, ":"); idx >= 0 {
		host = addr[:idx]
		portStr = addr[idx+1:]
	}
	port, err := strconv.Atoi(strings.TrimSpace(portStr))
	if err != nil {
		return Listener{}, false
	}
	l.Port = port
	l.Address = host
	return l, true
}

// parseTarget extracts host and port from a proxy_pass value.
func parseTarget(v string) (host string, port int, ok bool) {
	v = strings.TrimSpace(strings.TrimSuffix(v, ";"))
	// url form: http://host:port/path
	if m := urlRe.FindStringSubmatch(v); m != nil {
		host = m[1]
		if m[2] != "" {
			p, err := strconv.Atoi(m[2])
			if err != nil {
				return host, 0, false
			}
			return host, p, true
		}
		return host, 0, true // default port, caller decides
	}
	// bare host:port
	if m := hostPortRe.FindStringSubmatch(v); m != nil {
		p, err := strconv.Atoi(m[2])
		if err != nil {
			return "", 0, false
		}
		return m[1], p, true
	}
	return "", 0, false
}
