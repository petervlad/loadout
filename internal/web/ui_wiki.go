package web

import (
	"context"
	"errors"
	"fmt"
	"html/template"
	"io"
	"mime"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/petervlad/loadout/internal/account"
	"github.com/petervlad/loadout/internal/registry"
	"github.com/petervlad/loadout/internal/textdiff"
	"github.com/petervlad/loadout/internal/wiki"
)

// The wiki of a namespace, one Markdown page, while its switch is on:
//
//	GET  /t/{ns}/wiki                the newest revision, rendered (?source=1)
//	GET  /t/{ns}/wiki/edit           edit the newest (?from=N starts from an old revision)
//	POST /t/{ns}/wiki/edit           the same form again, as Write or Preview (view=)
//	POST /t/{ns}/wiki                save the form as the next revision
//	GET  /t/{ns}/wiki/history        who saved which revision, and when
//	GET  /t/{ns}/wiki/r/{seq}        one revision (?source=1)
//	GET  /t/{ns}/wiki/r/{seq}/diff   its changes from the revision before
//	GET  /t/{ns}/wiki/r/{seq}/raw    its text, as an attachment
//	GET  /t/{ns}/wiki/r/{seq}/draw   a diagram's sandboxed frame (?block=K, ?drawio=K)
//
// A wiki that is off is the usual 404 on every route. Every route under
// r/{seq} names one stored revision, so a frame only ever draws text
// someone saved, never text from a form or a link.

// Wiki is what the wiki pages need from the account service.
type Wiki interface {
	Wiki(ctx context.Context, actor account.Actor, namespace string, seq int) (wiki.Revision, error)
	WikiHistory(ctx context.Context, actor account.Actor, namespace string) ([]wiki.Info, error)
	SaveWiki(ctx context.Context, actor account.Actor, namespace string, base int, doc wiki.Doc, note string) (wiki.Info, bool, error)
}

func (h *handler) routeWiki(mux *http.ServeMux) {
	mux.HandleFunc("POST /t/{ns}/settings/wiki", h.inTeam(h.saveWikiSwitch))
	mux.HandleFunc("POST /t/{ns}/settings/course", h.inTeam(h.saveCourse))
	mux.HandleFunc("GET /t/{ns}/wiki", h.inWiki(h.wikiPage))
	mux.HandleFunc("POST /t/{ns}/wiki", h.inWiki(h.saveWiki))
	mux.HandleFunc("GET /t/{ns}/wiki/edit", h.inWiki(h.wikiEdit))
	mux.HandleFunc("POST /t/{ns}/wiki/edit", h.inWiki(h.wikiEdit))
	mux.HandleFunc("GET /t/{ns}/wiki/history", h.inWiki(h.wikiHistory))
	mux.HandleFunc("GET /t/{ns}/wiki/r/{seq}", h.inWiki(h.wikiRevision))
	mux.HandleFunc("GET /t/{ns}/wiki/r/{seq}/diff", h.inWiki(h.wikiDiff))
	mux.HandleFunc("GET /t/{ns}/wiki/r/{seq}/raw", h.inWiki(h.wikiRaw))
	mux.HandleFunc("GET /t/{ns}/wiki/r/{seq}/draw", h.inWiki(h.wikiFrame))
}

// wikiOn reports whether the namespace of t shows a wiki.
func (h *handler) wikiOn(t team) bool { return h.ui.Wiki != nil && t.place.Namespace.Wiki }

// inWiki is inTeam for the wiki routes: a wiki that is off is a 404, as
// if the page did not exist.
func (h *handler) inWiki(next teamHandler) http.HandlerFunc {
	return h.inTeam(func(w http.ResponseWriter, r *http.Request, t team) {
		if !h.wikiOn(t) {
			h.notFound(w, r, t.user)
			return
		}
		next(w, r, t)
	})
}

// canEditWiki reports whether the user may save the wiki of t: a role of
// their own there, not only reading it through a team below.
func canEditWiki(t team) bool { return t.member() && t.place.Access.CanEditWiki() }

// wikiBase is the wiki's address in t.
func wikiBase(t team) string { return "/t/" + t.ns() + "/wiki" }

// revisionURL is the page of revision seq.
func revisionURL(t team, seq int) string { return wikiBase(t) + "/r/" + strconv.Itoa(seq) }

// wikiView is the body of wiki.html: the newest revision or an older one,
// or the empty page.
type wikiView struct {
	Namespace string
	Base      string
	// Rev is the revision shown; Seq 0 is the empty page.
	Rev wiki.Info
	// Author names who saved it: their name, else the email they had.
	Author string
	// Newest is the newest revision's number; it differs from Rev.Seq on
	// an old revision.
	Newest int
	// HTML is the rendered page and Note what it leaves undrawn; Source
	// shows Text instead.
	HTML   template.HTML
	Note   string
	Source bool
	Text   string
	// Self is this page, for the Show source switch; Raw downloads the
	// text as File.
	Self, Raw, File string
	CanEdit         bool
	// Command saves a page from the CLI.
	Command string
	// Parent is the project above whose wiki is on, linked from a team's.
	Parent string
	// Course is the Course box at the end of the page: only on the
	// page's own address, rendered, never on an old revision or the
	// source.
	Course *courseView
}

// Old reports whether the page shows a revision older than the newest.
func (v wikiView) Old() bool { return v.Rev.Seq != v.Newest }

func (h *handler) wikiPage(w http.ResponseWriter, r *http.Request, t team) {
	rev, err := h.ui.Wiki.Wiki(r.Context(), t.actor(), t.ns(), 0)
	if err != nil {
		h.refused(w, r, t, err, "/t/"+t.ns())
		return
	}
	h.renderWiki(w, r, t, rev, rev.Seq)
}

func (h *handler) wikiRevision(w http.ResponseWriter, r *http.Request, t team) {
	rev, ok := h.openRevision(w, r, t)
	if !ok {
		return
	}
	newest, err := h.ui.Wiki.Wiki(r.Context(), t.actor(), t.ns(), 0)
	if err != nil {
		h.refused(w, r, t, err, wikiBase(t))
		return
	}
	h.renderWiki(w, r, t, rev, newest.Seq)
}

// openRevision reads the revision in the path, or answers 404 and returns
// false. Only the canonical number names a revision, so each has one URL.
func (h *handler) openRevision(w http.ResponseWriter, r *http.Request, t team) (wiki.Revision, bool) {
	seq, ok := canonicalSeq(r.PathValue("seq"))
	if !ok {
		h.notFound(w, r, t.user)
		return wiki.Revision{}, false
	}
	rev, err := h.ui.Wiki.Wiki(r.Context(), t.actor(), t.ns(), seq)
	if err != nil {
		h.refused(w, r, t, err, wikiBase(t))
		return wiki.Revision{}, false
	}
	return rev, true
}

// canonicalSeq reads a revision number: a positive integer written
// without sign or leading zeros.
func canonicalSeq(raw string) (int, bool) {
	n, err := strconv.Atoi(raw)
	return n, err == nil && n > 0 && strconv.Itoa(n) == raw
}

// renderWiki shows rev, which is the newest when its number is newest.
func (h *handler) renderWiki(w http.ResponseWriter, r *http.Request, t team, rev wiki.Revision, newest int) {
	v := wikiView{
		Namespace: t.ns(), Base: wikiBase(t), Rev: rev.Info, Newest: newest, CanEdit: canEditWiki(t),
		Self: r.URL.Path, File: rev.Doc.Format().File(), Command: "loadout wiki push wiki.md --team " + t.ns(),
		Parent: wikiParent(t),
	}
	title := "Wiki · " + t.ns()
	if rev.Seq > 0 {
		names, err := h.authorNames(r.Context(), rev.Info)
		if err != nil {
			h.fail(w, r, err)
			return
		}
		v.Author = authorOf(names, rev.Info)
		v.Raw = revisionURL(t, rev.Seq) + "/raw"
		if r.URL.Query().Get("source") == "1" {
			v.Source, v.Text = true, rev.Doc.Text()
		} else {
			v.HTML, v.Note = wikiDocument(rev.Doc.Text(), rev.Doc.Format(), revisionURL(t, rev.Seq)+"/draw")
		}
		if v.Old() {
			title = "Revision " + strconv.Itoa(rev.Seq) + " · " + title
		}
	}
	if r.URL.Path == wikiBase(t) && !v.Source {
		v.Course = h.courseBox(r.Context(), t, r.URL.Query())
		if v.Course != nil && v.Course.Open != nil {
			title = v.Course.Open.Title + " · " + title
		}
	}
	// The page draws its diagrams in frames of this origin.
	w.Header().Set("Content-Security-Policy", filePageCSP)
	h.renderTeam(w, r, http.StatusOK, "wiki.html", "wiki", t, uiView{Title: title, Notice: wikiNotice(r.URL.Query()), Page: v})
}

// wikiParent is the project above t whose wiki is on and which the user
// can open, or "": a team's wiki links to it rather than including it.
func wikiParent(t team) string {
	parent := t.place.Namespace.Parent
	if parent == "" {
		return ""
	}
	i := slices.IndexFunc(t.places, func(p account.Place) bool { return p.Namespace.Name == parent })
	if i < 0 || !t.places[i].Namespace.Wiki {
		return ""
	}
	return parent
}

// wikiNotice is the line shown after a save.
func wikiNotice(q url.Values) string {
	switch q.Get("done") {
	case "saved":
		if _, ok := canonicalSeq(q.Get("rev")); ok {
			return "Saved revision " + q.Get("rev") + "."
		}
		return "Saved."
	case "same":
		return "Nothing changed, so no revision was saved."
	}
	return ""
}

// authorNames are the display names of the authors of infos, by user id.
func (h *handler) authorNames(ctx context.Context, infos ...wiki.Info) (map[string]string, error) {
	ids := make([]string, 0, len(infos))
	for _, in := range infos {
		ids = append(ids, in.AuthorUserID)
	}
	names, err := h.ui.Teams.DisplayNames(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("wiki authors: %w", err)
	}
	return names, nil
}

// authorOf names who saved in: their name today, else the email they had
// when they saved (a deleted user keeps it).
func authorOf(names map[string]string, in wiki.Info) string {
	if n := names[in.AuthorUserID]; n != "" {
		return n
	}
	return in.Author
}

// wikiEditView is the body of wiki_edit.html.
type wikiEditView struct {
	Namespace string
	Base      string
	// Rev is the revision the text started from, 0 for a new page; a save
	// is refused unless it is still the newest.
	Rev int
	// From is the older revision the text was copied from, or 0.
	From   int
	Format wiki.Format
	Text   string
	Note   string
	// Preview shows the text rendered, with RenderNote saying what the
	// preview does not draw; the text stays in the form, hidden.
	Preview    bool
	HTML       template.HTML
	RenderNote string
	// Stale is set when someone saved while this text was being edited.
	Stale *wikiStale
}

// MDX reports whether the page is MDX, for its hint.
func (v wikiEditView) MDX() bool { return v.Format == wiki.MDX }

// wikiStale is what a save that came too late shows: who saved in
// between and what they changed.
type wikiStale struct {
	// Since is the revision the edit started from; Seq is the newest.
	Since, Seq int
	Author     string
	// Diff is the change from Since to Seq; nil when it cannot be shown
	// (the revision was pruned, or the change is too large), and Link
	// leads to the newest instead.
	Diff []diffFile
	Link string
}

// wikiEdit is the edit page. A GET starts from the newest revision, or
// from ?from=N; a POST shows the posted text again, as Write or Preview,
// so switching between them works without script and loses nothing.
func (h *handler) wikiEdit(w http.ResponseWriter, r *http.Request, t team) {
	if !canEditWiki(t) {
		h.explain(w, r, t, http.StatusForbidden, "Not allowed",
			"Editing the wiki of "+t.ns()+" needs a role in "+t.ns()+". Members of a team below only read it.", wikiBase(t))
		return
	}
	if r.Method == http.MethodPost {
		v := wikiForm(r)
		v.Preview = r.PostForm.Get("view") == "preview"
		if _, err := wiki.NewDoc(v.Format, v.Text); err != nil && v.Preview {
			v.Preview = false
			h.renderWikiEdit(w, r, t, http.StatusBadRequest, v, sentence(err), "wiki-text")
			return
		}
		h.renderWikiEdit(w, r, t, http.StatusOK, v, "", "")
		return
	}
	newest, err := h.ui.Wiki.Wiki(r.Context(), t.actor(), t.ns(), 0)
	if err != nil {
		h.refused(w, r, t, err, wikiBase(t))
		return
	}
	v := wikiEditView{Rev: newest.Seq, Format: newest.Doc.Format(), Text: newest.Doc.Text()}
	if raw := r.URL.Query().Get("from"); raw != "" {
		seq, ok := canonicalSeq(raw)
		if !ok {
			h.notFound(w, r, t.user)
			return
		}
		if seq != newest.Seq {
			old, err := h.ui.Wiki.Wiki(r.Context(), t.actor(), t.ns(), seq)
			if err != nil {
				h.refused(w, r, t, err, wikiBase(t))
				return
			}
			v.From, v.Format, v.Text = seq, old.Doc.Format(), old.Doc.Text()
		}
	}
	h.renderWikiEdit(w, r, t, http.StatusOK, v, "", "")
}

// wikiForm reads the edit form. Line endings become LF, as the page is
// stored, so Write and Preview give back the same text.
func wikiForm(r *http.Request) wikiEditView {
	f := r.PostForm
	v := wikiEditView{Text: toLF(f.Get("text")), Note: f.Get("note"), Format: wiki.Format(f.Get("format"))}
	if !v.Format.Valid() {
		v.Format = wiki.Markdown
	}
	if n, err := strconv.Atoi(f.Get("base")); err == nil && n > 0 {
		v.Rev = n
	}
	if n, ok := canonicalSeq(f.Get("from")); ok {
		v.From = n
	}
	return v
}

// toLF turns CRLF and lone CR into LF: browsers send a textarea's text
// with CRLF.
func toLF(s string) string {
	return strings.ReplaceAll(strings.ReplaceAll(s, "\r\n", "\n"), "\r", "\n")
}

// renderWikiEdit shows the edit page with v, and errText about the field
// with the id field, if any.
func (h *handler) renderWikiEdit(w http.ResponseWriter, r *http.Request, t team, status int, v wikiEditView, errText, field string) {
	v.Namespace, v.Base = t.ns(), wikiBase(t)
	if v.Preview {
		v.HTML, v.RenderNote = wikiDocument(v.Text, v.Format, "")
	}
	h.renderTeam(w, r, status, "wiki_edit.html", "wiki", t, uiView{Title: "Edit the wiki · " + t.ns(), Error: errText, Field: field, Page: v})
}

// saveWiki saves the posted text as the next revision. A save that comes
// too late, or a text the service refuses, shows the edit page again
// with the text in it, never an error page, so nothing typed is lost.
func (h *handler) saveWiki(w http.ResponseWriter, r *http.Request, t team) {
	v := wikiForm(r)
	doc, err := wiki.NewDoc(v.Format, v.Text)
	if err != nil {
		h.renderWikiEdit(w, r, t, http.StatusBadRequest, v, sentence(err), "wiki-text")
		return
	}
	info, saved, err := h.ui.Wiki.SaveWiki(r.Context(), t.actor(), t.ns(), v.Rev, doc, v.Note)
	if stale, ok := errors.AsType[*registry.StaleError](err); ok {
		h.staleWiki(w, r, t, v, stale)
		return
	}
	if errors.Is(err, registry.ErrInvalid) {
		field := "wiki-text"
		if note := strings.TrimSpace(v.Note); strings.ContainsAny(note, "\r\n") || utf8.RuneCountInString(note) > wiki.MaxNote {
			field = "wiki-note"
		}
		h.renderWikiEdit(w, r, t, http.StatusBadRequest, v, sentence(err), field)
		return
	}
	if err != nil {
		h.refused(w, r, t, err, wikiBase(t))
		return
	}
	if !saved {
		back(w, r, wikiBase(t), "same", nil)
		return
	}
	back(w, r, wikiBase(t), "saved", url.Values{"rev": {strconv.Itoa(info.Seq)}})
}

// staleWiki answers a save based on an old revision: 409 and the edit
// page with the person's text, what changed in between, and the newest
// revision as the new base, so the next Save is a deliberate one.
func (h *handler) staleWiki(w http.ResponseWriter, r *http.Request, t team, v wikiEditView, stale *registry.StaleError) {
	n := stale.Newest
	names, err := h.authorNames(r.Context(), n)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	s := &wikiStale{Since: stale.Base, Seq: n.Seq, Author: authorOf(names, n), Link: revisionURL(t, n.Seq)}
	diff, err := h.wikiChange(r.Context(), t, stale.Base, n.Seq)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	if diff != "" {
		s.Diff = []diffFile{{Name: n.Format.File(), Lines: diffLines(diff)}}
	}
	v.Rev, v.Stale, v.Preview = n.Seq, s, false
	msg := s.Author + " saved revision " + strconv.Itoa(n.Seq) + " while you were editing. Your text is still below. Look at what changed, merge it into your text, then save again."
	h.renderWikiEdit(w, r, t, http.StatusConflict, v, msg, "wiki-text")
}

// wikiChange is the unified diff from revision from (0: the empty page)
// to revision to, or "" when it cannot be shown: a revision is no longer
// kept, the texts are equal, or they are too long to compare.
func (h *handler) wikiChange(ctx context.Context, t team, from, to int) (string, error) {
	text := func(seq int) (string, bool, error) {
		if seq == 0 {
			return "", true, nil
		}
		rev, err := h.ui.Wiki.Wiki(ctx, t.actor(), t.ns(), seq)
		if errors.Is(err, registry.ErrNotFound) {
			return "", false, nil
		}
		if err != nil {
			return "", false, err
		}
		return rev.Doc.Text(), true, nil
	}
	a, ok, err := text(from)
	if err != nil || !ok {
		return "", err
	}
	b, ok, err := text(to)
	if err != nil || !ok {
		return "", err
	}
	diff, err := textdiff.Unified("a", "b", a, b, 3)
	if err != nil {
		// Too long to compare here; the caller links the revision.
		return "", nil //nolint:nilerr // a diff that cannot be shown is not a failure
	}
	return diff, nil
}

// wikiHistoryView is the body of wiki_history.html.
type wikiHistoryView struct {
	Namespace string
	Base      string
	Rows      []wikiRow
	// Full is true when the history holds as many revisions as are kept.
	Full bool
	Kept int
}

// wikiRow is one revision in the history.
type wikiRow struct {
	Seq    int
	Author string
	At     time.Time
	Note   string
	Size   int
	// Changes links the diff from the revision before, when it is kept.
	Changes bool
}

func (h *handler) wikiHistory(w http.ResponseWriter, r *http.Request, t team) {
	infos, err := h.ui.Wiki.WikiHistory(r.Context(), t.actor(), t.ns())
	if err != nil {
		h.refused(w, r, t, err, wikiBase(t))
		return
	}
	names, err := h.authorNames(r.Context(), infos...)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	kept := map[int]bool{}
	for _, in := range infos {
		kept[in.Seq] = true
	}
	v := wikiHistoryView{Namespace: t.ns(), Base: wikiBase(t), Full: len(infos) >= wiki.MaxRevisions, Kept: wiki.MaxRevisions}
	for _, in := range infos {
		v.Rows = append(v.Rows, wikiRow{
			Seq: in.Seq, Author: authorOf(names, in), At: in.CreatedAt, Note: in.Note, Size: in.Size,
			Changes: in.Seq > 1 && kept[in.Seq-1],
		})
	}
	h.renderTeam(w, r, http.StatusOK, "wiki_history.html", "wiki", t, uiView{Title: "History · Wiki · " + t.ns(), Page: v})
}

// wikiDiffView is the body of wiki_diff.html.
type wikiDiffView struct {
	Base string
	Seq  int
	Diff []diffFile
	// Same is true when the revision changed nothing in the text (only
	// its format); TooLarge when the change cannot be shown here.
	Same, TooLarge bool
}

// wikiDiff shows what revision seq changed from the one before. The
// first revision has nothing before it, and a revision whose predecessor
// is no longer kept has no diff here.
func (h *handler) wikiDiff(w http.ResponseWriter, r *http.Request, t team) {
	rev, ok := h.openRevision(w, r, t)
	if !ok {
		return
	}
	if rev.Seq == 1 {
		h.notFound(w, r, t.user)
		return
	}
	prev, err := h.ui.Wiki.Wiki(r.Context(), t.actor(), t.ns(), rev.Seq-1)
	if err != nil {
		h.refused(w, r, t, err, wikiBase(t))
		return
	}
	v := wikiDiffView{Base: wikiBase(t), Seq: rev.Seq}
	diff, err := textdiff.Unified("a", "b", prev.Doc.Text(), rev.Doc.Text(), 3)
	switch {
	case err != nil:
		v.TooLarge = true
	case diff == "":
		v.Same = true
	default:
		v.Diff = []diffFile{{Name: rev.Doc.Format().File(), Lines: diffLines(diff)}}
	}
	h.renderTeam(w, r, http.StatusOK, "wiki_diff.html", "wiki", t, uiView{Title: "Revision " + strconv.Itoa(rev.Seq) + " changes · Wiki · " + t.ns(), Page: v})
}

// wikiRaw sends a revision's text as a file, plain text to the browser
// whatever it holds.
func (h *handler) wikiRaw(w http.ResponseWriter, r *http.Request, t team) {
	rev, ok := h.openRevision(w, r, t)
	if !ok {
		return
	}
	hd := w.Header()
	hd.Set("Content-Type", "text/plain; charset=utf-8")
	hd.Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": rev.Doc.Format().File()}))
	hd.Set("Content-Security-Policy", rawCSP)
	if _, err := io.WriteString(w, rev.Doc.Text()); err != nil {
		h.log.DebugContext(r.Context(), "write wiki", "err", err)
	}
}

// wikiFrame draws one diagram fence of a stored revision; the query only
// picks which.
func (h *handler) wikiFrame(w http.ResponseWriter, r *http.Request, t team) {
	rev, ok := h.openRevision(w, r, t)
	if !ok {
		return
	}
	src, ok := wikiFence(rev.Doc.Text(), r.URL.Query())
	if !ok {
		h.notFound(w, r, t.user)
		return
	}
	h.serveFrame(w, r, "Diagram in the wiki of "+t.ns(), src)
}
