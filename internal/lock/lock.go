// Package lock provides a single-flight advisory lock so at most one collect
// runs at a time. A systemd timer may trigger collect while a restart-triggered
// collect is in flight; two collectors writing state.json.tmp concurrently
// would corrupt each other. The lock ensures the second either waits briefly or
// exits cleanly.
package lock

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// ErrBusy is returned when the lock is held and the wait budget is exceeded.
var ErrBusy = errors.New("another collect is already running")

// File is an advisory file lock based on O_EXCL + owner PID. It is crash-safe
// enough: a stale lock (owner PID gone) is reclaimed on the next attempt.
type File struct {
	path string
	f    *os.File
}

// NewFile creates a File handle (the lock directory is created on acquire).
func NewFile(path string) *File {
	return &File{path: path}
}

// TryAcquire acquires the lock without waiting. Returns ErrBusy if held.
func (l *File) TryAcquire() error {
	if err := os.MkdirAll(filepath.Dir(l.path), 0o755); err != nil {
		return err
	}
	if f, err := os.OpenFile(l.path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644); err == nil {
		l.f = f
		_, _ = f.WriteString(strconv.Itoa(os.Getpid()) + "\n")
		return nil
	}
	// Held. If the owner PID is gone, reclaim (stale lock).
	if pid := readPID(l.path); pid != "" && !pidAlive(pid) {
		_ = os.Remove(l.path)
		if f, err := os.OpenFile(l.path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644); err == nil {
			l.f = f
			_, _ = f.WriteString(strconv.Itoa(os.Getpid()) + "\n")
			return nil
		}
	}
	return ErrBusy
}

// Acquire waits up to timeout for the lock, polling briefly. Returns ErrBusy
// on timeout.
func (l *File) Acquire(timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		err := l.TryAcquire()
		if err == nil {
			return nil
		}
		if !errors.Is(err, ErrBusy) {
			return err
		}
		if time.Now().After(deadline) {
			return ErrBusy
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// Release removes the lock file.
func (l *File) Release() {
	if l.f != nil {
		_ = l.f.Close()
		_ = os.Remove(l.path)
		l.f = nil
	}
}

func readPID(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return string(b)
}

// pidAlive reports whether the PID recorded in the lock file still exists.
func pidAlive(pidStr string) bool {
	pid, err := strconv.Atoi(strings.TrimSpace(pidStr))
	if err != nil || pid <= 0 {
		return false
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	// Signal 0 probes existence without delivering a signal.
	return proc.Signal(signalZero) == nil
}
