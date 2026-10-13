package web

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/petervlad/loadout/internal/memory"
	"github.com/petervlad/loadout/internal/registry"
	"github.com/petervlad/loadout/internal/textdiff"
)

// The Memory tab of a namespace, while its team memory is on. People look
// memories over here and take back the wrong ones. Nobody edits: an agent
// saves and updates through the MCP server, and the text on these pages is
// plain text, never Markdown, since it is untrusted.
//
//	GET  /t/{ns}/memory                 search (?q ?type ?layer), else New this week
//	GET  /t/{ns}/memory/setup           how to connect an AI tool by hand
//	GET  /t/{ns}/memory/{id}            the memory, its links and its history
//	POST /t/{ns}/memory/{id}/retract    hide it (reason required)
//	POST /t/{ns}/memory/{id}/move       make it part of the project's memory
//
// Every route is the usual 404 while the switch is off, and for a memory the
// person does not read. The service decides who may retract or move; the
// buttons only appear for those it would accept.

const (
	// newThisWeek is how far back the New this week list reaches.
	newThisWeek = 7 * 24 * time.Hour
	// memoryListLimit is the most rows a list shows.
	memoryListLimit = memory.MaxSearchLimit
)

func (h *handler) routeMemoryTab(mux *http.ServeMux) {
	mux.HandleFunc("GET /t/{ns}/memory", h.inMemory(h.memoryPage))
	mux.HandleFunc("GET /t/{ns}/memory/setup", h.inMemory(h.memorySetup))
	mux.HandleFunc("GET /t/{ns}/memory/{id}", h.inMemory(h.memoryEntry))
	mux.HandleFunc("POST /t/{ns}/memory/{id}/retract", h.inMemory(h.memoryRetract))
	mux.HandleFunc("POST /t/{ns}/memory/{id}/move", h.inMemory(h.memoryMove))
}

// memoryOn reports whether the namespace of t shows team memory.
func (h *handler) memoryOn(ctx context.Context, t team) (bool, error) {
	if h.ui.Memory == nil {
		return false, nil
	}
	return h.ui.Memory.Enabled(ctx, t.ns())
}

// inMemory is inTeam for the Memory routes: memory that is off is a 404, as
// if the page did not exist.
func (h *handler) inMemory(next teamHandler) http.HandlerFunc {
	return h.inTeam(func(w http.ResponseWriter, r *http.Request, t team) {
		on, err := h.memoryOn(r.Context(), t)
		if err != nil {
			h.fail(w, r, err)
			return
		}
		if !on {
			h.notFound(w, r, t.user)
			return
		}
		next(w, r, t)
	})
}

func memoryBase(t team) string { return "/t/" + t.ns() + "/memory" }

func memoryURL(t team, id int64) string { return memoryBase(t) + "/" + strconv.FormatInt(id, 10) }

// memoryRow is one memory in a list.
type memoryRow struct {
	URL         string
	Name        string
	Description string
	Type        memory.Type
	// Layer is the namespace the memory lives in; Own is true when it is
	// the page's own.
	Layer string
	Own   bool
	// Author is who saved it first.
	Author string
	// EditedBy is the last editor when it is not the author.
	EditedBy    string
	CreatedAt   time.Time
	UpdatedAt   time.Time
	ConfirmedAt time.Time
	// Edited is true when someone changed it after it was saved.
	Edited      bool
	Unconfirmed bool
}

// memoryTypeOption is one choice of the type filter.
type memoryTypeOption struct {
	Value, Label string
	Selected     bool
}

// memoryView is the body of memory.html.
type memoryView struct {
	Namespace string
	Base      string
	Query     string
	Type      string
	Layer     string
	Types     []memoryTypeOption
	// Layers are the namespaces the filter offers; empty when there is
	// only one.
	Layers []string
	// Searched is true when the page shows search results, else New this
	// week.
	Searched bool
	Rows     []memoryRow
	// Full is true when the list stopped at its limit.
	Full bool
	// Filtered is true when a type or layer narrows the list.
	Filtered bool
}

func (h *handler) memoryPage(w http.ResponseWriter, r *http.Request, t team) {
	ctx, c := r.Context(), callerOf(t.user)
	layers, err := h.ui.Memory.Layers(ctx, c, t.ns())
	if err != nil {
		h.refused(w, r, t, err, "/t/"+t.ns())
		return
	}
	q := r.URL.Query()
	v := memoryView{Namespace: t.ns(), Base: memoryBase(t), Query: strings.TrimSpace(q.Get("q"))}
	if typ := memory.Type(q.Get("type")); typ.Valid() {
		v.Type = string(typ)
	}
	if l := q.Get("layer"); slices.Contains(layers, l) {
		v.Layer = l
	}
	if len(layers) > 1 {
		v.Layers = layers
	}
	v.Filtered = v.Type != "" || v.Layer != ""
	v.Types = []memoryTypeOption{{Value: "", Label: "Any type", Selected: v.Type == ""}}
	for _, typ := range memory.Types() {
		v.Types = append(v.Types, memoryTypeOption{Value: string(typ), Label: typeLabel(typ), Selected: string(typ) == v.Type})
	}

	var entries []memory.Entry
	if v.Query != "" {
		v.Searched = true
		query := memory.Query{Text: v.Query, Type: memory.Type(v.Type), Limit: memoryListLimit}
		if v.Layer != "" {
			query.Namespaces = []string{v.Layer}
		}
		entries, err = h.ui.Memory.Search(ctx, c, t.ns(), query)
	} else {
		entries, err = h.ui.Memory.Recent(ctx, c, t.ns(), h.ui.Memory.Now().Add(-newThisWeek), memoryListLimit)
		entries = slices.DeleteFunc(entries, func(e memory.Entry) bool {
			return (v.Type != "" && string(e.Type) != v.Type) || (v.Layer != "" && e.Namespace != v.Layer)
		})
	}
	if err != nil {
		h.refused(w, r, t, err, "/t/"+t.ns())
		return
	}
	v.Full = len(entries) >= memoryListLimit
	if v.Rows, err = h.memoryRows(ctx, t, entries); err != nil {
		h.fail(w, r, err)
		return
	}
	title := "Memory · " + t.ns()
	if v.Searched {
		title = "Search · " + title
	}
	h.renderTeam(w, r, http.StatusOK, "memory.html", "memory", t, uiView{Title: title, Notice: memoryNotice(q.Get("done")), Page: v})
}

// typeLabel is how a type reads in a list and a filter.
func typeLabel(t memory.Type) string {
	switch t {
	case memory.TypeFeedback:
		return "Feedback"
	case memory.TypeProject:
		return "Project"
	case memory.TypeReference:
		return "Reference"
	}
	return string(t)
}

// memoryRows turns entries into list rows, with the names of their authors.
func (h *handler) memoryRows(ctx context.Context, t team, entries []memory.Entry) ([]memoryRow, error) {
	ids := make([]string, 0, len(entries))
	for _, e := range entries {
		ids = append(ids, e.AuthorID)
	}
	names, err := h.ui.Teams.DisplayNames(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("memory authors: %w", err)
	}
	now := h.ui.Memory.Now()
	rows := make([]memoryRow, 0, len(entries))
	for _, e := range entries {
		rows = append(rows, memoryRow{
			URL: memoryURL(t, e.ID), Name: e.Name, Description: e.Description, Type: e.Type,
			Layer: e.Namespace, Own: e.Namespace == t.ns(), Author: nameOr(names, e.AuthorID, e.AuthorLabel),
			EditedBy: e.EditedBy, CreatedAt: e.CreatedAt, UpdatedAt: e.UpdatedAt, ConfirmedAt: e.ConfirmedAt,
			Edited: e.UpdatedAt.Sub(e.CreatedAt) >= time.Minute, Unconfirmed: e.Unconfirmed(now),
		})
	}
	return rows, nil
}

// nameOr is a person's display name, else the label they had when they acted.
func nameOr(names map[string]string, id, label string) string {
	if n := names[id]; n != "" {
		return n
	}
	return label
}

// memoryNotice is the line shown after retracting or moving.
func memoryNotice(done string) string {
	switch done {
	case "retracted":
		return "Retracted. The memory is hidden from search and from AI tools, and its history is kept."
	case "moved":
		return "Moved to the project. Every team in it reads this memory now."
	}
	return ""
}

// memoryVersion is one step of a memory's history.
type memoryVersion struct {
	Number int
	Action memory.Action
	// Verb is the step in words: Saved, Updated, Confirmed, Retracted, Moved.
	Verb   string
	Who    string
	At     time.Time
	Note   string
	Layer  string
	Latest bool
	// Diff is what the step changed from the one before; empty for the
	// first, and for a step that changed no text.
	Diff []diffFile
}

// memoryEntryView is the body of memory_entry.html.
type memoryEntryView struct {
	Namespace string
	Base      string
	URL       string
	Entry     memory.Entry
	Author    string
	Layer     string
	Own       bool
	// Unconfirmed is true when nobody checked it for six months.
	Unconfirmed bool
	Retracted   bool
	// RetractedBy, RetractedAt and Reason fill the notice of a retracted
	// memory.
	RetractedBy string
	RetractedAt time.Time
	Reason      string
	Links       []memoryRow
	// History is newest first.
	History    []memoryVersion
	CanRetract bool
	CanMove    bool
	// Project is where Move to project would put it.
	Project string
	// ReasonText is the reason typed into a form that was refused, and
	// OpenForm names the form to show open again: "retract" or "move".
	ReasonText string
	OpenForm   string
}

func (h *handler) memoryEntry(w http.ResponseWriter, r *http.Request, t team) {
	id, ok := memoryID(r)
	if !ok {
		h.notFound(w, r, t.user)
		return
	}
	rec, err := h.ui.Memory.History(r.Context(), callerOf(t.user), t.ns(), id)
	if err != nil {
		h.refused(w, r, t, err, memoryBase(t))
		return
	}
	h.renderMemoryEntry(w, r, t, http.StatusOK, rec, "", "", "", "")
}

// memoryID reads the entry number in the path: a positive integer written
// without sign or leading zeros, so each entry has one address.
func memoryID(r *http.Request) (int64, bool) {
	raw := r.PathValue("id")
	n, err := strconv.ParseInt(raw, 10, 64)
	return n, err == nil && n > 0 && strconv.FormatInt(n, 10) == raw
}

// renderMemoryEntry shows rec. A refused form comes back with its message in
// errText, the form named by openForm open again and the typed reason kept.
func (h *handler) renderMemoryEntry(w http.ResponseWriter, r *http.Request, t team, status int, rec memory.Record, errText, field, openForm, reason string) {
	ctx := r.Context()
	e := rec.Entry
	linked, err := h.ui.Memory.Linked(ctx, callerOf(t.user), t.ns(), e.Links)
	if err != nil {
		h.refused(w, r, t, err, memoryBase(t))
		return
	}
	ids := []string{e.AuthorID}
	for _, v := range rec.Versions {
		ids = append(ids, v.By.UserID)
	}
	for _, l := range linked {
		ids = append(ids, l.AuthorID)
	}
	names, err := h.ui.Teams.DisplayNames(ctx, ids)
	if err != nil {
		h.fail(w, r, fmt.Errorf("memory authors: %w", err))
		return
	}
	v := memoryEntryView{
		Namespace: t.ns(), Base: memoryBase(t), URL: memoryURL(t, e.ID), Entry: e,
		Author: nameOr(names, e.AuthorID, e.AuthorLabel), Layer: e.Namespace, Own: e.Namespace == t.ns(),
		Unconfirmed: e.Unconfirmed(h.ui.Memory.Now()), Retracted: !e.Active(),
		CanRetract: rec.CanRetract, CanMove: rec.CanMove, ReasonText: reason, OpenForm: openForm,
	}
	if v.CanMove {
		v.Project = t.place.Namespace.Parent
		if e.Namespace != t.ns() {
			v.Project = ""
		}
	}
	for _, l := range linked {
		rows, err := h.memoryRows(ctx, t, []memory.Entry{l})
		if err != nil {
			h.fail(w, r, err)
			return
		}
		v.Links = append(v.Links, rows...)
	}
	v.History = memoryHistory(rec.Versions, names)
	if v.Retracted {
		if last, ok := lastAction(rec.Versions, memory.ActionRetract); ok {
			v.RetractedBy, v.RetractedAt, v.Reason = nameOr(names, last.By.UserID, last.By.Label), last.At, last.Note
		}
	}
	h.renderTeam(w, r, status, "memory_entry.html", "memory", t, uiView{
		Title: e.Name + " · Memory · " + t.ns(), Notice: memoryNotice(r.URL.Query().Get("done")), Error: errText, Field: field, Page: v,
	})
}

// lastAction is the newest version with the action.
func lastAction(versions []memory.Version, a memory.Action) (memory.Version, bool) {
	for _, v := range slices.Backward(versions) {
		if v.Action == a {
			return v, true
		}
	}
	return memory.Version{}, false
}

// memoryHistory lists the steps newest first, each with its change from the
// step before.
func memoryHistory(versions []memory.Version, names map[string]string) []memoryVersion {
	out := make([]memoryVersion, 0, len(versions))
	for i, v := range versions {
		step := memoryVersion{
			Number: v.Number, Action: v.Action, Verb: actionVerb(v.Action), Who: nameOr(names, v.By.UserID, v.By.Label),
			At: v.At, Note: v.Note, Layer: v.Namespace, Latest: i == len(versions)-1,
		}
		if i > 0 {
			// A diff that is too large is left out: the steps still say what happened.
			if d, err := textdiff.Unified("a", "b", snapshot(versions[i-1]), snapshot(v), 3); err == nil && d != "" {
				step.Diff = []diffFile{{Name: "Version " + strconv.Itoa(v.Number), Lines: diffLines(d)}}
			}
		}
		out = append(out, step)
	}
	slices.Reverse(out)
	return out
}

func actionVerb(a memory.Action) string {
	switch a {
	case memory.ActionSave:
		return "Saved"
	case memory.ActionUpdate:
		return "Updated"
	case memory.ActionConfirm:
		return "Confirmed"
	case memory.ActionRetract:
		return "Retracted"
	case memory.ActionMove:
		return "Moved"
	}
	return string(a)
}

// snapshot is the text of a version as lines, so a change shows as the lines
// that differ: the fields first, then the body.
func snapshot(v memory.Version) string {
	links := make([]string, len(v.Links))
	for i, l := range v.Links {
		links[i] = strconv.FormatInt(l, 10)
	}
	var b strings.Builder
	b.WriteString("Name: " + v.Name + "\n")
	b.WriteString("Description: " + v.Description + "\n")
	b.WriteString("Type: " + string(v.Type) + "\n")
	b.WriteString("Team: " + v.Namespace + "\n")
	b.WriteString("Links: " + strings.Join(links, ", ") + "\n\n")
	b.WriteString(v.Body)
	if !strings.HasSuffix(v.Body, "\n") {
		b.WriteString("\n")
	}
	return b.String()
}

// memoryAct runs a Retract or a Move for the entry in the path. It checks
// the person reads the entry through this page first, and answers a refused
// input on the entry's own page with the form open again.
func (h *handler) memoryAct(w http.ResponseWriter, r *http.Request, t team, form string, act func(context.Context, memory.Caller, int64) error) {
	id, ok := memoryID(r)
	if !ok {
		h.notFound(w, r, t.user)
		return
	}
	ctx, c := r.Context(), callerOf(t.user)
	rec, err := h.ui.Memory.History(ctx, c, t.ns(), id)
	if err != nil {
		h.refused(w, r, t, err, memoryBase(t))
		return
	}
	err = act(ctx, c, id)
	if errors.Is(err, registry.ErrInvalid) {
		field := ""
		if form == "retract" {
			field = "mem-reason"
		}
		h.renderMemoryEntry(w, r, t, http.StatusBadRequest, rec, sentence(err), field, form, r.PostForm.Get("reason"))
		return
	}
	if err != nil {
		h.refused(w, r, t, err, memoryURL(t, id))
		return
	}
	if h.ui.MemoryChanged != nil {
		h.ui.MemoryChanged()
	}
	done := "retracted"
	if form == "move" {
		done = "moved"
	}
	back(w, r, memoryURL(t, id), done, nil)
}

func (h *handler) memoryRetract(w http.ResponseWriter, r *http.Request, t team) {
	reason := r.PostForm.Get("reason")
	h.memoryAct(w, r, t, "retract", func(ctx context.Context, c memory.Caller, id int64) error {
		_, err := h.ui.Memory.Retract(ctx, c, id, reason)
		return err
	})
}

func (h *handler) memoryMove(w http.ResponseWriter, r *http.Request, t team) {
	h.memoryAct(w, r, t, "move", func(ctx context.Context, c memory.Caller, id int64) error {
		_, err := h.ui.Memory.Move(ctx, c, id)
		return err
	})
}

// memorySetupView is the body of memory_setup.html.
type memorySetupView struct {
	Namespace string
	Base      string
	// Endpoint is the team's MCP address.
	Endpoint string
	// Origin is the registry's address.
	Origin  string
	Curl    string
	Bridge  string
	Config  string
	Account string
}

func (h *handler) memorySetup(w http.ResponseWriter, r *http.Request, t team) {
	endpoint := h.origin + "/mcp/t/" + t.ns()
	v := memorySetupView{
		Namespace: t.ns(), Base: memoryBase(t), Endpoint: endpoint, Origin: h.origin, Account: "/account",
		Curl: "curl " + endpoint + ` \
  -H "Authorization: Bearer $LOADOUT_MEMORY_TOKEN" \
  -H "Content-Type: application/json" \
  -H "Accept: application/json, text/event-stream" \
  -d '{"jsonrpc":"2.0","id":1,"method":"tools/list"}'`,
		Bridge: "loadout mcp --team " + t.ns() + " --remote " + h.origin,
		Config: `{
  "mcpServers": {
    "loadout-memory-` + t.ns() + `": {
      "type": "http",
      "url": "` + endpoint + `",
      "headers": {
        "Authorization": "Bearer YOUR_MEMORY_TOKEN"
      }
    }
  }
}`,
	}
	h.renderTeam(w, r, http.StatusOK, "memory_setup.html", "memory", t, uiView{Title: "Set up · Memory · " + t.ns(), Page: v})
}
