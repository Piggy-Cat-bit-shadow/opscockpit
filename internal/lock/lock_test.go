package lock

import (
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func TestAcquireRelease(t *testing.T) {
	dir := t.TempDir()
	l := NewFile(filepath.Join(dir, "collect.lock"))
	if err := l.TryAcquire(); err != nil {
		t.Fatal(err)
	}
	l.Release()
	// Re-acquire after release.
	if err := l.TryAcquire(); err != nil {
		t.Fatal(err)
	}
	l.Release()
}

func TestSingleFlight(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "collect.lock")
	a := NewFile(path)
	b := NewFile(path)
	if err := a.TryAcquire(); err != nil {
		t.Fatal(err)
	}
	// Second must be busy.
	if err := b.TryAcquire(); !errors.Is(err, ErrBusy) {
		t.Fatalf("second acquire err = %v, want ErrBusy", err)
	}
	// After release, b can acquire.
	a.Release()
	if err := b.TryAcquire(); err != nil {
		t.Fatal(err)
	}
	b.Release()
}

func TestAcquireWaits(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "collect.lock")
	a := NewFile(path)
	b := NewFile(path)
	if err := a.TryAcquire(); err != nil {
		t.Fatal(err)
	}
	go func() {
		time.Sleep(150 * time.Millisecond)
		a.Release()
	}()
	start := time.Now()
	if err := b.Acquire(2 * time.Second); err != nil {
		t.Fatalf("acquire with wait failed: %v", err)
	}
	elapsed := time.Since(start)
	if elapsed < 100*time.Millisecond {
		t.Errorf("acquire should have waited for release, got %v", elapsed)
	}
	b.Release()
}

func TestAcquireTimeout(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "collect.lock")
	a := NewFile(path)
	if err := a.TryAcquire(); err != nil {
		t.Fatal(err)
	}
	defer a.Release()
	b := NewFile(path)
	if err := b.Acquire(150 * time.Millisecond); !errors.Is(err, ErrBusy) {
		t.Fatalf("timeout acquire err = %v, want ErrBusy", err)
	}
}
