package api

import (
	"errors"
	"time"
)

// CodeStale is the code of a 409 for a wiki save based on an old revision.
// Its ErrorBody names the newest revision in Newest.
const CodeStale = "stale"

// ErrStale matches, with errors.Is, a response carrying CodeStale.
var ErrStale = errors.New("changed since it was read")

// ErrBehind matches, with errors.Is, a response carrying CodeBehind.
var ErrBehind = errors.New("proposal is behind the latest release")

// WikiPage is the body of GET /v1/namespaces/{ns}/wiki (?revision=N) and of
// a PUT that saved. Revision 0 means nothing is written yet.
type WikiPage struct {
	Namespace string    `json:"namespace"`
	Revision  int       `json:"revision"`
	Format    string    `json:"format"`
	Text      string    `json:"text"`
	Author    string    `json:"author,omitempty"`
	Note      string    `json:"note,omitempty"`
	CreatedAt time.Time `json:"created_at,omitzero"`
	// Saved is false when the text equalled the base and nothing was written.
	Saved bool `json:"saved,omitempty"`
}

// WikiSave is the body of PUT /v1/namespaces/{ns}/wiki.
type WikiSave struct {
	Base   int    `json:"base"`
	Format string `json:"format"`
	Text   string `json:"text"`
	Note   string `json:"note,omitempty"`
}

// WikiRevisions is the body of GET /v1/namespaces/{ns}/wiki/revisions,
// newest first.
type WikiRevisions struct {
	Namespace string         `json:"namespace"`
	Revisions []WikiRevision `json:"revisions"`
}

// WikiRevision is one saved revision of a wiki page, without its text.
type WikiRevision struct {
	Revision  int       `json:"revision"`
	Format    string    `json:"format"`
	Size      int       `json:"size"`
	Author    string    `json:"author"`
	Note      string    `json:"note,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}
