// Package lock records what loadout installed: which package version, where,
// with which update policy, and the hash of every file it wrote.
//
// The lock is how loadout knows a file is its own, and whether someone has
// edited it since.
package lock

import (
	"cmp"
	"slices"
	"strings"

	"github.com/petervlad/loadout/internal/pack"
	"github.com/petervlad/loadout/internal/semver"
	"github.com/petervlad/loadout/internal/target"
)

// Policy says how updates reach an installed package.
type Policy string

// Update policies: install updates silently, announce them, or never change.
const (
	PolicyAuto   Policy = "auto"
	PolicyNotify Policy = "notify"
	PolicyPin    Policy = "pin"
)

// DefaultPolicy is used when the user does not choose one. It is auto so
// that what a team publishes reaches everyone on their next sync: a file the
// person edited is never overwritten (the update is held back and reported),
// and notify or pin stay one command away for anyone who wants them.
const DefaultPolicy = PolicyAuto

// Valid reports whether p is a known policy.
func (p Policy) Valid() bool {
	return p == PolicyAuto || p == PolicyNotify || p == PolicyPin
}

// FileRecord is one file an install wrote.
type FileRecord struct {
	// Target names the AI tool the file is for; "claude,copilot" when two
	// tools read the same file (a skill in a repository).
	Target string `yaml:"target"`
	// Path is slash-separated and relative to the entry's scope root.
	Path   string `yaml:"path"`
	SHA256 string `yaml:"sha256"`
	// Unit is the skill folder the file belongs to; empty for an agent
	// file. An emptied skill folder is removed with its last file.
	Unit string `yaml:"unit,omitempty"`
}

// Entry is one package installed into one scope.
type Entry struct {
	ID      pack.ID        `yaml:"id"`
	Version semver.Version `yaml:"version"`
	Digest  pack.Digest    `yaml:"digest"`
	// Source is where the package came from: a folder path or a registry URL.
	Source string `yaml:"source"`
	// Remote names the registry the package was installed from; empty for
	// folder installs. sync only updates entries that have one.
	Remote string       `yaml:"remote,omitempty"`
	Policy Policy       `yaml:"policy"`
	Scope  target.Scope `yaml:"scope"`
	// Depth is how far the package's namespace is from the installing
	// token's own: 0 for its own namespace (and for folders), 1 for the
	// parent, and so on. When two packages write the same file, the lower
	// depth wins.
	Depth int          `yaml:"depth,omitempty"`
	Files []FileRecord `yaml:"files"`
	// Shadowed lists files the package would write but a package from a
	// closer layer owns; they are not on disk for this entry.
	Shadowed []Shadow `yaml:"shadowed,omitempty"`
	// Models is the digest of the model table the files were rendered
	// with; empty for the empty table and in entries written before model
	// tables, which is the same thing. sync compares it with the team's
	// current table.
	Models string `yaml:"models,omitempty"`
	// Requires records the releases of the packages the files were rendered
	// from (the template a conductor comes from). Empty for most kinds.
	// sync compares it with the registry. It is kept out of lock.yaml, in
	// the Store's requires file, so older CLIs still read the lock.
	Requires []Requirement `yaml:"-"`
}

// Requirement is one release a package was rendered from.
type Requirement struct {
	ID      pack.ID        `yaml:"id"`
	Version semver.Version `yaml:"version"`
	Digest  pack.Digest    `yaml:"digest"`
}

// Shadow is one file a closer layer's package owns instead.
type Shadow struct {
	Target string `yaml:"target"`
	// Path is slash-separated and relative to the entry's scope root.
	Path string `yaml:"path"`
	// By is the package that owns the file.
	By pack.ID `yaml:"by"`
	// Unit is the skill folder the file belongs to; empty for an agent file
	// and in locks written before skills.
	Unit string `yaml:"unit,omitempty"`
}

// Targets returns the targets the entry was installed for, including those
// whose file is shadowed, in first-seen order.
func (e Entry) Targets() []string {
	var out []string
	add := func(ts string) {
		for t := range strings.SplitSeq(ts, ",") {
			if !slices.Contains(out, t) {
				out = append(out, t)
			}
		}
	}
	for _, f := range e.Files {
		add(f.Target)
	}
	for _, s := range e.Shadowed {
		add(s.Target)
	}
	return out
}

// FullyShadowed reports whether every file of the entry is owned by a
// closer layer's package, so none of it is in use.
func (e Entry) FullyShadowed() bool { return len(e.Files) == 0 && len(e.Shadowed) > 0 }

func (e Entry) clone() Entry {
	e.Files = slices.Clone(e.Files)
	e.Shadowed = slices.Clone(e.Shadowed)
	e.Requires = slices.Clone(e.Requires)
	return e
}

// Lock is the set of installed entries. An entry is keyed by package id and
// scope (kind and root), so the same agent can be installed for the user and
// in any number of repos.
type Lock struct {
	entries []Entry
}

// Entries returns a copy of all entries, sorted by id then scope.
func (l *Lock) Entries() []Entry {
	out := make([]Entry, len(l.entries))
	for i, e := range l.entries {
		out[i] = e.clone()
	}
	slices.SortFunc(out, func(a, b Entry) int {
		return cmp.Or(cmp.Compare(a.ID.String(), b.ID.String()), cmp.Compare(a.Scope.Root, b.Scope.Root))
	})
	return out
}

// Find returns the entry for id installed in scope.
func (l *Lock) Find(id pack.ID, scope target.Scope) (Entry, bool) {
	i := l.index(id, scope)
	if i < 0 {
		return Entry{}, false
	}
	return l.entries[i].clone(), true
}

// Owner returns the entry that wrote the file at path under root.
func (l *Lock) Owner(root, path string) (Entry, bool) {
	for _, e := range l.entries {
		if e.Scope.Root != root {
			continue
		}
		for _, f := range e.Files {
			if f.Path == path {
				return e.clone(), true
			}
		}
	}
	return Entry{}, false
}

// Put adds e, replacing any entry with the same id and scope root.
func (l *Lock) Put(e Entry) {
	e = e.clone()
	if i := l.index(e.ID, e.Scope); i >= 0 {
		l.entries[i] = e
		return
	}
	l.entries = append(l.entries, e)
}

// Remove deletes the entry for id in scope, reporting whether it existed.
func (l *Lock) Remove(id pack.ID, scope target.Scope) bool {
	i := l.index(id, scope)
	if i < 0 {
		return false
	}
	l.entries = slices.Delete(l.entries, i, i+1)
	return true
}

func (l *Lock) index(id pack.ID, scope target.Scope) int {
	return slices.IndexFunc(l.entries, func(e Entry) bool {
		return e.ID == id && e.Scope == scope
	})
}
