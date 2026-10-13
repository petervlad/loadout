// Package memory is team memory: short typed facts that a team's agents save
// for each other. This file holds the values, draft.go the checks that build
// them, store.go the storage interfaces the package owns.
package memory

import (
	"time"

	"github.com/petervlad/loadout/internal/registry"
)

// Type is what kind of fact an entry is. "user" is not a Type: preferences
// stay in a person's own memory.
type Type string

// The three types, as in personal memory.
const (
	TypeFeedback  Type = "feedback"
	TypeProject   Type = "project"
	TypeReference Type = "reference"
)

// Types lists the valid types.
func Types() []Type { return []Type{TypeFeedback, TypeProject, TypeReference} }

// Valid reports whether t is one of the three types.
func (t Type) Valid() bool {
	switch t {
	case TypeFeedback, TypeProject, TypeReference:
		return true
	}
	return false
}

// Status says whether an entry is shown.
type Status string

// Retracted entries are hidden from search, the index and recent, and keep
// their history.
const (
	StatusActive    Status = "active"
	StatusRetracted Status = "retracted"
)

// Action is what a version records.
type Action string

// The actions a version can have.
const (
	ActionSave    Action = "save"
	ActionUpdate  Action = "update"
	ActionConfirm Action = "confirm"
	ActionRetract Action = "retract"
	ActionMove    Action = "move"
)

// Caller is the person behind a request, resolved from a memory token or a
// web session. Label is the email, as the wiki keeps it.
type Caller struct {
	UserID string
	Label  string
}

// Entry is one memory as read from a store. Stores return copies, so callers
// may keep one, but nothing writes through it.
type Entry struct {
	ID          int64
	Namespace   string
	Type        Type
	Name        string
	Description string
	Body        string
	Links       []int64 // ids of other entries
	AuthorID    string
	AuthorLabel string
	// EditedBy is the label of the newest version's author that is not a
	// confirm, when that is not the entry's author. Empty if the author made
	// the last edit.
	EditedBy    string
	CreatedAt   time.Time
	UpdatedAt   time.Time // the last version, whatever its action
	ConfirmedAt time.Time // the last time someone checked it is still true
	Status      Status
	Version     int // number of the latest version, from 1
	Uses        int // how many times an agent read it with get
}

// Active reports whether the entry is shown.
func (e Entry) Active() bool { return e.Status == StatusActive }

// Unconfirmed reports whether nobody checked the entry for more than six
// calendar months before now. One rule for the index, search, get and the
// web. The entry is marked, never hidden.
func (e Entry) Unconfirmed(now time.Time) bool {
	return e.ConfirmedAt.Before(now.AddDate(0, -UnconfirmedMonths, 0))
}

// Version is one step in an entry's history: the full snapshot after the
// action, and who did it.
type Version struct {
	EntryID     int64
	Number      int
	Action      Action
	Namespace   string
	Type        Type
	Name        string
	Description string
	Body        string
	Links       []int64
	By          Caller
	Note        string // an update note or a retract reason
	At          time.Time
}

// Purged records that an entry was removed for good. It holds no text.
type Purged struct {
	EntryID   int64
	Namespace string
	Versions  int       // how many versions went with it
	CreatedAt time.Time // when the entry was first saved, to find backups that hold it
	At        time.Time
}

// Rights is what one person may do with the memory of one namespace.
type Rights struct {
	Read, Write, Moderate, Switch bool
	// Layers are the namespace and its project, closest first.
	Layers []string
	// Kind says whether the namespace is a project or a team.
	Kind registry.NamespaceKind
}
