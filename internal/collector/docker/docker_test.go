package docker

import (
	"context"
	"reflect"
	"strings"
	"testing"
)

// mockCommand returns canned output keyed by the command name.
func mockClient(ps string, inspect map[string]string) ExecClient {
	return ExecClient{
		Command: func(ctx context.Context, argv []string) (string, error) {
			switch argv[1] {
			case "ps":
				return ps, nil
			case "inspect":
				id := argv[2]
				return inspect[id], nil
			}
			return "", nil
		},
	}
}

func TestListParsesContainers(t *testing.T) {
	ps := "abc123|adguard|adguard/adguardhome:latest|Up 3 days\n" +
		"def456|xray|xray:v1|Up 2 hours\n"
	insp := map[string]string{
		"abc123": `{"443/tcp":[{"HostIp":"0.0.0.0","HostPort":"443"}]}|0|{"com.example":"one"}`,
		"def456": `{}|0|{}`,
	}
	client := mockClient(ps, insp)
	containers, err := client.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(containers) != 2 {
		t.Fatalf("got %d containers", len(containers))
	}
	ad := containers[0]
	if ad.Name != "adguard" || ad.Image != "adguard/adguardhome:latest" {
		t.Errorf("adguard = %+v", ad)
	}
	if !reflect.DeepEqual(ad.PublishedPorts, []string{"443/tcp"}) {
		t.Errorf("ports = %v", ad.PublishedPorts)
	}
	if ad.Labels["com.example"] != "one" {
		t.Errorf("labels = %v", ad.Labels)
	}
}

func TestDockerUnavailableNoError(t *testing.T) {
	client := ExecClient{
		Command: func(ctx context.Context, argv []string) (string, error) {
			return "", &execError{}
		},
	}
	containers, err := client.List(context.Background())
	if err != nil {
		t.Fatalf("docker unavailable must not fail the collector: %v", err)
	}
	if len(containers) != 0 {
		t.Errorf("expected no containers, got %v", containers)
	}
}

type execError struct{}

func (e *execError) Error() string { return "docker not found" }

func TestParsePortBindings(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{`{"443/tcp":[{"HostIp":"0.0.0.0","HostPort":"443"}]}`, []string{"443/tcp"}},
		{`{"853/tcp":[{"HostIp":"0.0.0.0","HostPort":"853"}],"853/udp":[{"HostIp":"0.0.0.0","HostPort":"853"}]}`, []string{"853/tcp", "853/udp"}},
		{"{}", nil},
		{"", nil},
	}
	for _, c := range cases {
		got := parsePortBindings(c.in)
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("parsePortBindings(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestParseLabels(t *testing.T) {
	got := parseLabels(`{"a":"1","b":"2"}`)
	if got["a"] != "1" || got["b"] != "2" {
		t.Errorf("labels = %v", got)
	}
	if !strings.Contains(`{"a":"1","b":"2"}`, "a") {
		t.Fatal("sanity")
	}
}

func TestSplitCommaNotInBrackets(t *testing.T) {
	parts := splitCommaNotInBrackets(`"443/tcp":[{"HostIp":"0.0.0.0"}],"853/tcp":[{"HostIp":"0.0.0.0"}]`)
	if len(parts) != 2 {
		t.Fatalf("parts = %v", parts)
	}
}
