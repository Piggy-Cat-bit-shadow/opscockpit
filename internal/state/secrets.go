package state

import (
	"encoding/json"
	"fmt"
	"strings"
)

// SecretFieldNames are field names that must never appear anywhere in the
// serialized state.json. This is a hard structural guarantee: state.json is a
// fixed allowlist schema, so credential-carrying fields cannot exist. This
// validator is belt-and-suspenders — it scans the rendered JSON so that even a
// future field with one of these names (or a leaked value) fails the write.
var SecretFieldNames = []string{
	"password",
	"token",
	"secret",
	"private_key",
	"api_key",
	"apikey",
	"cookie",
	"credential",
	"uuid",
	"client_secret",
	"access_key",
	"session_id",
	"authorization",
	"x-api-key",
}

// Secrets may also be flagged by value shape (a private key PEM block, or a
// bare hex string under a suspicious key). Keep it conservative: we only hard
// block exact private-key markers, never normal IPs/ports.
var privateKeyMarkers = []string{
	"-----BEGIN",
	"PRIVATE KEY",
	"ssh-rsa",
	"ssh-ed25519",
	"ssh-ecdsa",
}

// IsSecretField reports whether a field name looks credential-bearing.
func IsSecretField(name string) bool {
	lower := strings.ToLower(name)
	for _, f := range SecretFieldNames {
		if strings.Contains(lower, f) {
			return true
		}
	}
	return false
}

// checkForSecrets scans decoded JSON (map[string]any) recursively and returns
// the first secret-bearing key/value found, or "" if clean.
func checkForSecrets(v any) string {
	switch t := v.(type) {
	case map[string]any:
		for k, val := range t {
			if IsSecretField(k) {
				return k
			}
			if s := checkForSecrets(val); s != "" {
				return s
			}
		}
	case []any:
		for _, item := range t {
			if s := checkForSecrets(item); s != "" {
				return s
			}
		}
	case string:
		// Only exact private-key markers are blocked by value. The key-name
		// scan above is the primary defense.
		for _, m := range privateKeyMarkers {
			if strings.Contains(t, m) {
				return t[:min(len(t), 40)]
			}
		}
	}
	return ""
}

// Validate returns an error if the marshaled state contains any field or value
// that looks credential-bearing. It also verifies the schema version.
func (s *State) Validate() error {
	if s.SchemaVersion != SchemaVersion {
		return fmt.Errorf("state schema_version = %d, want %d", s.SchemaVersion, SchemaVersion)
	}
	raw, err := json.Marshal(s)
	if err != nil {
		return fmt.Errorf("marshal state: %w", err)
	}
	var decoded any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return fmt.Errorf("unmarshal marshaled state: %w", err)
	}
	if found := checkForSecrets(decoded); found != "" {
		return fmt.Errorf("state contains forbidden secret field or value: %q", found)
	}
	return nil
}
