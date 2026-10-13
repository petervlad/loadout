// Package remote manages the registries a user publishes to and installs
// from: their names, URLs and access tokens.
//
// Settings live in config.yaml; tokens live apart in credentials.yaml with
// 0600 permissions, so the config can be shared or shown without leaking
// secrets. LOADOUT_TOKEN overrides the stored token for every remote.
package remote

import (
	"errors"
	"fmt"
	"net/url"
	"slices"

	"github.com/petervlad/loadout/internal/ident"
)

// EnvToken overrides the stored token, for CI and one-off use.
const EnvToken = "LOADOUT_TOKEN"

// EnvMemoryToken overrides the stored memory token for every remote.
const EnvMemoryToken = "LOADOUT_MEMORY_TOKEN"

// ErrNoRemote is returned when no remote is configured or selected.
var ErrNoRemote = errors.New("no remote configured; run: loadout remote add NAME URL")

// Remote is one registry.
type Remote struct {
	Name string `yaml:"name"`
	URL  string `yaml:"url"`
	// CAFile is an extra PEM CA bundle, for TLS-inspecting corporate proxies.
	CAFile string `yaml:"ca_file,omitempty"`
}

// Validate checks the name and URL.
func (r Remote) Validate() error {
	if err := ident.Check("remote", r.Name); err != nil {
		return err
	}
	u, err := url.Parse(r.URL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return fmt.Errorf("remote URL %q: want http(s)://host[:port]", r.URL)
	}
	return nil
}

// Insecure reports whether tokens to this remote travel unencrypted to a
// host other than this machine.
func (r Remote) Insecure() bool {
	u, err := url.Parse(r.URL)
	if err != nil || u.Scheme == "https" {
		return false
	}
	switch u.Hostname() {
	case "localhost", "127.0.0.1", "::1":
		return false
	}
	return true
}

// Config is the set of remotes, the default one, and which remote holds
// each team's namespace. A config without bindings (written before teams
// existed) works as before: everything goes to the default remote.
type Config struct {
	Default  string    `yaml:"default,omitempty"`
	Remotes  []Remote  `yaml:"remotes"`
	Bindings []Binding `yaml:"bindings,omitempty"`
}

// Add adds r. The first remote becomes the default.
func (c *Config) Add(r Remote) error {
	if err := r.Validate(); err != nil {
		return err
	}
	if _, ok := c.Get(r.Name); ok {
		return fmt.Errorf("remote %q already exists; remove it first", r.Name)
	}
	c.Remotes = append(c.Remotes, r)
	if c.Default == "" {
		c.Default = r.Name
	}
	return nil
}

// Remove deletes the named remote, moving the default if needed, and every
// binding to it. It returns the removed bindings.
func (c *Config) Remove(name string) ([]Binding, error) {
	i := slices.IndexFunc(c.Remotes, func(r Remote) bool { return r.Name == name })
	if i < 0 {
		return nil, fmt.Errorf("remote %q does not exist", name)
	}
	c.Remotes = slices.Delete(c.Remotes, i, i+1)
	var unbound []Binding
	c.Bindings = slices.DeleteFunc(c.Bindings, func(b Binding) bool {
		if b.Remote == name {
			unbound = append(unbound, b)
			return true
		}
		return false
	})
	if c.Default == name {
		c.Default = ""
		if len(c.Remotes) > 0 {
			c.Default = c.Remotes[0].Name
		}
	}
	return unbound, nil
}

// Get returns the named remote.
func (c *Config) Get(name string) (Remote, bool) {
	i := slices.IndexFunc(c.Remotes, func(r Remote) bool { return r.Name == name })
	if i < 0 {
		return Remote{}, false
	}
	return c.Remotes[i], true
}

// Resolve returns the named remote, or the default when name is empty.
func (c *Config) Resolve(name string) (Remote, error) {
	if name == "" {
		name = c.Default
	}
	if name == "" {
		return Remote{}, ErrNoRemote
	}
	r, ok := c.Get(name)
	if !ok {
		return Remote{}, fmt.Errorf("remote %q does not exist (see: loadout remote list)", name)
	}
	return r, nil
}
