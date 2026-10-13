package web

import (
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/petervlad/loadout/internal/pack"
	"github.com/petervlad/loadout/internal/registry"
	"github.com/petervlad/loadout/internal/review"
	"github.com/petervlad/loadout/internal/textdiff"
)

// proposalID accepts the ids the registry hands out; anything else is a
// 404 without a lookup.
var proposalID = regexp.MustCompile(`^[a-z0-9]{1,64}$`)

// status is how a proposal's status looks: a label and a pill color.
type status struct {
	Label string
	Tone  string
}

func statusOf(s registry.ProposalStatus) status {
	switch s {
	case registry.ProposalOpen:
		return status{"Waiting for review", "warn"}
	case registry.ProposalApproved:
		return status{"Released", "ok"}
	case registry.ProposalRejected:
		return status{"Rejected", "bad"}
	}
	return status{"Withdrawn", "plain"}
}

// proposalStatus is statusOf, marking a proposal its author approved as
// owner, so nobody mistakes it for a reviewed release, and one a newer
// proposal replaced.
func proposalStatus(p registry.Proposal) status {
	switch {
	case p.Status == registry.ProposalApproved && p.SelfApproved:
		return status{"Released without review", "warn"}
	case p.Status == registry.ProposalWithdrawn && p.ReplacedBy != "":
		return status{"Replaced", "plain"}
	}
	return statusOf(p.Status)
}

// reviewsView is the body of reviews.html.
type reviewsView struct {
	All  bool
	Rows []proposalRow
	// Review is the team's "review before release" setting; CanReview
	// links to where it is changed.
	Review    bool
	CanReview bool
}

// proposalRow is one proposal in the list.
type proposalRow struct {
	ID      string
	Package string
	From    string
	To      string
	// Behind is the latest release when the proposal is open and that
	// release came out after its base, else "".
	Behind  string
	Note    string
	Author  string
	Created time.Time
	Status  status
	Reports int
	// Delete marks a request to delete a package; Path is its page. A
	// proposal's page is /t/{ns}/reviews/{ID}.
	Delete bool
	Path   string
}

func (h *handler) reviewsPage(w http.ResponseWriter, r *http.Request, t team) {
	if !t.member() {
		h.notFound(w, r, t.user)
		return
	}
	all := r.URL.Query().Get("status") == "all"
	st := registry.ProposalOpen
	if all {
		st = ""
	}
	props, err := h.ui.Proposals.Proposals(r.Context(), t.pr, pack.ID{}, st)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	names, err := h.names(r, props)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	// One read of the team's latest releases for the whole list.
	standings, err := h.ui.Proposals.Standings(r.Context(), t.pr, props)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	v := reviewsView{All: all, Review: t.place.Namespace.RequireReview, CanReview: t.place.Access.CanSetReview()}
	for i, p := range props {
		v.Rows = append(v.Rows, proposalRow{
			ID:      p.ID,
			Package: p.Package.String(),
			From:    p.Base.String(),
			To:      p.Version.String(),
			Behind:  behindOf(p, standings[i]),
			Note:    firstLine(p.Note, 90),
			Author:  who(p.Author, names),
			Created: p.CreatedAt,
			Status:  proposalStatus(p),
			Reports: len(p.Reviews),
			Path:    "/t/" + t.ns() + "/reviews/" + p.ID,
		})
	}
	if h.ui.Requests != nil {
		dels, err := h.requestRows(r, t, all)
		if err != nil {
			h.fail(w, r, err)
			return
		}
		v.Rows = append(v.Rows, dels...)
		// Newest first, requests and proposals together.
		slices.SortStableFunc(v.Rows, func(a, b proposalRow) int { return b.Created.Compare(a.Created) })
	}
	h.renderTeam(w, r, http.StatusOK, "reviews.html", "reviews", t, uiView{Title: "Reviews · " + t.ns(), Wide: true, Page: v})
}

// behindOf is the release an open proposal is behind, "" when it is not
// behind or is closed: a closed proposal's standing says nothing anyone
// can act on, and an approved one is behind its own release.
func behindOf(p registry.Proposal, st registry.Standing) string {
	if p.Status != registry.ProposalOpen || !st.Behind() {
		return ""
	}
	return st.Latest.String()
}

// names returns display names of the users behind proposals, their
// reviews and their prompts, and of the users with the extra ids.
func (h *handler) names(r *http.Request, props []registry.Proposal, extra ...string) (map[string]string, error) {
	ids := extra
	for _, p := range props {
		ids = append(ids, p.Author.UserID, p.DecidedBy.UserID)
		for _, rv := range p.Reviews {
			ids = append(ids, rv.Author.UserID)
		}
		for _, pr := range p.Prompts {
			ids = append(ids, pr.Author.UserID)
		}
		for _, pr := range p.RemovedPrompts {
			ids = append(ids, pr.Author.UserID, pr.RemovedBy.UserID)
		}
	}
	return h.ui.Teams.DisplayNames(r.Context(), ids)
}

// who names the person or token behind an action: the user's name, else
// the token's label.
func who(a registry.Actor, names map[string]string) string {
	if n, ok := names[a.UserID]; ok {
		return n
	}
	if a.Label != "" {
		return a.Label
	}
	return "a token"
}

// proposalView is the body of proposal.html.
type proposalView struct {
	ID      string
	Package string
	From    string
	To      string
	Status  status
	Open    bool
	Author  string
	Created time.Time
	Note    string
	// Replaces and ReplacedBy link a proposal and the one its author
	// replaced it with after a rebase; see replacement.
	Replaces   *proposalLink
	ReplacedBy *proposalLink
	// Behind is the notice of an open proposal a newer release came out
	// after, nil otherwise.
	Behind  *behindView
	Prompts promptsView
	// Review is the command that compares this proposal with the version
	// the reviewer has installed.
	Review   string
	Decision decision
	// Report is the comparison shown, if any report was attached.
	Report *reportView
	// Markdown is the text of a report attached without data.
	Markdown string
	// What names the package's kind in a sentence.
	What string
	// Diff is the start of the change to the package's files, without the
	// manifest.
	Diff     []diffFile
	DiffMore bool
	Activity []event
	// ran holds the prompt texts of the shown report, nil without one.
	ran map[string]bool
}

// reportLink is the query of a link to report seq that keeps the prompt,
// run and whole-answer view of the report shown now.
func (v *proposalView) reportLink(seq int) string {
	if v.Report == nil {
		return fmt.Sprintf("?report=%d", seq)
	}
	other := *v.Report
	other.Seq = seq
	return other.Query(v.Report.Full)
}

// decision says what the viewer may decide and why.
type decision struct {
	CanApprove  bool
	CanWithdraw bool
	// CanReject offers Reject alone, with Approve off: a release at or
	// above the proposed version came out after it.
	CanReject bool
	// AsOwner offers the author, an owner of the namespace, to approve
	// their own proposal without review; Uncompared warns that no
	// comparison is attached.
	AsOwner    bool
	Uncompared bool
	// Over is the latest release when the proposal is behind it: Approve
	// asks first, and the form sends it as over.
	Over string
	// Why explains the buttons: why they are on, or why they are off.
	Why string
}

// reportView is one comparison report, at one prompt and run.
type reportView struct {
	Seq     int
	By      string
	At      time.Time
	Tool    string
	Model   string
	Runs    int
	Prompts []string
	Prompt  int
	Run     int
	Text    string
	// Long is true when the picker shows only part of Text, so the page
	// shows it in full.
	Long bool
	// Current is nil when there was no release to compare with.
	Current   *answerView
	Candidate answerView
	CurVer    string
	CandVer   string
	// CurLabel and CandLabel say where each side came from: "Installed",
	// "Release" or "Proposal"; reports made before sources were recorded
	// keep "Current" and "Candidate".
	CurLabel  string
	CandLabel string
	// Mode is "Full access" or "Safe mode", empty in older reports.
	Mode string
	// Context lists the team packages that sat beside each side; Notes
	// are what the tool could not do, such as Copilot in safe mode.
	Context []contextItem
	Notes   []string
	// Reports lists every attached report, for switching between them.
	Reports []int
	// Full is true when the page shows whole answers (?full=1), and
	// AnyCut when at least one answer is long enough to be cut, so the
	// page offers to switch.
	Full   bool
	AnyCut bool
	// Preview is how many words a cut answer shows.
	Preview int
}

// Query is the query of a link to this report, prompt and run, whole or
// cut. Every link between views of the proposal page is built here, so
// a new view parameter is carried everywhere at once.
func (v *reportView) Query(full bool) string {
	q := fmt.Sprintf("?report=%d&prompt=%d&run=%d", v.Seq, v.Prompt, v.Run)
	if full {
		q += "&full=1"
	}
	return q
}

// answerView is one answer with its time, tokens and words. HTML is the
// answer's Markdown rendered safely (see markdown): its first words when
// it is cut and the page is not showing whole answers, else all of it.
type answerView struct {
	Text  string
	HTML  template.HTML
	Err   string
	Stats string
	Words int
	// Cut is true when the answer is long enough to be cut; Left is how
	// many words the page leaves out, 0 when it shows the whole answer.
	Cut  bool
	Left int
}

// event is one line of the activity, newest first. Quote is a short,
// untrusted quote after What; Href links What to what it is about, and
// Where follows it unlinked.
type event struct {
	Who   string
	What  string
	Where string
	Quote string
	At    time.Time
	Tone  string
	Seq   int
	Href  string
}

func (h *handler) proposalPage(w http.ResponseWriter, r *http.Request, t team) {
	p, ok := h.proposal(w, r, t)
	if !ok {
		return
	}
	st, err := h.standing(r, t, p)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	sides, err := h.sides(r, t, p, st)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	var since []registry.Release
	if sides.latest != nil {
		if since, err = h.releasesSince(r, t, p); err != nil {
			h.fail(w, r, err)
			return
		}
	}
	extra := make([]string, 0, len(since))
	for _, rel := range since {
		extra = append(extra, rel.PublishedBy.UserID)
	}
	names, err := h.names(r, []registry.Proposal{p}, extra...)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	cmd, err := h.reviewCommand(r, t, p)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	v := proposalView{
		ID:       p.ID,
		Package:  p.Package.String(),
		From:     p.Base.String(),
		To:       p.Version.String(),
		Status:   proposalStatus(p),
		Open:     p.Status == registry.ProposalOpen,
		Author:   who(p.Author, names),
		Created:  p.CreatedAt,
		Note:     p.Note,
		Review:   cmd,
		Decision: decide(t, p, st),
		Activity: activity(p, names, "/t/"+t.ns()+"/reviews"),
		What:     p.Kind.Describe(),
	}
	if sides.latest != nil {
		v.Behind = behindNote(t, p, st, sides, since, names)
	}
	if err := h.replacement(r, t, p, &v); err != nil {
		h.fail(w, r, err)
		return
	}
	files, err := diffPacks(sides.base, sides.prop, 0)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	v.Diff, v.DiffMore = preview(files)
	if err := h.report(r, t, p, names, &v); err != nil {
		h.fail(w, r, err)
		return
	}
	v.Prompts = promptsOf(t, p, names, v.ran, r.URL.Query().Get("done"))
	for i, e := range v.Activity {
		if e.Seq > 0 {
			v.Activity[i].Href = v.reportLink(e.Seq)
		}
	}
	h.renderTeam(w, r, http.StatusOK, "proposal.html", "reviews", t, uiView{
		Bare:   true,
		Title:  p.Package.String() + " " + p.Version.String(),
		Notice: decisionNotice(r.URL.Query().Get("done")),
		Page:   v,
	})
}

// reviewCommand is the command that reviews p: the package alone when p is
// the newest open proposal of its package, which `loadout review` picks by
// itself, else with --to.
func (h *handler) reviewCommand(r *http.Request, t team, p registry.Proposal) (string, error) {
	cmd := "loadout review " + p.Package.String()
	if p.Status != registry.ProposalOpen {
		return cmd, nil
	}
	open, err := h.ui.Proposals.Proposals(r.Context(), t.pr, p.Package, registry.ProposalOpen)
	if err != nil {
		return "", err
	}
	if len(open) > 0 && open[0].ID != p.ID {
		cmd += " --to " + p.ID
	}
	return cmd, nil
}

// promptPicker is how much of a prompt its picker shows.
const promptPicker = 70

// answerPreview is how many words of each answer the proposal page shows
// until the reader asks for the whole answers.
const answerPreview = 150

// maxDiffPreview is how many diff lines the proposal page shows beside the
// answers; the diff page shows all of them.
const maxDiffPreview = 14

// preview is the start of a diff shown beside the answers: the changed
// lines of every file but the manifest (where the version always changes),
// at most maxDiffPreview of them. more is true when it leaves lines out.
func preview(files []diffFile) (out []diffFile, more bool) {
	n := 0
	for _, f := range files {
		if f.Name == pack.ManifestFile {
			continue
		}
		if n == maxDiffPreview {
			return out, true
		}
		p := diffFile{Name: f.Name, Change: f.Change}
		for _, l := range f.Lines {
			if l.Kind == "hunk" {
				continue
			}
			if n == maxDiffPreview {
				more = true
				break
			}
			p.Lines = append(p.Lines, l)
			n++
		}
		out = append(out, p)
		if more {
			return out, true
		}
	}
	return out, false
}

// proposal loads the proposal of the page, answering 404 itself for an id
// that is not one of this team's proposals.
func (h *handler) proposal(w http.ResponseWriter, r *http.Request, t team) (registry.Proposal, bool) {
	id := r.PathValue("id")
	if !t.member() || !proposalID.MatchString(id) {
		h.notFound(w, r, t.user)
		return registry.Proposal{}, false
	}
	p, err := h.ui.Proposals.Proposal(r.Context(), t.pr, id)
	if errors.Is(err, registry.ErrNotFound) || (err == nil && p.Package.Team() != t.ns()) {
		h.notFound(w, r, t.user)
		return registry.Proposal{}, false
	}
	if err != nil {
		h.fail(w, r, err)
		return registry.Proposal{}, false
	}
	return p, true
}

// decide works out which buttons the viewer gets. It mirrors the rules of
// the proposal service, which checks them again on every POST.
func decide(t team, p registry.Proposal, st registry.Standing) decision {
	author := t.is(p.Author)
	// The notice above says why at length; this names the reason the
	// disabled button is tied to.
	blocked := "Approve is off: " + st.Latest.String() + " is already released."
	// Behind, approving names the release it goes over (the service
	// checks it again, inside the approval).
	over := ""
	if st.Behind() {
		over = st.Latest.String()
	}
	switch {
	case p.Status != registry.ProposalOpen:
		return decision{}
	case st.Blocks(p.Version) && author:
		return decision{CanWithdraw: true, Why: blocked}
	case st.Blocks(p.Version) && t.place.Access.CanApprove():
		return decision{CanReject: true, Why: blocked}
	case st.Blocks(p.Version):
		return decision{Why: blocked}
	case author && t.place.Owns:
		return decision{
			CanWithdraw: true, AsOwner: true, Uncompared: len(p.Reviews) == 0, Over: over,
			Why: "You proposed this version. As an owner of " + t.ns() + " you may release it yourself; teammates will see that review was skipped.",
		}
	case author:
		return decision{CanWithdraw: true, Why: "You proposed this version, so a teammate who maintains " + t.ns() + " approves it."}
	case !t.place.Access.CanApprove():
		return decision{Why: "Only maintainers of " + t.ns() + " approve or reject. You can still run the comparison and attach it."}
	}
	why := "You can approve: you publish to " + t.ns() + " and you are not the author. Approving releases " + p.Version.String() + " to everyone on their next sync."
	if over != "" {
		why = "You can approve: you publish to " + t.ns() + " and you are not the author. Approving releases " + p.Version.String() + " without the changes in " + over + ", so it asks first."
	}
	return decision{CanApprove: true, CanReject: true, Over: over, Why: why}
}

// activity lists what happened to a proposal, newest first. reviews is
// the path of the team's reviews, for links to a replaced or replacing
// proposal.
func activity(p registry.Proposal, names map[string]string, reviews string) []event {
	proposed, out := promptEvents(p, names)
	what := "proposed " + p.Version.String()
	if proposed > 0 {
		what += " with " + plural(proposed, "prompt")
	}
	if p.Replaces != "" {
		what += ", replacing " + p.Replaces
	}
	out = append(out, event{Who: who(p.Author, names), What: what, At: p.CreatedAt, Tone: "plain"})
	for _, rv := range p.Reviews {
		out = append(out, event{Who: who(rv.Author, names), What: "attached a comparison", At: rv.CreatedAt, Tone: "accent", Seq: rv.Seq})
	}
	if !p.DecidedAt.IsZero() {
		what := map[registry.ProposalStatus]string{
			registry.ProposalApproved:  "approved and released it",
			registry.ProposalRejected:  "rejected it",
			registry.ProposalWithdrawn: "withdrew it",
		}[p.Status]
		if p.SelfApproved {
			what = "approved own proposal as owner, without review"
		}
		tone := proposalStatus(p).Tone
		// Approved over a newer release: released without its changes.
		if !p.ApprovedOver.IsZero() {
			if p.SelfApproved {
				what += ", over " + p.ApprovedOver.String()
			} else {
				what = "approved and released it over " + p.ApprovedOver.String()
			}
			tone = "warn"
		}
		href := ""
		switch {
		case p.ReplacedBy != "":
			// The note only repeats the id ("replaced by ID").
			what, href = "replaced it with "+p.ReplacedBy, reviews+"/"+p.ReplacedBy
		case p.DecisionNote != "":
			what += ": “" + p.DecisionNote + "”"
		}
		out = append(out, event{Who: who(p.DecidedBy, names), What: what, At: p.DecidedAt, Tone: tone, Href: href})
	}
	slices.SortStableFunc(out, func(a, b event) int { return b.At.Compare(a.At) })
	return out
}

// decisionNotice is the line shown after a decision.
func decisionNotice(done string) string {
	switch done {
	case "approved":
		return "Approved. Everyone gets the new version on their next sync."
	case "approved-as-owner":
		return "Released without review. Everyone gets the new version on their next sync, and the activity says you approved it as owner."
	case "rejected":
		return "Rejected. The author can propose a new version."
	case "withdrawn":
		return "Withdrawn."
	}
	return ""
}

// report fills in the comparison report the page shows: the one picked
// with ?report=, else the newest with data, else the newest.
func (h *handler) report(r *http.Request, t team, p registry.Proposal, names map[string]string, v *proposalView) error {
	if len(p.Reviews) == 0 {
		return nil
	}
	q := r.URL.Query()
	pick := slices.IndexFunc(p.Reviews, func(rv registry.Review) bool { return strconv.Itoa(rv.Seq) == q.Get("report") })
	if pick < 0 {
		pick = len(p.Reviews) - 1
		for i, rv := range slices.Backward(p.Reviews) {
			if rv.DataSize > 0 {
				pick = i
				break
			}
		}
	}
	rv := p.Reviews[pick]
	if rv.DataSize > 0 {
		_, data, err := h.ui.Proposals.ReviewData(r.Context(), t.pr, p.ID, rv.Seq)
		if err != nil && !errors.Is(err, registry.ErrNotFound) {
			return err
		}
		var rep review.Report
		if err == nil && json.Unmarshal(data, &rep) == nil && len(rep.Prompts) > 0 {
			v.Report = reportOf(rep, rv, who(rv.Author, names), atoi(q.Get("prompt")), atoi(q.Get("run")), q.Get("full") == "1")
			for _, other := range p.Reviews {
				v.Report.Reports = append(v.Report.Reports, other.Seq)
			}
			v.ran = map[string]bool{}
			for _, pr := range rep.Prompts {
				v.ran[strings.TrimSpace(pr.Prompt)] = true
			}
			return nil
		}
		// Data that does not decode falls back to the text below.
	}
	_, text, err := h.ui.Proposals.ReviewText(r.Context(), t.pr, p.ID, rv.Seq)
	if err != nil {
		return err
	}
	v.Markdown = string(text)
	return nil
}

func atoi(s string) int {
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0
	}
	return n
}

// reportOf picks one prompt and run of a report, both counted from 1 and
// clamped to what the report has. full shows whole answers rather than
// their first answerPreview words.
func reportOf(rep review.Report, rv registry.Review, by string, prompt, run int, full bool) *reportView {
	prompt = min(max(prompt, 1), len(rep.Prompts))
	pr := rep.Prompts[prompt-1]
	v := &reportView{
		Seq:       rv.Seq,
		By:        by,
		At:        rv.CreatedAt,
		Tool:      toolName(rep.Tool),
		Model:     cmp.Or(rep.Model, "default model"),
		Runs:      rep.Runs,
		Prompt:    prompt,
		Text:      pr.Prompt,
		CandVer:   rep.Candidate.Version,
		Full:      full,
		Preview:   answerPreview,
		CurLabel:  sideLabel(rep.Current, "Current"),
		CandLabel: sideLabel(&rep.Candidate, "Candidate"),
		Mode:      modeName(rep.Mode),
		Context:   contextOf(rep.Context),
		Notes:     rep.Notes,
	}
	for _, p := range rep.Prompts {
		v.Prompts = append(v.Prompts, firstLine(p.Prompt, promptPicker))
	}
	v.Long = firstLine(pr.Prompt, promptPicker) != strings.TrimSpace(pr.Prompt)
	if len(pr.Runs) == 0 {
		v.Candidate = answerView{Err: "This prompt has no runs in the report."}
		return v
	}
	v.Runs = len(pr.Runs)
	v.Run = min(max(run, 1), len(pr.Runs))
	pair := pr.Runs[v.Run-1]
	v.Candidate = answerOf(pair.Candidate, full)
	v.AnyCut = v.Candidate.Cut
	if pair.Current != nil {
		a := answerOf(*pair.Current, full)
		v.Current = &a
		v.AnyCut = v.AnyCut || a.Cut
		if rep.Current != nil {
			v.CurVer = rep.Current.Version
		}
	}
	return v
}

// answerOf renders one outcome: whole when full, else cut to its first
// answerPreview words if it is long.
func answerOf(o review.Outcome, full bool) answerView {
	if o.Err != "" {
		return answerView{Err: o.Err, Stats: "failed"}
	}
	a := renderAnswer(o.Answer.Text, answerPreview)
	stats := fmt.Sprintf("%.1f s", o.Answer.Duration.Seconds())
	if o.Answer.InputTokens > 0 || o.Answer.OutputTokens > 0 {
		stats += " · " + tokens(o.Answer.InputTokens) + " in / " + tokens(o.Answer.OutputTokens) + " out"
	}
	stats += " · " + plural(a.Words, "word")
	v := answerView{Text: o.Answer.Text, HTML: a.Full, Stats: stats, Words: a.Words, Cut: a.Cut}
	if a.Cut && !full {
		v.HTML, v.Left = a.Short, a.Left
	}
	return v
}

// contextItem is one team package that sat beside both sides of a run.
type contextItem struct {
	ID, Version, Kind string
}

func contextOf(in []review.ContextPackage) []contextItem {
	out := make([]contextItem, 0, len(in))
	for _, c := range in {
		out = append(out, contextItem{ID: c.ID, Version: c.Version, Kind: kindName(pack.Kind(c.Kind))})
	}
	return out
}

// sideLabel names where a side of a report came from; reports made before
// sources were recorded keep the old word.
func sideLabel(v *review.Version, old string) string {
	if v == nil {
		return old
	}
	switch v.Source {
	case review.SourceInstalled:
		return "Installed"
	case review.SourceRelease:
		return "Release"
	case review.SourceProposal:
		return "Proposal"
	}
	return old
}

// modeName is the access a report's runs had, as the page says it.
func modeName(m review.Mode) string {
	switch m {
	case review.ModeFull:
		return "Full access"
	case review.ModeSafe:
		return "Safe mode"
	}
	return ""
}

// tokens writes a token count the short way: 950, 3.9k, 1.2M.
func tokens(n int) string {
	switch {
	case n < 1000:
		return strconv.Itoa(n)
	case n < 1_000_000:
		return strconv.FormatFloat(float64(n)/1000, 'f', 1, 64) + "k"
	}
	return strconv.FormatFloat(float64(n)/1_000_000, 'f', 1, 64) + "M"
}

// toolName is the AI tool a report was made with, as people call it.
func toolName(tool string) string {
	switch tool {
	case "claude":
		return "Claude Code"
	case "copilot":
		return "Copilot CLI"
	}
	return tool
}

// diffFile is the diff of one file of a package. Change is "added" or
// "removed" for a file only one side has, when there is a release to
// compare with.
type diffFile struct {
	Name   string
	Change string
	Lines  []diffLine
}

// diffLine is one line of a unified diff: Kind is "add", "del", "hunk" or
// "same", Mark the sign shown before an added or removed line, and Text
// the line without the diff's own marker.
type diffLine struct {
	Kind string
	Mark string
	Text string
}

// diffPacks compares two versions of a package, with context lines
// around each change; from is nil before the first release. Unchanged
// files are left out. The files come in to's reading order (see
// diffOrder).
func diffPacks(from, to *pack.Pack, context int) ([]diffFile, error) {
	base := map[string][]byte{}
	if from != nil {
		base = from.Files()
	}
	files := to.Files()
	var out []diffFile
	for _, name := range diffOrder(to, base) {
		old, inBase := base[name]
		cur, inTo := files[name]
		text, err := textdiff.Unified(name, name, string(old), string(cur), context)
		if err != nil {
			return nil, err
		}
		if text == "" {
			continue
		}
		f := diffFile{Name: name}
		switch {
		case from == nil:
		case !inBase:
			f.Change = "added"
		case !inTo:
			f.Change = "removed"
		}
		f.Lines = diffLines(text)
		out = append(out, f)
	}
	return out, nil
}

// diffLines splits a unified diff from textdiff into lines for the
// diff-file template, leaving out its --- and +++ header.
func diffLines(text string) []diffLine {
	var out []diffLine
	for i, l := range strings.Split(strings.TrimSuffix(text, "\n"), "\n") {
		if i < 2 {
			continue
		}
		line := diffLine{Kind: "hunk", Text: l}
		switch {
		case strings.HasPrefix(l, "+"):
			line = diffLine{Kind: "add", Mark: "+", Text: l[1:]}
		case strings.HasPrefix(l, "-"):
			line = diffLine{Kind: "del", Mark: "−", Text: l[1:]}
		case strings.HasPrefix(l, " "):
			line = diffLine{Kind: "same", Mark: " ", Text: l[1:]}
		}
		out = append(out, line)
	}
	return out
}

// diffOrder lists the paths of both versions: the candidate's reading
// order (main file first) with removed files sorted in among the rest, and
// the manifest last, since only its version usually changes.
func diffOrder(cand *pack.Pack, base map[string][]byte) []string {
	paths := cand.Paths()[1:]
	for name := range base {
		if name != pack.ManifestFile && !slices.Contains(paths, name) {
			paths = append(paths, name)
		}
	}
	rest := paths
	if main := cand.Kind().MainFile(); main != "" {
		rest = paths[1:]
	}
	slices.Sort(rest)
	return append(paths, pack.ManifestFile)
}

// diffView is the body of diff.html.
type diffView struct {
	ID      string
	Package string
	To      string
	// From and Until are the versions compared in the view shown; From is
	// "" before the first release.
	From  string
	Until string
	View  string
	Files []diffFile
	// Tabs switch between the views; only a proposal that is behind has
	// more than one.
	Tabs []diffTab
}

// diffTab is one view of the diff page.
type diffTab struct {
	Key     string
	Label   string
	Current bool
}

// diffKind is one way to compare a proposal: its query value and tab.
type diffKind struct{ key, label string }

// diffViews are the diff page's views of a proposal that is behind: the
// first is the default and the only one when it is not.
var diffViews = []diffKind{
	{"changes", "This proposal"}, // base to proposal: what the author changed
	{"since", "Released since"},  // base to latest: what the proposal does not have
	{"effect", "After approval"}, // latest to proposal: what everyone gets on sync
}

func (h *handler) diffPage(w http.ResponseWriter, r *http.Request, t team) {
	p, ok := h.proposal(w, r, t)
	if !ok {
		return
	}
	st, err := h.standing(r, t, p)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	sides, err := h.sides(r, t, p, st)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	v := diffView{ID: p.ID, Package: p.Package.String(), To: p.Version.String(), View: diffViews[0].key}
	if sides.latest != nil {
		if q := r.URL.Query().Get("view"); slices.ContainsFunc(diffViews, func(d diffKind) bool { return d.key == q }) {
			v.View = q
		}
		for _, d := range diffViews {
			v.Tabs = append(v.Tabs, diffTab{Key: d.key, Label: d.label, Current: d.key == v.View})
		}
	}
	from, to := sides.base, sides.prop
	v.From, v.Until = p.Base.String(), p.Version.String()
	switch v.View {
	case "since":
		to, v.Until = sides.latest, st.Latest.String()
	case "effect":
		from, v.From = sides.latest, st.Latest.String()
	}
	if v.Files, err = diffPacks(from, to, 3); err != nil {
		h.fail(w, r, err)
		return
	}
	h.renderTeam(w, r, http.StatusOK, "diff.html", "reviews", t, uiView{Bare: true, Title: "Changes in " + p.Package.String() + " " + p.Version.String(), Page: v})
}

func (h *handler) decideProposal(w http.ResponseWriter, r *http.Request, t team) {
	p, ok := h.proposal(w, r, t)
	if !ok {
		return
	}
	note := r.PostForm.Get("note")
	var err error
	done := ""
	switch r.PathValue("action") {
	case "approve":
		// The service checks as_owner and over again; the page only offers
		// them. Without over, a behind proposal is refused with ErrBehind.
		var got registry.Proposal
		got, _, err = h.ui.Proposals.Approve(r.Context(), t.pr, p.ID, registry.ApproveInput{
			Note: note, AsOwner: r.PostForm.Get("as_owner") == "on", Over: r.PostForm.Get("over"),
		})
		done = "approved"
		if got.SelfApproved {
			done = "approved-as-owner"
		}
	case "reject":
		_, err = h.ui.Proposals.Reject(r.Context(), t.pr, p.ID, note)
		done = "rejected"
	case "withdraw":
		_, err = h.ui.Proposals.Withdraw(r.Context(), t.pr, p.ID, note)
		done = "withdrawn"
	default:
		h.notFound(w, r, t.user)
		return
	}
	page := "/t/" + t.ns() + "/reviews/" + p.ID
	if err != nil {
		h.refused(w, r, t, err, page)
		return
	}
	h.log.InfoContext(r.Context(), "proposal decided on the web", "proposal", p.ID, "action", r.PathValue("action"), "user_id", t.user.ID)
	back(w, r, page, done, nil)
}
