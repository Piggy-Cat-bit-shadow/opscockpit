// Package nginx provides a minimal adapter over `nginx -T` output. It is not a
// full nginx parser — it only extracts the facts needed for topology:
//
//   - listen directives (port + protocol)
//   - named upstream blocks (upstream name { server host:port; })
//   - proxy_pass targets, including named upstreams and `$var` map backends
//   - map $var $name { key upstream; } variable → upstream tables
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
	Port     int
	Address  string // bind address, e.g. 0.0.0.0 or empty for default
	Protocol string // tcp | udp
	SSL      bool
}

// ProxyPass is one proxy_pass target within a server block.
type ProxyPass struct {
	// Target is "host:port", "http://host:port", a named upstream, or "$var".
	Target string
	// UpstreamHost and UpstreamPort are parsed from Target when it is a
	// literal endpoint.
	UpstreamHost string
	UpstreamPort int
	// UpstreamName is set when Target is a named upstream (or $var).
	UpstreamName string
	// ServerPort is the listen port of the enclosing server block, or 0.
	ServerPort int
	// Protocol is tcp (stream) or http (http blocks).
	Protocol string
}

// Upstream is one named upstream block.
type Upstream struct {
	Name    string
	Servers []string // "host:port" strings
}

// Config is the adapter result.
type Config struct {
	Listeners   []Listener
	ProxyPasses []ProxyPass
	Upstreams   []Upstream
	// Maps maps $var → list of upstream names / endpoints it can select.
	Maps map[string][]string
}

var (
	listenRe    = regexp.MustCompile(`(?m)^\s*listen\s+([^;]+);`)
	proxyRe     = regexp.MustCompile(`(?m)^\s*proxy_pass\s+([^;]+);`)
	urlRe       = regexp.MustCompile(`(?i)^(?:http|https|uwsgi|fastcgi|grpc)://([^:/]+)(?::(\d+))?`)
	hostPortRe  = regexp.MustCompile(`^([^:/]+):(\d+)$`)
	upstreamRe  = regexp.MustCompile(`(?m)^\s*upstream\s+(\S+)\s*\{`)
	serverRe    = regexp.MustCompile(`(?m)^\s*server\s+(\S+);`)
	// header is "$ssl_preread_server_name $backend" (the `map` keyword and
	// trailing `{` are stripped by block extraction).
	mapHeadRe   = regexp.MustCompile(`^\s*(\$\S+)\s+(\$\S+)\s*$`)
	mapEntryRe  = regexp.MustCompile(`(?m)^\s*(\S+)\s+(\$\S+|\S+);`)
	blockOpenRe = regexp.MustCompile(`^(\w+)\s+`)
)

// Parse processes `nginx -T` text.
func Parse(output string) Config {
	cfg := Config{Maps: map[string][]string{}}

	// --- Pass 1: extract named upstreams and map tables (block-scoped). ---
	blocks := extractBlocks(output)
	for _, b := range blocks {
		switch {
		case b.name == "upstream":
			up := Upstream{Name: b.header}
			for _, line := range b.lines {
				if m := serverRe.FindStringSubmatch(line); m != nil {
					up.Servers = append(up.Servers, m[1])
				}
			}
			cfg.Upstreams = append(cfg.Upstreams, up)
		case b.name == "map":
			// map $ssl_preread_server_name $backend { key value; }
			if m := mapHeadRe.FindStringSubmatch(b.header); m != nil {
				dest := m[2]
				for _, line := range b.lines {
					entry := mapEntryRe.FindStringSubmatch(line)
					if entry == nil {
						continue
					}
					key := entry[1]
					val := entry[2]
					if key == "default" || strings.HasPrefix(key, "default") {
						cfg.Maps[dest] = append(cfg.Maps[dest], val)
						continue
					}
					cfg.Maps[dest] = append(cfg.Maps[dest], val)
				}
			}
		}
	}
	upstreamByName := map[string]*Upstream{}
	for i := range cfg.Upstreams {
		upstreamByName[cfg.Upstreams[i].Name] = &cfg.Upstreams[i]
	}

	// --- Pass 2: listen + proxy_pass lines. ---
	lines := splitLines(output)
	currentServerPort := 0
	inStream := false
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
			pp.Protocol = "http"
			if inStream {
				pp.Protocol = "stream"
			}
			if isVariable(target) {
				pp.UpstreamName = target
			} else if up, ok := upstreamByName[target]; ok && up != nil {
				pp.UpstreamName = target
			} else {
				host, port, ok := parseTarget(target)
				if ok {
					pp.UpstreamHost = host
					pp.UpstreamPort = port
				}
			}
			cfg.ProxyPasses = append(cfg.ProxyPasses, pp)
		}
		if trimmed == "stream {" {
			inStream = true
		} else if trimmed == "}" {
			inStream = false
		}
	}

	return cfg
}

// block is one named nginx block (upstream, map, server, stream, http, ...).
type block struct {
	name   string
	header string
	lines  []string
}

// extractBlocks finds named blocks (`name ... { ... }`) at any nesting depth
// and returns their header + inner lines. Every block opener is found
// independently and its body matched by brace counting, so upstream/map blocks
// nested inside http/stream are captured.
func extractBlocks(output string) []block {
	var blocks []block
	lines := splitLines(output)
	for i := 0; i < len(lines); i++ {
		line := strings.TrimSpace(lines[i])
		if !strings.HasSuffix(line, "{") {
			continue
		}
		name, header := blockOpen(line)
		if name == "" {
			continue
		}
		inner, _ := consumeBlock(lines, i+1)
		blocks = append(blocks, block{name: name, header: header, lines: inner})
	}
	return blocks
}

// consumeBlock collects lines from `from` until the enclosing brace closes.
func consumeBlock(lines []string, from int) (inner []string, next int) {
	depth := 1
	j := from
	for ; j < len(lines); j++ {
		t := strings.TrimSpace(lines[j])
		depth += strings.Count(t, "{") - strings.Count(t, "}")
		if depth <= 0 {
			return inner, j + 1
		}
		inner = append(inner, t)
	}
	return inner, j
}

func blockOpen(line string) (name, header string) {
	// line like "upstream web_backend {" or "map $x $y {" or "server {"
	m := blockOpenRe.FindStringSubmatch(line)
	if m == nil {
		return "", ""
	}
	name = m[1]
	header = strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(line, m[0]), "{"))
	return name, header
}

// isVariable reports whether a proxy_pass target is a $variable (map-selected
// backend), never a concrete endpoint.
func isVariable(target string) bool {
	return strings.HasPrefix(target, "$")
}

// ResolveProxyTargets expands each ProxyPass to concrete endpoints, following
// named upstreams and $var map tables. Returns a list of "host:port" endpoints.
// A $var with no resolvable map entries yields no endpoint (never invented).
func (cfg Config) ResolveProxyTargets() map[int][]string {
	out := map[int][]string{} // server port → endpoints
	for _, pp := range cfg.ProxyPasses {
		var endpoints []string
		switch {
		case pp.UpstreamHost != "" && pp.UpstreamPort > 0:
			endpoints = []string{joinHostPort(pp.UpstreamHost, pp.UpstreamPort)}
		case pp.UpstreamName != "":
			if isVariable(pp.UpstreamName) {
				// $var → look up map table values.
				for _, val := range cfg.Maps[pp.UpstreamName] {
					if isVariable(val) {
						// chained variable: skip (would need another lookup)
						continue
					}
					if up := cfg.findUpstream(val); up != "" {
						endpoints = append(endpoints, cfg.upstreamServers(up)...)
					} else if h, p, ok := parseTarget(val); ok && p > 0 {
						endpoints = append(endpoints, joinHostPort(h, p))
					}
				}
			} else {
				endpoints = append(endpoints, cfg.upstreamServers(pp.UpstreamName)...)
			}
		}
		if len(endpoints) > 0 {
			out[pp.ServerPort] = append(out[pp.ServerPort], endpoints...)
		}
	}
	// Deduplicate.
	for k := range out {
		out[k] = dedupStrings(out[k])
	}
	return out
}

func (cfg Config) findUpstream(name string) string {
	for _, u := range cfg.Upstreams {
		if u.Name == name {
			return name
		}
	}
	return ""
}

func (cfg Config) upstreamServers(name string) []string {
	for _, u := range cfg.Upstreams {
		if u.Name == name {
			return u.Servers
		}
	}
	return nil
}

func dedupStrings(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		if seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}

func joinHostPort(host string, port int) string {
	if strings.Contains(host, ":") && !strings.HasPrefix(host, "[") {
		return "[" + host + "]:" + strconv.Itoa(port)
	}
	return host + ":" + strconv.Itoa(port)
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
