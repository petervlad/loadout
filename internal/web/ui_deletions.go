package web

// Requests to delete a package, reviewed like proposals:
//
//	GET|POST /t/{ns}/p/{team}/{name}/delete-request   ask, with a required reason
//	GET      /t/{ns}/deletions/{id}                   one request, its footprint and its outcome
//	POST     /t/{ns}/deletions/{id}/approve|reject|withdraw
//
// They live under /deletions, not /reviews, where reviews/delete/{id} would
// conflict with reviews/{id}/diff. The service decides who may do what; the
// pages mirror its rules to show only the buttons that would work.

import (
	"context"
	"errors"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/petervlad/loadout/internal/pack"
	"github.com/petervlad/loadout/internal/registry"
)

// DeleteRequests is what the request pages need from the deletion service;
// nil leaves out their routes, the package page's "Ask to delete" and the
// deletions in the reviews list.
type DeleteRequests interface {
	Propose(ctx context.Context, pr registry.Principal, id pack.ID, reason string) (registry.Deletion, error)
	Footprint(ctx context.Context, pr registry.Principal, id pack.ID) (registry.PackageFootprint, error)
	Deletions(ctx context.Context, pr registry.Principal, team string, status registry.DeletionStatus) ([]registry.Deletion, error)
	Deletion(ctx context.Context, pr registry.Principal, id string) (registry.Deletion, error)
	Standing(ctx context.Context, d registry.Deletion) (registry.DeletionStanding, error)
	Approve(ctx context.Context, pr registry.Principal, id string, in registry.ApproveInput) (registry.Deletion, error)
	Reject(ctx context.Context, pr registry.Principal, id, note string) (registry.Deletion, error)
	Withdraw(ctx context.Context, pr registry.Principal, id, note string) (registry.Deletion, error)
}

var _ DeleteRequests = (*registry.DeletionService)(nil)

// deletionIDPattern accepts the ids the registry hands out; anything else is
// a 404 without a lookup.
var deletionIDPattern = regexp.MustCompile(`^del-[a-z0-9]{1,60}$`)

// requestLink is an open request to delete a package, as the pages name it.
// Path is empty when the viewer is no member of the package's namespace and
// so cannot open it.
type requestLink struct {
	ID   string
	Path string
}

// deleteFacts is what a package holds, which deleting it removes: the
// "footprint" list of the delete, ask and deletion pages.
type deleteFacts struct {
	Releases  []string
	Open      []openProposal
	Proposals int
	Reviews   int
	Prompts   int
	Courses   []string
	// Requests are the open requests to delete it.
	Requests []requestLink
}

// factsOf lists fp for the page of t.
func (h *handler) factsOf(t team, fp registry.PackageFootprint) deleteFacts {
	f := deleteFacts{Proposals: fp.Proposals, Reviews: fp.Reviews, Prompts: fp.Prompts, Courses: slices.Clone(fp.Courses)}
	for _, v := range fp.Releases {
		f.Releases = append(f.Releases, v.String())
	}
	for _, id := range fp.Open {
		f.Open = append(f.Open, openProposal{ID: id, Path: "/t/" + t.ns() + "/reviews/" + id})
	}
	for _, id := range fp.Deletions {
		f.Requests = append(f.Requests, requestLink{ID: id, Path: h.requestPath(t, fp.ID, id)})
	}
	return f
}

// requestPath is the page of request id for package pkg, or "" when the
// viewer is not a member of its namespace (they read it through a team
// below, and only members open the reviews of a namespace).
func (h *handler) requestPath(t team, pkg pack.ID, id string) string {
	for _, p := range t.places {
		if p.Namespace.Name == pkg.Team() && p.Via == "" {
			return "/t/" + pkg.Team() + "/deletions/" + id
		}
	}
	return ""
}

// deletionStatus is how a request's status looks.
func deletionStatus(d registry.Deletion) status {
	switch d.Status {
	case registry.DeletionOpen:
		return status{"Waiting for review", "warn"}
	case registry.DeletionApproved:
		if d.SelfApproved {
			return status{"Deleted without review", "warn"}
		}
		return status{"Deleted", "ok"}
	case registry.DeletionRejected:
		return status{"Rejected", "bad"}
	case registry.DeletionGone:
		return status{"Deleted another way", "plain"}
	}
	return status{"Withdrawn", "plain"}
}

// ---- ask

// askView is the body of ask.html.
type askView struct {
	ID    string
	Back  string
	Facts deleteFacts
	// Readers says who may have the package installed.
	Readers string
	// Reason is what the person typed, kept after an error.
	Reason string
	// Existing is the open request that already covers the package; the
	// form is then left out.
	Existing *requestLink
	// ExistingBy and ExistingAt say who asked and when.
	ExistingBy string
	ExistingAt time.Time
	// Decider names who decides, for the sentence.
	Team string
}

// askPage shows what a deletion would remove and the form that asks for it.
func (h *handler) askPage(w http.ResponseWriter, r *http.Request, t team) {
	id, ok := h.deleteTarget(w, r, t)
	if !ok {
		return
	}
	fp, err := h.ui.Requests.Footprint(r.Context(), t.pr, id)
	if err != nil {
		h.refused(w, r, t, err, packagePath(t, id))
		return
	}
	h.renderAsk(w, r, t, http.StatusOK, fp, "", "", "")
}

// askDeletion stores the request, or says what is missing.
func (h *handler) askDeletion(w http.ResponseWriter, r *http.Request, t team) {
	id, ok := h.deleteTarget(w, r, t)
	if !ok {
		return
	}
	ctx := r.Context()
	fp, err := h.ui.Requests.Footprint(ctx, t.pr, id)
	if err != nil {
		h.refused(w, r, t, err, packagePath(t, id))
		return
	}
	reason := strings.TrimSpace(r.PostFormValue("reason"))
	if reason == "" {
		h.renderAsk(w, r, t, http.StatusBadRequest, fp, reason, "Say why "+id.String()+" should be deleted. The person who decides reads it.", "ask-reason")
		return
	}
	d, err := h.ui.Requests.Propose(ctx, t.pr, id, reason)
	if errors.Is(err, registry.ErrExists) && len(fp.Deletions) > 0 {
		// A double submit, or someone asked first: go to the request that is open.
		h.toRequest(w, r, t, id, fp.Deletions[0], "exists")
		return
	}
	if err != nil {
		h.refused(w, r, t, err, packagePath(t, id))
		return
	}
	h.log.InfoContext(ctx, "deletion asked on the web", "deletion", d.ID, "package", id.String(), "user_id", t.user.ID)
	h.toRequest(w, r, t, id, d.ID, "asked")
}

// toRequest redirects to request did of package id, or to the package page
// when the viewer cannot open requests of its namespace.
func (h *handler) toRequest(w http.ResponseWriter, r *http.Request, t team, id pack.ID, did, done string) {
	if p := h.requestPath(t, id, did); p != "" {
		back(w, r, p, done, nil)
		return
	}
	back(w, r, packagePath(t, id), done, nil)
}

func (h *handler) renderAsk(w http.ResponseWriter, r *http.Request, t team, code int, fp registry.PackageFootprint, reason, errText, field string) {
	v := askView{
		ID:      fp.ID.String(),
		Back:    packagePath(t, fp.ID),
		Facts:   h.factsOf(t, fp),
		Readers: readersOf(t, fp.ID),
		Reason:  reason,
		Team:    fp.ID.Team(),
	}
	v.Facts.Requests = nil // the page says so itself when one is open
	if len(fp.Deletions) > 0 {
		d, err := h.ui.Requests.Deletion(r.Context(), t.pr, fp.Deletions[0])
		if err != nil {
			h.fail(w, r, err)
			return
		}
		names, err := h.deletionNames(r, d)
		if err != nil {
			h.fail(w, r, err)
			return
		}
		v.Existing = &requestLink{ID: d.ID, Path: h.requestPath(t, fp.ID, d.ID)}
		v.ExistingBy, v.ExistingAt = who(d.Author, names), d.CreatedAt
	}
	h.renderTeam(w, r, code, "ask.html", "packages", t, uiView{Title: "Ask to delete " + v.ID, Error: errText, Field: field, Page: v})
}

// deletionNames returns display names for the people behind requests.
func (h *handler) deletionNames(r *http.Request, ds ...registry.Deletion) (map[string]string, error) {
	var ids []string
	for _, d := range ds {
		ids = append(ids, d.Author.UserID, d.DecidedBy.UserID)
	}
	return h.ui.Teams.DisplayNames(r.Context(), ids)
}

// openRequest is the open request to delete id, nil when there is none, for
// the package page.
func (h *handler) openRequest(r *http.Request, t team, id pack.ID) (*requestNote, error) {
	ds, err := h.ui.Requests.Deletions(r.Context(), t.pr, id.Team(), registry.DeletionOpen)
	if err != nil {
		return nil, err
	}
	i := slices.IndexFunc(ds, func(d registry.Deletion) bool { return d.Package == id })
	if i < 0 {
		return nil, nil
	}
	names, err := h.deletionNames(r, ds[i])
	if err != nil {
		return nil, err
	}
	d := ds[i]
	return &requestNote{ID: d.ID, Path: h.requestPath(t, id, d.ID), By: who(d.Author, names), At: d.CreatedAt}, nil
}

// requestNote is the line "Deletion asked by bob, 2 days ago" on the
// package page.
type requestNote struct {
	ID   string
	Path string
	By   string
	At   time.Time
}

// ---- the request page

// deletionView is the body of deletion.html.
type deletionView struct {
	ID      string
	Package string
	Reason  string
	Status  status
	Open    bool
	Author  string
	Created time.Time
	// PackagePath links to the package while it is there.
	PackagePath string
	// Facts is what the package holds now (open), or what went (approved).
	Facts    *deleteFacts
	FactsAt  string
	Readers  string
	Newer    string
	Decision deletionDecision
	Outcome  *deletionOutcome
}

// deletionOutcome says how a closed request ended.
type deletionOutcome struct {
	Heading string
	// Lead, By, the time and Tail make one sentence: "Rejected by Alice 2
	// days ago, so the package stays."
	Lead string
	By   string
	At   time.Time
	Tail string
	Note string
}

// deletionDecision says what the viewer may do and why, mirroring the
// service's rules (which check again on every POST).
type deletionDecision struct {
	CanApprove  bool
	CanReject   bool
	CanWithdraw bool
	AsOwner     bool
	Why         string
}

func decideDeletion(t team, d registry.Deletion, st registry.DeletionStanding) deletionDecision {
	author := t.is(d.Author)
	blocked := "Approve is off: " + st.Newest.String() + " came out after this was asked. Withdraw the request and ask again."
	switch {
	case d.Status != registry.DeletionOpen:
		return deletionDecision{}
	case st.Stale && author:
		return deletionDecision{CanWithdraw: true, Why: blocked}
	case st.Stale && t.place.Access.CanApprove():
		return deletionDecision{CanReject: true, Why: blocked}
	case st.Stale:
		return deletionDecision{Why: blocked}
	case author && t.place.Owns:
		return deletionDecision{
			CanWithdraw: true, AsOwner: true,
			Why: "You asked for this. As an owner of " + t.ns() + " you may delete the package yourself; teammates will see that review was skipped.",
		}
	case author:
		return deletionDecision{CanWithdraw: true, Why: "You asked for this, so a teammate who maintains " + t.ns() + " approves it."}
	case !t.place.Access.CanApprove():
		return deletionDecision{Why: "Only maintainers of " + t.ns() + " approve or reject."}
	}
	return deletionDecision{
		CanApprove: true, CanReject: true,
		Why: "You can approve: you maintain " + t.ns() + " and you are not the author. Approving deletes the package for good, after a copy of the database is saved.",
	}
}

// requestOf loads the request of the page, answering 404 itself for an id
// that is not one of this namespace's requests.
func (h *handler) requestOf(w http.ResponseWriter, r *http.Request, t team) (registry.Deletion, bool) {
	id := r.PathValue("id")
	if !t.member() || !deletionIDPattern.MatchString(id) {
		h.notFound(w, r, t.user)
		return registry.Deletion{}, false
	}
	d, err := h.ui.Requests.Deletion(r.Context(), t.pr, id)
	if errors.Is(err, registry.ErrNotFound) || (err == nil && d.Package.Team() != t.ns()) {
		h.notFound(w, r, t.user)
		return registry.Deletion{}, false
	}
	if err != nil {
		h.fail(w, r, err)
		return registry.Deletion{}, false
	}
	return d, true
}

func (h *handler) deletionPage(w http.ResponseWriter, r *http.Request, t team) {
	d, ok := h.requestOf(w, r, t)
	if !ok {
		return
	}
	names, err := h.deletionNames(r, d)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	v := deletionView{
		ID: d.ID, Package: d.Package.String(), Reason: d.Reason, Status: deletionStatus(d),
		Open: d.Status == registry.DeletionOpen, Author: who(d.Author, names), Created: d.CreatedAt,
		Readers: readersOf(t, d.Package),
	}
	if v.Open {
		st, err := h.ui.Requests.Standing(r.Context(), d)
		if err != nil {
			h.fail(w, r, err)
			return
		}
		v.PackagePath = packagePath(t, d.Package)
		facts := h.factsOf(t, st.Footprint)
		facts.Requests = nil // the only open one is this page
		v.Facts, v.FactsAt = &facts, "This is what loadout removes."
		if st.Stale {
			v.Newer = st.Newest.String()
		}
		v.Decision = decideDeletion(t, d, st)
	}
	if d.Status == registry.DeletionApproved {
		facts := h.factsOf(t, d.Removed)
		facts.Open, facts.Requests = nil, nil
		v.Facts, v.FactsAt = &facts, "This is what loadout removed."
	}
	v.Outcome = outcomeOf(d, names)
	h.renderTeam(w, r, http.StatusOK, "deletion.html", "reviews", t, uiView{
		Title:  "Delete " + v.Package,
		Notice: deletionNotice(r.URL.Query().Get("done"), t.ns()),
		Page:   v,
	})
}

// outcomeOf describes how d ended, nil while it is open.
func outcomeOf(d registry.Deletion, names map[string]string) *deletionOutcome {
	if d.Status == registry.DeletionOpen {
		return nil
	}
	o := &deletionOutcome{At: d.DecidedAt, Note: d.DecisionNote}
	if d.DecidedBy != (registry.Actor{}) {
		o.By = who(d.DecidedBy, names)
	}
	switch d.Status {
	case registry.DeletionApproved:
		rm := d.Removed
		o.Heading = "Deleted"
		o.Lead = "Deleted " + plural(len(rm.Releases), "release") + ", " + plural(rm.Proposals, "proposal") + ", " + plural(rm.Reviews, "review") + " and " + plural(rm.Prompts, "prompt")
		if d.SelfApproved {
			o.Heading, o.Tail = "Deleted without review", "on their own request, without a second person's review"
		}
	case registry.DeletionRejected:
		o.Heading, o.Lead, o.Tail = "Rejected", "Rejected", "so the package stays"
	case registry.DeletionWithdrawn:
		o.Heading, o.Lead, o.Tail = "Withdrawn", "Withdrawn", "so the package stays"
	case registry.DeletionGone:
		o.Heading, o.Lead, o.Tail = "Deleted another way", "Closed", "because the package was deleted another way, directly or from the registry host"
	}
	return o
}

// deletionNotice is the line shown after an action on a request.
func deletionNotice(done, ns string) string {
	switch done {
	case "asked":
		return "Asked. A maintainer or owner of " + ns + " who is not you approves it or rejects it. You can withdraw it until then."
	case "exists":
		return "Someone already asked to delete this package. This is that request."
	case "approved":
		return "Approved. The package was deleted, and a copy of the database from before is in the backup folder."
	case "approved-as-owner":
		return "Deleted without review. The record says you approved your own request as owner."
	case "rejected":
		return "Rejected. The package stays."
	case "withdrawn":
		return "Withdrawn. The package stays."
	case "closed":
		return "That request was already closed. This is how it ended."
	}
	return ""
}

// packageNotice is the line the package page shows after asking for a
// deletion when the viewer cannot open requests of the package's namespace.
func packageNotice(done string, id pack.ID) string {
	switch done {
	case "asked":
		return "Asked. A maintainer or owner of " + id.Team() + " decides; you cannot open their reviews, so ask them if you need an answer sooner."
	case "exists":
		return "Someone already asked to delete this package, and a maintainer or owner of " + id.Team() + " decides."
	}
	return ""
}

// decideRequest approves, rejects or withdraws a request.
func (h *handler) decideRequest(w http.ResponseWriter, r *http.Request, t team) {
	d, ok := h.requestOf(w, r, t)
	if !ok {
		return
	}
	ctx, note := r.Context(), r.PostForm.Get("note")
	var (
		got  registry.Deletion
		err  error
		done string
	)
	switch r.PathValue("action") {
	case "approve":
		got, err = h.ui.Requests.Approve(ctx, t.pr, d.ID, registry.ApproveInput{Note: note, AsOwner: r.PostForm.Get("as_owner") == "on"})
		done = "approved"
		if got.SelfApproved {
			done = "approved-as-owner"
		}
	case "reject":
		_, err = h.ui.Requests.Reject(ctx, t.pr, d.ID, note)
		done = "rejected"
	case "withdraw":
		_, err = h.ui.Requests.Withdraw(ctx, t.pr, d.ID, note)
		done = "withdrawn"
	default:
		h.notFound(w, r, t.user)
		return
	}
	page := "/t/" + t.ns() + "/deletions/" + d.ID
	switch {
	case errors.Is(err, registry.ErrProposalClosed):
		// A second click, or someone decided first: the page shows how it ended.
		back(w, r, page, "closed", nil)
		return
	case errors.Is(err, registry.ErrBackupFailed):
		h.log.ErrorContext(ctx, "web delete stopped: no backup", "deletion", d.ID, "package", d.Package.String(), "err", err)
		h.explain(w, r, t, http.StatusInternalServerError, "Nothing was deleted",
			"loadout could not save a copy of the database first, so it deleted nothing. Ask whoever runs the registry to check its backup folder, then try again.", page)
		return
	case err != nil:
		h.refused(w, r, t, err, page)
		return
	}
	h.log.InfoContext(ctx, "deletion decided on the web", "deletion", d.ID, "action", r.PathValue("action"), "user_id", t.user.ID)
	back(w, r, page, done, nil)
}

// requestRows are the requests of the namespace for the reviews list, as
// rows, and the people behind them.
func (h *handler) requestRows(r *http.Request, t team, all bool) ([]proposalRow, error) {
	st := registry.DeletionOpen
	if all {
		st = ""
	}
	ds, err := h.ui.Requests.Deletions(r.Context(), t.pr, t.ns(), st)
	if err != nil {
		return nil, err
	}
	names, err := h.deletionNames(r, ds...)
	if err != nil {
		return nil, err
	}
	rows := make([]proposalRow, 0, len(ds))
	for _, d := range ds {
		rows = append(rows, proposalRow{
			ID: d.ID, Package: d.Package.String(), Delete: true, Path: "/t/" + t.ns() + "/deletions/" + d.ID,
			Note: firstLine(d.Reason, 90), Author: who(d.Author, names), Created: d.CreatedAt, Status: deletionStatus(d),
		})
	}
	return rows, nil
}

// openRequests counts the open requests for the Reviews tab.
func (h *handler) openRequests(r *http.Request, t team) (int, error) {
	ds, err := h.ui.Requests.Deletions(r.Context(), t.pr, t.ns(), registry.DeletionOpen)
	return len(ds), err
}
