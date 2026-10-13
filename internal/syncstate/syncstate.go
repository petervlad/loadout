// Package syncstate keeps what sync learned from the registries between runs,
// so status and the session hooks can say it without a network call.
//
// The state is a hint, never the truth: whoever reads it checks every record
// against the lock and the files on disk again. It lives apart from the lock
// because an older loadout reads the lock strictly and would call a new field
// damage, and because it can be deleted at any time and costs only notices
// until the next sync.
package syncstate

import (
	"cmp"
	"slices"
	"time"

	"github.com/petervlad/loadout/internal/pack"
	"github.com/petervlad/loadout/internal/semver"
	"github.com/petervlad/loadout/internal/target"
	"github.com/petervlad/loadout/internal/textdiff"
)

// Key names one install: a package in a scope.
type Key struct {
	ID    pack.ID
	Scope target.Scope
}

// Blocker is one file that keeps an update out.
type Blocker struct {
	// Path is slash-separated and relative to the scope root.
	Path string `yaml:"path"`
	// Reason says why it is in the way, in the installer's words: "edited
	// since loadout wrote it", "not written by loadout", ...
	Reason string `yaml:"reason"`
	// Owner names the package the file belongs to, when another package
	// of the same layer is in the way.
	Owner string `yaml:"owner,omitempty"`
}

// Behind is an install the last sync found behind its registry.
type Behind struct {
	ID    pack.ID      `yaml:"id"`
	Scope target.Scope `yaml:"scope"`
	// Version is what was installed when sync checked.
	Version semver.Version `yaml:"version"`
	// Latest is the newest release; Version when only the team's model
	// settings changed.
	Latest semver.Version `yaml:"latest"`
	// Models is the digest of the team's model table, for a model change.
	Models string `yaml:"models,omitempty"`
	// Template is the template release ("acme/dev-loop@1.1.0") a conductor
	// is behind on; empty for every other kind of change. It is kept out of
	// syncstate.yaml, in the Store's templates file, so older CLIs still
	// read the state.
	Template string `yaml:"-"`
	// Held lists the files in the way; empty when the update is only
	// available.
	Held []Blocker `yaml:"held,omitempty"`
	// Since is when a sync first saw this Latest.
	Since time.Time `yaml:"since"`
	// Checked is when a sync last checked the install.
	Checked time.Time `yaml:"checked"`
	// Told is set once the notifier was told about Latest, held or not.
	Told bool `yaml:"told,omitempty"`
}

// Key returns the install b is about.
func (b Behind) Key() Key { return Key{ID: b.ID, Scope: b.Scope} }

// IsHeld reports whether files keep the update out.
func (b Behind) IsHeld() bool { return len(b.Held) > 0 }

// ModelsOnly reports whether only the team's model settings changed.
func (b Behind) ModelsOnly() bool { return b.Latest.Compare(b.Version) == 0 && b.Template == "" }

// HeldPaths returns the paths of the files in the way.
func (b Behind) HeldPaths() []string {
	out := make([]string, len(b.Held))
	for i, h := range b.Held {
		out[i] = h.Path
	}
	return out
}

func (b Behind) clone() Behind {
	b.Held = slices.Clone(b.Held)
	return b
}

// Merged is an install that loadout install --merge left with conflict
// markers, for status and the hooks to say until they are resolved.
type Merged struct {
	ID    pack.ID      `yaml:"id"`
	Scope target.Scope `yaml:"scope"`
	// Version is the release merged in.
	Version semver.Version `yaml:"version"`
	// Labels are the names in the markers written.
	Labels textdiff.Labels `yaml:"labels"`
	// Paths are the files with markers, relative to the scope root.
	Paths []string  `yaml:"paths,flow"`
	At    time.Time `yaml:"at"`
}

// Key returns the install m is about.
func (m Merged) Key() Key { return Key{ID: m.ID, Scope: m.Scope} }

func (m Merged) clone() Merged {
	m.Paths = slices.Clone(m.Paths)
	return m
}

// State is the set of records, one of each kind per install at most. Its
// methods copy on the way in and out, so a State read once never changes
// under a caller.
type State struct {
	behind []Behind
	merged []Merged
}

// FindMerged returns the merge record of the install k.
func (s *State) FindMerged(k Key) (Merged, bool) {
	i := slices.IndexFunc(s.merged, func(m Merged) bool { return m.Key() == k })
	if i < 0 {
		return Merged{}, false
	}
	return s.merged[i].clone(), true
}

// PutMerged adds m, replacing the merge record of the same install.
func (s *State) PutMerged(m Merged) {
	m = m.clone()
	if i := slices.IndexFunc(s.merged, func(o Merged) bool { return o.Key() == m.Key() }); i >= 0 {
		s.merged[i] = m
		return
	}
	s.merged = append(s.merged, m)
}

// DropMerged removes the merge record of k, reporting whether there was one.
func (s *State) DropMerged(k Key) bool {
	i := slices.IndexFunc(s.merged, func(m Merged) bool { return m.Key() == k })
	if i < 0 {
		return false
	}
	s.merged = slices.Delete(s.merged, i, i+1)
	return true
}

// AllMerged returns a copy of every merge record, sorted like All.
func (s *State) AllMerged() []Merged {
	out := make([]Merged, len(s.merged))
	for i, m := range s.merged {
		out[i] = m.clone()
	}
	slices.SortFunc(out, func(a, b Merged) int {
		return cmp.Or(cmp.Compare(a.ID.String(), b.ID.String()), cmp.Compare(a.Scope.Root, b.Scope.Root), cmp.Compare(a.Scope.Kind, b.Scope.Kind))
	})
	return out
}

// Find returns the record of the install k.
func (s *State) Find(k Key) (Behind, bool) {
	i := s.index(k)
	if i < 0 {
		return Behind{}, false
	}
	return s.behind[i].clone(), true
}

// Put adds b, replacing the record of the same install.
func (s *State) Put(b Behind) {
	b = b.clone()
	if i := s.index(b.Key()); i >= 0 {
		s.behind[i] = b
		return
	}
	s.behind = append(s.behind, b)
}

// Drop removes the record of k, reporting whether there was one.
func (s *State) Drop(k Key) bool {
	i := s.index(k)
	if i < 0 {
		return false
	}
	s.behind = slices.Delete(s.behind, i, i+1)
	return true
}

// All returns a copy of every record, sorted by package then scope root.
func (s *State) All() []Behind {
	out := make([]Behind, len(s.behind))
	for i, b := range s.behind {
		out[i] = b.clone()
	}
	slices.SortFunc(out, func(a, b Behind) int {
		return cmp.Or(cmp.Compare(a.ID.String(), b.ID.String()), cmp.Compare(a.Scope.Root, b.Scope.Root), cmp.Compare(a.Scope.Kind, b.Scope.Kind))
	})
	return out
}

func (s *State) index(k Key) int {
	return slices.IndexFunc(s.behind, func(b Behind) bool { return b.ID == k.ID && b.Scope == k.Scope })
}
