package state

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// WriteAtomicOptions configures an atomic state write.
type WriteAtomicOptions struct {
	// FSync controls whether the temp file is fsync'd before rename.
	FSync bool
	// Mode is the file mode for the final state.json (default 0644).
	Mode os.FileMode
}

// WriteAtomic writes state.json safely:
//
//	state.json.tmp → (validate) → fsync → rename → state.json
//
// The previous state.json is never truncated in place; on any failure the
// original file is left untouched.
func WriteAtomic(path string, s *State, opts WriteAtomicOptions) error {
	if s == nil {
		return fmt.Errorf("write state: nil state")
	}
	if opts.Mode == 0 {
		opts.Mode = 0o644
	}

	if err := s.Validate(); err != nil {
		return fmt.Errorf("write state: validation failed: %w", err)
	}

	dir := filepath.Dir(path)
	base := filepath.Base(path)
	tmp := filepath.Join(dir, base+".tmp")

	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return fmt.Errorf("write state: marshal: %w", err)
	}
	// The temp file must be valid JSON and pass the secret scan too.
	if err := json.Unmarshal(data, &struct{}{}); err != nil {
		return fmt.Errorf("write state: invalid json: %w", err)
	}
	// Re-run the secret scan over the exact bytes that will be committed.
	var decoded any
	if err := json.Unmarshal(data, &decoded); err != nil {
		return fmt.Errorf("write state: invalid json: %w", err)
	}
	if found := checkForSecrets(decoded); found != "" {
		return fmt.Errorf("write state: contains forbidden secret field or value: %q", found)
	}

	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, opts.Mode)
	if err != nil {
		return fmt.Errorf("write state: open tmp: %w", err)
	}
	if _, err := io.Copy(f, bytes.NewReader(data)); err != nil {
		f.Close()
		os.Remove(tmp)
		return fmt.Errorf("write state: write tmp: %w", err)
	}
	if _, err := f.Write([]byte("\n")); err != nil {
		f.Close()
		os.Remove(tmp)
		return fmt.Errorf("write state: write trailing newline: %w", err)
	}
	if opts.FSync {
		if err := f.Sync(); err != nil {
			f.Close()
			os.Remove(tmp)
			return fmt.Errorf("write state: fsync tmp: %w", err)
		}
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("write state: close tmp: %w", err)
	}

	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("write state: rename: %w", err)
	}

	if opts.FSync {
		if d, err := os.Open(dir); err == nil {
			d.Sync()
			d.Close()
		}
	}
	return nil
}
