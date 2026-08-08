//go:build !linux

package host

import (
	"errors"
	"time"
)

// statfsT is a stub statfs result used only to keep the signature stable on
// non-linux platforms (fixture-only there).
type statfsT struct {
	Blocks int64
	Bfree  int64
	Bsize  int64
}

var errNotSupported = errors.New("statfs not supported on this platform")

func statfs(mount string) (statfsT, error) {
	return statfsT{}, errNotSupported
}

func timeSleep(ms int) { time.Sleep(time.Duration(ms) * time.Millisecond) }
