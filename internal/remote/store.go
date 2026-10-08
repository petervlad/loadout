package remote

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"go.yaml.in/yaml/v3"

	"github.com/petervlad/loadout/internal/atomicfile"
	"github.com/petervlad/loadout/internal/strictyaml"
)

// Store reads and writes the remotes config and the credentials file.
type Store struct {
	configPath string
	credsPath  string
	getenv     func(string) string
}

// NewStore returns a store keeping its files in dir. getenv is used for the
// LOADOUT_TOKEN override.
func NewStore(dir string, getenv func(string) string) *Store {
	return &Store{
		configPath: filepath.Join(dir, "config.yaml"),
		credsPath:  filepath.Join(dir, "credentials.yaml"),
		getenv:     getenv,
	}
}

// Load reads the config; a missing file is an empty config.
func (s *Store) Load() (*Config, error) {
	var c Config
	if err := readYAML(s.configPath, &c); err != nil {
		return nil, err
	}
	return &c, nil
}

// Save writes the config.
func (s *Store) Save(c *Config) error {
	return writeYAML(s.configPath, c, 0o644)
}

// credentials is the on-disk shape of credentials.yaml.
type credentials struct {
	Tokens map[string]string `yaml:"tokens"`
}

// Token returns the token for the named remote, preferring LOADOUT_TOKEN.
// An empty token with a nil error means none is stored.
func (s *Store) Token(name string) (string, error) {
	if t := s.getenv(EnvToken); t != "" {
		return t, nil
	}
	return s.StoredToken(name)
}

// StoredToken returns the token stored for the named remote, ignoring
// LOADOUT_TOKEN. An empty token with a nil error means none is stored.
func (s *Store) StoredToken(name string) (string, error) {
	var c credentials
	if err := readYAML(s.credsPath, &c); err != nil {
		return "", err
	}
	return c.Tokens[name], nil
}

// SetToken stores the token for the named remote; an empty token deletes it.
func (s *Store) SetToken(name, token string) error {
	var c credentials
	if err := readYAML(s.credsPath, &c); err != nil {
		return err
	}
	if c.Tokens == nil {
		c.Tokens = map[string]string{}
	}
	if token == "" {
		delete(c.Tokens, name)
	} else {
		c.Tokens[name] = token
	}
	return writeYAML(s.credsPath, c, 0o600)
}

func readYAML(path string, v any) error {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}
	if err := strictyaml.Decode(data, v); err != nil && !errors.Is(err, strictyaml.ErrEmpty) {
		return fmt.Errorf("parse %s: %w", path, err)
	}
	return nil
}

// writeYAML writes atomically with the given permissions.
func writeYAML(path string, v any, perm os.FileMode) error {
	data, err := yaml.Marshal(v)
	if err != nil {
		return fmt.Errorf("encode %s: %w", path, err)
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	if err := atomicfile.Write(path, data, perm); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}
