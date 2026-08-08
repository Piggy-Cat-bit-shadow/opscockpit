package nginx

import "testing"

const nginxFixture = `# configuration file /etc/nginx/nginx.conf:
http {
  server {
    listen 80 default_server;
    listen [::]:80 default_server;
    server_name _;
    return 444;
  }
}

# configuration file /etc/nginx/sites-enabled/proxy.conf:
server {
    listen 443 ssl;
    listen [::]:443 ssl;
    server_name example.com;

    location / {
        proxy_pass http://127.0.0.1:18444;
        proxy_set_header Host $host;
    }
}

server {
    listen 443 udp;
    listen [::]:443 udp;
    server_name _;
}

stream {
    server {
        listen 853 udp;
        proxy_pass 127.0.0.1:853;
    }
}
`

func TestParseNginxFixture(t *testing.T) {
	cfg := Parse(nginxFixture)

	if len(cfg.Listeners) == 0 {
		t.Fatal("no listeners parsed")
	}

	// Assert specific listeners exist.
	assertHasListener(t, cfg, 80, "tcp")
	assertHasListener(t, cfg, 443, "tcp")
	assertHasListener(t, cfg, 443, "udp")
	assertHasListener(t, cfg, 853, "udp")

	// The proxy_pass to 127.0.0.1:18444 must be captured with the enclosing
	// server block's port (443).
	found := false
	for _, pp := range cfg.ProxyPasses {
		if pp.UpstreamHost == "127.0.0.1" && pp.UpstreamPort == 18444 {
			found = true
			if pp.ServerPort != 443 {
				t.Errorf("proxy_pass ServerPort = %d, want 443", pp.ServerPort)
			}
		}
	}
	if !found {
		t.Error("proxy_pass 127.0.0.1:18444 not found")
	}
}

func assertHasListener(t *testing.T, cfg Config, port int, proto string) {
	t.Helper()
	for _, l := range cfg.Listeners {
		if l.Port == port && l.Protocol == proto {
			return
		}
	}
	t.Errorf("listener %d/%s not found in %+v", port, proto, cfg.Listeners)
}

func TestParseListen(t *testing.T) {
	l, ok := parseListen("443 ssl")
	if !ok || l.Port != 443 || l.Protocol != "tcp" || !l.SSL {
		t.Errorf("parseListen('443 ssl') = %+v, %v", l, ok)
	}
	l, ok = parseListen("127.0.0.1:18444")
	if !ok || l.Port != 18444 || l.Address != "127.0.0.1" {
		t.Errorf("parseListen('127.0.0.1:18444') = %+v, %v", l, ok)
	}
	l, ok = parseListen("443 udp")
	if !ok || l.Port != 443 || l.Protocol != "udp" {
		t.Errorf("parseListen('443 udp') = %+v, %v", l, ok)
	}
	if _, ok := parseListen(""); ok {
		t.Error("empty listen should fail")
	}
}

func TestParseTarget(t *testing.T) {
	cases := []struct {
		in   string
		host string
		port int
		ok   bool
	}{
		{"http://127.0.0.1:18444", "127.0.0.1", 18444, true},
		{"http://127.0.0.1:18444/", "127.0.0.1", 18444, true},
		{"127.0.0.1:853", "127.0.0.1", 853, true},
		{"unix:/run/foo.sock", "", 0, false},
	}
	for _, c := range cases {
		host, port, ok := parseTarget(c.in)
		if ok != c.ok || host != c.host || port != c.port {
			t.Errorf("parseTarget(%q) = %q,%d,%v want %q,%d,%v", c.in, host, port, ok, c.host, c.port, c.ok)
		}
	}
}
