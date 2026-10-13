package remote

import (
	"path/filepath"
	"time"
)

// memoryCredentialsFile holds memory tokens apart from credentials.yaml,
// whose strict shape older CLIs depend on. A leaked MCP client config never
// holds one: clients run `loadout mcp`, which reads it here.
const memoryCredentialsFile = "memory-credentials.yaml"

// memoryCredentials is the on-disk shape of memory-credentials.yaml.
type memoryCredentials struct {
	Tokens map[string]string `yaml:"tokens"`
	// Expires says when each stored token stops working, so loadout sync can
	// make a new one in time. The registry cannot tell a secret's age.
	Expires map[string]time.Time `yaml:"expires,omitempty"`
	// IDs holds the registry's id of each stored token, so loadout sync can
	// revoke the one it replaces. The id is not a secret.
	IDs map[string]string `yaml:"ids,omitempty"`
}

// MemoryToken returns the memory token for the named remote, preferring
// LOADOUT_MEMORY_TOKEN. An empty token with a nil error means none is stored.
func (s *Store) MemoryToken(name string) (string, error) {
	if t := s.getenv(EnvMemoryToken); t != "" {
		return t, nil
	}
	return s.StoredMemoryToken(name)
}

// MemoryTokenFromEnv reports whether LOADOUT_MEMORY_TOKEN stands in for every
// stored memory token, which the person then looks after.
func (s *Store) MemoryTokenFromEnv() bool { return s.getenv(EnvMemoryToken) != "" }

// StoredMemoryToken returns the memory token stored for the named remote,
// ignoring LOADOUT_MEMORY_TOKEN. An empty token with a nil error means none
// is stored.
func (s *Store) StoredMemoryToken(name string) (string, error) {
	var c memoryCredentials
	if err := readYAML(s.memoryCredsPath(), &c); err != nil {
		return "", err
	}
	return c.Tokens[name], nil
}

// StoredMemoryTokenExpiry returns when the stored memory token stops
// working. The zero time means it is not known, as for a token stored by
// SetMemoryToken.
func (s *Store) StoredMemoryTokenExpiry(name string) (time.Time, error) {
	var c memoryCredentials
	if err := readYAML(s.memoryCredsPath(), &c); err != nil {
		return time.Time{}, err
	}
	return c.Expires[name], nil
}

// SetMemoryToken stores the memory token for the named remote, 0600 and
// atomically. An empty token deletes it. Its expiry becomes unknown.
func (s *Store) SetMemoryToken(name, token string) error {
	return s.SetMemoryTokenUntil(name, token, time.Time{})
}

// StoredMemoryTokenID returns the registry's id of the stored memory token,
// or "" when it is not known.
func (s *Store) StoredMemoryTokenID(name string) (string, error) {
	var c memoryCredentials
	if err := readYAML(s.memoryCredsPath(), &c); err != nil {
		return "", err
	}
	return c.IDs[name], nil
}

// SetMemoryTokenUntil stores the memory token and the time it expires, which
// the registry told when it made the token. A zero time means unknown.
func (s *Store) SetMemoryTokenUntil(name, token string, expires time.Time) error {
	return s.SetMemoryTokenMade(name, token, "", expires)
}

// SetMemoryTokenMade is SetMemoryTokenUntil that also keeps the token's id.
func (s *Store) SetMemoryTokenMade(name, token, id string, expires time.Time) error {
	var c memoryCredentials
	if err := readYAML(s.memoryCredsPath(), &c); err != nil {
		return err
	}
	if c.Tokens == nil {
		c.Tokens = map[string]string{}
	}
	delete(c.Expires, name)
	delete(c.IDs, name)
	if token == "" {
		delete(c.Tokens, name)
	} else {
		c.Tokens[name] = token
		if !expires.IsZero() {
			if c.Expires == nil {
				c.Expires = map[string]time.Time{}
			}
			c.Expires[name] = expires.UTC()
		}
		if id != "" {
			if c.IDs == nil {
				c.IDs = map[string]string{}
			}
			c.IDs[name] = id
		}
	}
	return writeYAML(s.memoryCredsPath(), c, 0o600)
}

func (s *Store) memoryCredsPath() string {
	return filepath.Join(filepath.Dir(s.credsPath), memoryCredentialsFile)
}
