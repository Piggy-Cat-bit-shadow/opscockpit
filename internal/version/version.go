// Package version carries the OpsCockpit build version.
//
// The values are overridden at build time via:
//
//	go build -ldflags "-X github.com/opscockpit/opscockpit/internal/version.Version=v1.2.3"
package version

import "fmt"

var (
	// Version is the semantic version. Defaults to "dev".
	Version = "dev"
	// Commit is the git commit SHA the binary was built from, if known.
	Commit = "unknown"
	// BuildTime is the RFC3339 build timestamp, if known.
	BuildTime = ""
)

// Info returns a one-line version string.
func Info() string {
	if Commit != "unknown" && len(Commit) > 8 {
		Commit = Commit[:8]
	}
	if BuildTime == "" {
		return fmt.Sprintf("opscockpit %s (%s)", Version, Commit)
	}
	return fmt.Sprintf("opscockpit %s (%s, built %s)", Version, Commit, BuildTime)
}
