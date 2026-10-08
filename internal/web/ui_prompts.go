package web

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/petervlad/loadout/internal/registry"
)

// promptView is one active prompt of a proposal on its page. Text is
// untrusted and shown as escaped text with its line breaks, never as
// Markdown.
type promptView struct {
	Seq    int
	Text   string
	By     string
	At     time.Time
	Edited bool
	// Mine offers Edit and Remove: only the person who added a prompt may
	// change it, as the proposal service checks again.
	Mine bool
	// NotRun marks a prompt the shown report did not ask.
	NotRun bool
}

// promptsView is the Prompts panel of a proposal.
type promptsView struct {
	List []promptView
	Max  int
	Open bool
	// CanAdd is true while the proposal is open and below Max.
	CanAdd bool
	// Full is true for an open proposal at Max.
	Full bool
	// Last is true when only one prompt is left, which the service refuses
	// to remove.
	Last   bool
	Notice string
}

// promptsOf builds the Prompts panel. ran lists the texts the shown report
// asked, nil when no report with data is shown.
func promptsOf(t team, p registry.Proposal, names map[string]string, ran map[string]bool, done string) promptsView {
	open := p.Status == registry.ProposalOpen
	v := promptsView{
		Max:    registry.MaxPrompts,
		Open:   open,
		CanAdd: open && len(p.Prompts) < registry.MaxPrompts,
		Full:   open && len(p.Prompts) >= registry.MaxPrompts,
		Last:   len(p.Prompts) == 1,
		Notice: promptNotice(done),
	}
	for _, pr := range p.Prompts {
		v.List = append(v.List, promptView{
			Seq:    pr.Seq,
			Text:   pr.Text,
			By:     who(pr.Author, names),
			At:     pr.CreatedAt,
			Edited: !pr.EditedAt.IsZero(),
			Mine:   open && t.is(pr.Author),
			NotRun: ran != nil && !ran[strings.TrimSpace(pr.Text)],
		})
	}
	return v
}

// is reports whether a is the viewer: their session's token, or any token
// of their user.
func (t team) is(a registry.Actor) bool {
	return a.TokenID == t.pr.TokenID || (a.UserID != "" && a.UserID == t.user.ID)
}

// promptNotice is the line the Prompts panel shows after a change.
func promptNotice(done string) string {
	switch done {
	case "prompt-added":
		return "Prompt added. The next comparison asks it."
	case "prompt-edited":
		return "Prompt saved."
	case "prompt-removed":
		return "Prompt removed."
	}
	return ""
}

// promptEvents are the activity lines of prompts added after the proposal,
// edited or removed. The author's own prompts from propose count in the
// proposal's line instead; the number of those is returned.
func promptEvents(p registry.Proposal, names map[string]string) (proposed int, out []event) {
	all := append(append([]registry.Prompt{}, p.Prompts...), p.RemovedPrompts...)
	for _, pr := range all {
		quote := "“" + firstLine(pr.Text, 60) + "”"
		link := ""
		if pr.RemovedAt.IsZero() {
			link = "#prompt-" + strconv.Itoa(pr.Seq)
		}
		switch {
		case sameActor(pr.Author, p.Author) && pr.CreatedAt.Equal(p.CreatedAt):
			proposed++
		case p.Replaces != "" && pr.CreatedAt.Before(p.CreatedAt):
			// Carried over from the proposal this one replaced, with its
			// author and time.
			out = append(out, event{Who: who(pr.Author, names), What: "added a prompt", Where: "on " + p.Replaces, Quote: quote, At: pr.CreatedAt, Tone: "plain", Href: link})
		default:
			out = append(out, event{Who: who(pr.Author, names), What: "added a prompt", Quote: quote, At: pr.CreatedAt, Tone: "plain", Href: link})
		}
		if !pr.EditedAt.IsZero() {
			out = append(out, event{Who: who(pr.Author, names), What: "edited a prompt", Quote: quote, At: pr.EditedAt, Tone: "plain", Href: link})
		}
		if !pr.RemovedAt.IsZero() {
			out = append(out, event{Who: who(pr.RemovedBy, names), What: "removed a prompt", Quote: quote, At: pr.RemovedAt, Tone: "plain"})
		}
	}
	return proposed, out
}

// sameActor mirrors the registry's rule: one token, or tokens of one user.
func sameActor(a, b registry.Actor) bool {
	return a.TokenID == b.TokenID || (a.UserID != "" && a.UserID == b.UserID)
}

// addPrompt, editPrompt and removePrompt change the prompts of a proposal
// from its page. The proposal service holds the rules (who, how many, how
// long); these only read the form and say what happened.
func (h *handler) addPrompt(w http.ResponseWriter, r *http.Request, t team) {
	p, ok := h.proposal(w, r, t)
	if !ok {
		return
	}
	_, err := h.ui.Proposals.AddPrompt(r.Context(), t.pr, p.ID, r.PostForm.Get("text"))
	h.promptDone(w, r, t, p.ID, err, "prompt-added")
}

func (h *handler) editPrompt(w http.ResponseWriter, r *http.Request, t team) {
	p, ok := h.proposal(w, r, t)
	if !ok {
		return
	}
	seq, ok := h.promptSeq(w, r, t)
	if !ok {
		return
	}
	_, err := h.ui.Proposals.EditPrompt(r.Context(), t.pr, p.ID, seq, r.PostForm.Get("text"))
	h.promptDone(w, r, t, p.ID, err, "prompt-edited")
}

func (h *handler) removePrompt(w http.ResponseWriter, r *http.Request, t team) {
	p, ok := h.proposal(w, r, t)
	if !ok {
		return
	}
	seq, ok := h.promptSeq(w, r, t)
	if !ok {
		return
	}
	err := h.ui.Proposals.RemovePrompt(r.Context(), t.pr, p.ID, seq)
	h.promptDone(w, r, t, p.ID, err, "prompt-removed")
}

// promptSeq reads the prompt number of the path; anything but a small
// positive number is a 404.
func (h *handler) promptSeq(w http.ResponseWriter, r *http.Request, t team) (int, bool) {
	seq, err := strconv.Atoi(r.PathValue("seq"))
	if err != nil || seq < 1 || seq > 1000 {
		h.notFound(w, r, t.user)
		return 0, false
	}
	return seq, true
}

// promptDone goes back to the Prompts panel, or says why the service
// refused. A prompt that is gone or a proposal that closed meanwhile is a
// conflict with its own sentence, not a missing page: the proposal is
// still there.
func (h *handler) promptDone(w http.ResponseWriter, r *http.Request, t team, id string, err error, done string) {
	page := "/t/" + t.ns() + "/reviews/" + id
	switch {
	case errors.Is(err, registry.ErrNotFound):
		h.explain(w, r, t, http.StatusConflict, "Something changed", "That prompt was removed, so it cannot change any more.", page)
		return
	case errors.Is(err, registry.ErrProposalClosed):
		h.explain(w, r, t, http.StatusConflict, "Something changed", "This proposal is closed, so its prompts cannot change any more.", page)
		return
	case err != nil:
		h.refused(w, r, t, err, page)
		return
	}
	h.log.InfoContext(r.Context(), "prompt changed on the web", "proposal", id, "done", done, "user_id", t.user.ID)
	// Back to the panel, where the notice is, not to the top of the page.
	http.Redirect(w, r, page+"?done="+done+"#prompts", http.StatusSeeOther)
}
