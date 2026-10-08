package remote

import (
	"fmt"
	"net/url"
	"slices"
	"strings"

	"github.com/petervlad/loadout/internal/ident"
)

// Role is what a team's token may do there. Empty means unknown.
type Role string

// Roles.
const (
	RoleRead    Role = "read"
	RolePublish Role = "publish"
)

// Valid reports whether r is a known role.
func (r Role) Valid() bool { return r == RoleRead || r == RolePublish }

// Binding says which remote holds a namespace's packages, so commands given
// a package id find its registry without --remote.
type Binding struct {
	Namespace string `yaml:"namespace"`
	Remote    string `yaml:"remote"`
	Role      Role   `yaml:"role,omitempty"`
	// Via lists the joined namespaces this one is an ancestor layer of. It
	// is empty for a namespace joined directly. An ancestor binding goes
	// away when the last namespace that brought it in is left.
	Via []string `yaml:"via,omitempty"`
}

// Direct reports whether the namespace was joined itself, not only reached
// as an ancestor of a joined one.
func (b Binding) Direct() bool { return len(b.Via) == 0 }

// UnboundError is returned when a package's namespace has no team registry
// and loadout cannot pick one of several remotes.
type UnboundError struct {
	Namespace string
}

func (e *UnboundError) Error() string {
	return fmt.Sprintf("no team registry for namespace %s; join the team (loadout team join) or pass --remote", e.Namespace)
}

// Binding returns the binding of namespace.
func (c *Config) Binding(namespace string) (Binding, bool) {
	i := slices.IndexFunc(c.Bindings, func(b Binding) bool { return b.Namespace == namespace })
	if i < 0 {
		return Binding{}, false
	}
	b := c.Bindings[i]
	b.Via = slices.Clone(b.Via)
	return b, true
}

// Bind binds namespace to the named remote as a directly joined team,
// replacing any earlier binding of it.
func (c *Config) Bind(namespace, remoteName string, role Role) error {
	if err := ident.Check("team", namespace); err != nil {
		return err
	}
	if _, ok := c.Get(remoteName); !ok {
		return fmt.Errorf("remote %q does not exist", remoteName)
	}
	if role != "" && !role.Valid() {
		return fmt.Errorf("role %q: want read or publish", role)
	}
	b := Binding{Namespace: namespace, Remote: remoteName, Role: role}
	if i := slices.IndexFunc(c.Bindings, func(b Binding) bool { return b.Namespace == namespace }); i >= 0 {
		c.Bindings[i] = b
		return nil
	}
	c.Bindings = append(c.Bindings, b)
	return nil
}

// BindAncestor binds namespace, an ancestor layer of the joined namespace
// via, to the same remote. It never moves an existing binding to another
// remote: it returns false and leaves the config alone in that case.
func (c *Config) BindAncestor(namespace, remoteName, via string) (bool, error) {
	if err := ident.Check("team", namespace); err != nil {
		return false, err
	}
	if _, ok := c.Get(remoteName); !ok {
		return false, fmt.Errorf("remote %q does not exist", remoteName)
	}
	i := slices.IndexFunc(c.Bindings, func(b Binding) bool { return b.Namespace == namespace })
	if i < 0 {
		c.Bindings = append(c.Bindings, Binding{Namespace: namespace, Remote: remoteName, Role: RoleRead, Via: []string{via}})
		return true, nil
	}
	b := &c.Bindings[i]
	if b.Remote != remoteName {
		return false, nil
	}
	if !b.Direct() && !slices.Contains(b.Via, via) {
		b.Via = append(b.Via, via)
	}
	return true, nil
}

// Unbind removes the binding of namespace and every ancestor binding only
// it brought in. It returns the removed bindings.
func (c *Config) Unbind(namespace string) ([]Binding, error) {
	if _, ok := c.Binding(namespace); !ok {
		return nil, fmt.Errorf("namespace %s is not bound to a registry (see: loadout team list)", namespace)
	}
	var removed []Binding
	kept := c.Bindings[:0]
	for _, b := range c.Bindings {
		if b.Namespace == namespace {
			removed = append(removed, b)
			continue
		}
		if !b.Direct() {
			b.Via = slices.DeleteFunc(slices.Clone(b.Via), func(v string) bool { return v == namespace })
			if len(b.Via) == 0 {
				removed = append(removed, b)
				continue
			}
		}
		kept = append(kept, b)
	}
	c.Bindings = kept
	return removed, nil
}

// Bound reports whether any binding uses the named remote.
func (c *Config) Bound(remoteName string) bool {
	return slices.ContainsFunc(c.Bindings, func(b Binding) bool { return b.Remote == remoteName })
}

// ByURL returns the remotes whose URL is u, ignoring a trailing slash.
func (c *Config) ByURL(u string) []Remote {
	u = strings.TrimRight(u, "/")
	var out []Remote
	for _, r := range c.Remotes {
		if strings.TrimRight(r.URL, "/") == u {
			out = append(out, r)
		}
	}
	return out
}

// NameFor derives an unused remote name from a registry URL's host, for
// example "registry-example-com", then "registry-example-com-2".
func (c *Config) NameFor(rawURL string) string {
	base := "registry"
	if u, err := url.Parse(rawURL); err == nil && u.Hostname() != "" {
		var sb strings.Builder
		for _, r := range strings.ToLower(u.Hostname()) {
			switch {
			case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
				sb.WriteRune(r)
			default:
				sb.WriteByte('-')
			}
		}
		base = strings.Join(strings.FieldsFunc(sb.String(), func(r rune) bool { return r == '-' }), "-")
		if base == "" {
			base = "registry"
		}
		if len(base) > 48 {
			base = strings.TrimRight(base[:48], "-")
		}
	}
	name := base
	for n := 2; ; n++ {
		if _, taken := c.Get(name); !taken {
			return name
		}
		name = fmt.Sprintf("%s-%d", base, n)
	}
}

// ResolveFor picks the remote for a package in namespace. The order is: the
// explicit remote (--remote), the namespace's binding, the remote the
// package was installed from (recorded), then the default. With team
// bindings in use and several remotes, an unbound namespace is an
// *UnboundError rather than a guess.
func (c *Config) ResolveFor(explicit, namespace, recorded string) (Remote, error) {
	if explicit != "" {
		return c.Resolve(explicit)
	}
	if b, ok := c.Binding(namespace); ok && namespace != "" {
		r, ok := c.Get(b.Remote)
		if !ok {
			return Remote{}, fmt.Errorf("team %s is bound to remote %q, which does not exist; run: loadout team leave %s, then join again", namespace, b.Remote, namespace)
		}
		return r, nil
	}
	if recorded != "" {
		return c.Resolve(recorded)
	}
	if namespace != "" && len(c.Bindings) > 0 && len(c.Remotes) > 1 {
		return Remote{}, &UnboundError{Namespace: namespace}
	}
	return c.Resolve("")
}
