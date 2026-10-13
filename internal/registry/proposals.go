package registry

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/petervlad/loadout/internal/bundle"
	"github.com/petervlad/loadout/internal/pack"
)

// ProposalService implements the review rules: who may propose, approve,
// reject and withdraw, and when a candidate may become a release.
//
// It never runs an agent. Comparisons happen on a reviewer's own machine,
// in their own AI tool; the registry only stores what they attach.
type ProposalService struct {
	releases  Store
	proposals ProposalStore
	now       func() time.Time
	log       *slog.Logger
	// access narrows account principals; nil refuses them.
	access *Service
	// owners may approve their own proposals as owner; nil keeps four
	// eyes for everyone.
	owners Owners
}

// ProposalOption configures a ProposalService.
type ProposalOption func(*ProposalService)

// WithAccess lets account tokens act on proposals, narrowed by reg to the
// namespace of each proposal's package.
func WithAccess(reg *Service) ProposalOption { return func(s *ProposalService) { s.access = reg } }

// WithOwners lets owners approve their own proposals, o saying who owns a
// namespace. Without it (a registry without accounts) nobody approves
// their own proposal.
func WithOwners(o Owners) ProposalOption { return func(s *ProposalService) { s.owners = o } }

// NewProposalService returns a proposal service. releases is the same Store
// the registry Service publishes to. now is time.Now in production.
func NewProposalService(releases Store, proposals ProposalStore, now func() time.Time, log *slog.Logger, opts ...ProposalOption) *ProposalService {
	s := &ProposalService{releases: releases, proposals: proposals, now: now, log: log}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// in narrows an account principal to namespace ns (see Service.In); others
// are returned as they are.
func (s *ProposalService) in(ctx context.Context, pr Principal, ns string) (Principal, error) {
	return narrow(ctx, s.access, pr, ns)
}

// narrow is the narrowing both review services do: an account principal
// is narrowed by access to ns, and refused when there is no access.
func narrow(ctx context.Context, access *Service, pr Principal, ns string) (Principal, error) {
	if !pr.Account || pr.Team == ns {
		return pr, nil
	}
	if access == nil {
		return Principal{}, ErrUnauthorized
	}
	pr.Team = ""
	return access.In(ctx, pr, ns)
}

// ProposalInput is what an author sends with a candidate bundle.
type ProposalInput struct {
	Note    string
	Prompts []string
	// Base is the release the candidate was made from. Empty means the
	// latest release now, which is what every older CLI gets; a version
	// must be a release of the package.
	Base string
	// Replaces is the id of the caller's own open proposal of the same
	// package that this one replaces, after a rebase: it closes as
	// withdrawn in the same step, and its prompts come along.
	Replaces string
}

// Propose stores a candidate version with the prompts the author tested it
// with. Any token that reads the package's namespace may propose, so a team
// can suggest a change to a layer above it; only that layer's publishers
// decide. The version must be higher than the latest release.
func (s *ProposalService) Propose(ctx context.Context, pr Principal, data []byte, in ProposalInput) (Proposal, error) {
	p, err := bundle.Decode(data)
	if err != nil {
		return Proposal{}, fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	if pr, err = s.in(ctx, pr, p.ID().Team()); err != nil {
		return Proposal{}, err
	}
	if !pr.CanRead(p.ID().Team()) {
		return Proposal{}, fmt.Errorf("%w: propose to team %s", ErrForbidden, p.ID().Team())
	}
	if err := checkInput(in); err != nil {
		return Proposal{}, err
	}
	var old *Proposal
	if in.Replaces != "" {
		if old, err = s.replaceable(ctx, pr, p.ID(), in.Replaces); err != nil {
			return Proposal{}, err
		}
	}
	rels, err := s.checkAboveLatest(ctx, p)
	if err != nil {
		return Proposal{}, err
	}
	base, err := baseOf(p.ID(), rels, in.Base)
	if err != nil {
		return Proposal{}, err
	}
	id, err := newProposalID()
	if err != nil {
		return Proposal{}, fmt.Errorf("propose %s@%s: %w", p.ID(), p.Version(), err)
	}
	author, now := actorOf(pr), s.now().UTC()
	prompts, err := promptsFor(old, in.Prompts, author, now)
	if err != nil {
		return Proposal{}, err
	}
	prop := Proposal{
		ID:          id,
		Package:     p.ID(),
		Version:     p.Version(),
		Digest:      p.Digest(),
		Description: p.Manifest().Description,
		Kind:        p.Kind(),
		Size:        len(data),
		Author:      author,
		Note:        strings.TrimSpace(in.Note),
		Prompts:     prompts,
		Status:      ProposalOpen,
		CreatedAt:   now,
		Base:        base,
	}
	if old != nil {
		return s.replace(ctx, pr, *old, prop, data)
	}
	if err := s.proposals.CreateProposal(ctx, prop, data); err != nil {
		return Proposal{}, fmt.Errorf("propose %s@%s: %w", p.ID(), p.Version(), err)
	}
	s.log.Info("proposed", "proposal", id, "id", prop.Package, "version", prop.Version, "base", prop.Base.String(), "digest", prop.Digest.Short(), "token_id", pr.TokenID)
	return prop, nil
}

// checkAboveLatest returns the package's releases, lowest first, after
// checking that p's version is above all of them.
func (s *ProposalService) checkAboveLatest(ctx context.Context, p *pack.Pack) ([]Release, error) {
	rels, err := s.releases.Releases(ctx, p.ID())
	if err != nil && !errors.Is(err, ErrNotFound) {
		return nil, fmt.Errorf("propose %s: %w", p.ID(), err)
	}
	if n := len(rels); n > 0 {
		if latest := rels[n-1].Version; p.Version().Compare(latest) <= 0 {
			return nil, fmt.Errorf("%w: %s@%s must be higher than the latest release %s", ErrVersionConflict, p.ID(), p.Version(), latest)
		}
	}
	return rels, nil
}

// baseOf resolves what a candidate of id was made from: the release given,
// or the latest of rels (lowest first) when given is empty. A base older
// than the latest is accepted, since the author said so; the pages say it
// is behind from the start.
func baseOf(id pack.ID, rels []Release, given string) (Base, error) {
	if given == "" {
		if n := len(rels); n > 0 {
			return BaseAt(rels[n-1].Version), nil
		}
		return Base{}, nil
	}
	b, err := ParseBase(given)
	if err != nil {
		return Base{}, fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	v, _ := b.Version()
	for _, r := range rels {
		if r.Version.Compare(v) == 0 {
			return b, nil
		}
	}
	return Base{}, fmt.Errorf("%w: %s is not a release of %s; say which release your folder was made from, or leave the base out for the latest", ErrInvalid, v, id)
}

func checkInput(in ProposalInput) error {
	var errs []error
	if len(in.Note) > MaxNoteSize {
		errs = append(errs, fmt.Errorf("note is over %d bytes", MaxNoteSize))
	}
	if !utf8.ValidString(in.Note) {
		errs = append(errs, errors.New("note is not valid UTF-8"))
	}
	switch n := len(in.Prompts); {
	case n == 0 && in.Replaces == "":
		errs = append(errs, errors.New("a proposal needs at least one test prompt"))
	case n > MaxPrompts:
		errs = append(errs, fmt.Errorf("%d prompts, limit %d", n, MaxPrompts))
	}
	for i, p := range in.Prompts {
		if err := checkPrompt(p); err != nil {
			errs = append(errs, fmt.Errorf("prompt %d %w", i+1, err))
		}
	}
	if err := errors.Join(errs...); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	return nil
}

// checkPrompt checks one prompt's text; the error reads after "prompt N".
func checkPrompt(p string) error {
	switch {
	case strings.TrimSpace(p) == "":
		return errors.New("is empty")
	case len(p) > MaxPromptSize:
		return fmt.Errorf("is over %d bytes", MaxPromptSize)
	case !utf8.ValidString(p):
		return errors.New("is not valid UTF-8")
	}
	return nil
}

// newProposalID returns a random 12-character hex id, short enough to type.
func newProposalID() (string, error) {
	raw := make([]byte, 6)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", raw), nil
}

// Proposals lists proposals newest first: those of pkg's namespace, or of
// the principal's own namespace when pkg is zero. An empty status means any
// status.
func (s *ProposalService) Proposals(ctx context.Context, pr Principal, pkg pack.ID, status ProposalStatus) ([]Proposal, error) {
	if !pkg.IsZero() && pr.Account && !pr.CanRead(pkg.Team()) {
		var err error
		if pr, err = s.in(ctx, pr, pkg.Team()); err != nil {
			return nil, err
		}
	}
	if pkg.IsZero() && !pr.Narrowed() {
		return nil, errNoView
	}
	if !pkg.IsZero() && !pr.CanRead(pkg.Team()) {
		return nil, fmt.Errorf("%w: read team %s", ErrForbidden, pkg.Team())
	}
	if status != "" && !status.Valid() {
		return nil, fmt.Errorf("%w: status %q: want open, approved, rejected or withdrawn", ErrInvalid, status)
	}
	team := pr.Team
	if !pkg.IsZero() {
		team = pkg.Team()
	}
	props, err := s.proposals.Proposals(ctx, ProposalFilter{Team: team, Package: pkg, Status: status, Limit: maxListed})
	if err != nil {
		return nil, fmt.Errorf("proposals of %s: %w", team, err)
	}
	return props, nil
}

// Standings says where each of props stands against its package's latest
// release now, in the same order. A list from Proposals is one team's, so
// this reads that team's latest releases with one query; pr must be able
// to read every team in the list.
func (s *ProposalService) Standings(ctx context.Context, pr Principal, props []Proposal) ([]Standing, error) {
	latest := map[pack.ID]Base{}
	read := map[string]bool{}
	out := make([]Standing, len(props))
	for i, p := range props {
		if team := p.Package.Team(); !read[team] {
			if err := s.latestOf(ctx, pr, team, latest); err != nil {
				return nil, err
			}
			read[team] = true
		}
		out[i] = Standing{Base: p.Base, Latest: latest[p.Package]}
	}
	return out, nil
}

// latestOf adds the latest release of every package of team to into,
// after the read check Proposals makes.
func (s *ProposalService) latestOf(ctx context.Context, pr Principal, team string, into map[pack.ID]Base) error {
	if pr.Account && !pr.CanRead(team) {
		var err error
		if pr, err = s.in(ctx, pr, team); err != nil {
			return err
		}
	}
	if !pr.CanRead(team) {
		return fmt.Errorf("%w: read team %s", ErrForbidden, team)
	}
	rels, err := s.releases.Packages(ctx, team)
	if err != nil {
		return fmt.Errorf("latest releases of %s: %w", team, err)
	}
	for _, r := range rels {
		into[r.ID] = BaseAt(r.Version)
	}
	return nil
}

// Proposal returns one proposal. A proposal in a namespace the principal
// cannot read is reported as not found, so its id cannot be probed.
func (s *ProposalService) Proposal(ctx context.Context, pr Principal, id string) (Proposal, error) {
	p, _, err := s.proposalAs(ctx, pr, id)
	return p, err
}

// proposalAs is Proposal, also returning pr narrowed to the proposal's
// namespace, which is where every decision about it is made.
func (s *ProposalService) proposalAs(ctx context.Context, pr Principal, id string) (Proposal, Principal, error) {
	p, err := s.proposals.Proposal(ctx, id)
	if err != nil {
		return Proposal{}, Principal{}, fmt.Errorf("proposal %s: %w", id, err)
	}
	if pr.Account {
		narrowed, err := s.in(ctx, pr, p.Package.Team())
		if errors.Is(err, ErrForbidden) {
			return Proposal{}, Principal{}, fmt.Errorf("proposal %s: %w", id, ErrNotFound)
		}
		if err != nil {
			return Proposal{}, Principal{}, err
		}
		pr = narrowed
	}
	if !pr.CanRead(p.Package.Team()) {
		return Proposal{}, Principal{}, fmt.Errorf("proposal %s: %w", id, ErrNotFound)
	}
	return p, pr, nil
}

// ProposalBundle returns a proposal and its candidate bundle.
func (s *ProposalService) ProposalBundle(ctx context.Context, pr Principal, id string) (Proposal, []byte, error) {
	p, err := s.Proposal(ctx, pr, id)
	if err != nil {
		return Proposal{}, nil, err
	}
	data, err := s.proposals.ProposalBundle(ctx, id)
	if err != nil {
		return Proposal{}, nil, fmt.Errorf("proposal %s: %w", id, err)
	}
	return p, data, nil
}

// ApproveInput is what a reviewer sends with an approval.
type ApproveInput struct {
	Note string
	// AsOwner says the caller knows they approve their own proposal and
	// skip review. It is refused unless they own the namespace, and it
	// changes nothing on someone else's proposal: a second person
	// reviewed that one.
	AsOwner bool
	// Over is the latest release the approver accepts releasing over: the
	// proposal is behind it and approving drops its changes. It must name
	// the latest release exactly; it is ignored when the proposal is not
	// behind.
	Over string
}

// Approve turns an open proposal into a normal release, so teammates get it
// through sync. It needs a publish token of the team that is not the
// author's (four eyes), except for an owner approving their own proposal
// as owner with a note, which is recorded as released without review. The
// version must still be higher than the latest release: if someone
// published in between, it fails with ErrVersionConflict and the author
// proposes again. A proposal a newer release came out after (behind) is
// approved only over that release, named in in.Over, which is recorded;
// otherwise it fails with ErrBehind.
func (s *ProposalService) Approve(ctx context.Context, pr Principal, id string, in ApproveInput) (Proposal, Release, error) {
	p, pr, err := s.proposalAs(ctx, pr, id)
	if err != nil {
		return Proposal{}, Release{}, err
	}
	subj := p.subject()
	if err := checkDecider(pr, subj, "approve"); err != nil {
		return Proposal{}, Release{}, err
	}
	if p.Status != ProposalOpen {
		return Proposal{}, Release{}, fmt.Errorf("approve %s: %w: it is %s", id, ErrProposalClosed, p.Status)
	}
	st, err := s.standing(ctx, p)
	if err != nil {
		return Proposal{}, Release{}, err
	}
	if err := checkOver(p, st, in.Over); err != nil {
		return Proposal{}, Release{}, err
	}
	self := selfError(pr, subj, "approve")
	if self != nil {
		if err := checkOwnApproval(ctx, s.owners, pr, subj, in.AsOwner, in.Note, self); err != nil {
			return Proposal{}, Release{}, err
		}
	}
	if err := checkNote(in.Note); err != nil {
		return Proposal{}, Release{}, err
	}
	d := Decision{Status: ProposalApproved, By: actorOf(pr), At: s.now().UTC(), Note: strings.TrimSpace(in.Note), SelfApproved: self != nil, Latest: st.Latest}
	if st.Behind() {
		d.Over = st.Latest
	}
	approved, rel, err := s.proposals.ApproveProposal(ctx, id, d)
	if errors.Is(err, ErrStale) {
		return Proposal{}, Release{}, s.landedMeanwhile(ctx, p, in.Over, err)
	}
	p = approved
	if errors.Is(err, ErrVersionConflict) {
		return Proposal{}, Release{}, fmt.Errorf("approve %s: %w", id, refusal(err, "%s", rebaseHint(id)))
	}
	if err != nil {
		return Proposal{}, Release{}, fmt.Errorf("approve %s: %w", id, err)
	}
	if d.SelfApproved {
		s.log.Info("proposal approved by its author as owner", "proposal", id, "id", rel.ID, "version", rel.Version, "seq", rel.Seq, "token_id", pr.TokenID, "user_id", pr.UserID)
		return p, rel, nil
	}
	if !d.Over.IsZero() {
		s.log.Info("proposal approved over a newer release", "proposal", id, "base", p.Base.String(), "over", d.Over.String(), "token_id", pr.TokenID)
	}
	s.log.Info("proposal approved", "proposal", id, "id", rel.ID, "version", rel.Version, "seq", rel.Seq, "token_id", pr.TokenID, "author_token_id", p.Author.TokenID)
	return p, rel, nil
}

// Reject closes an open proposal. Like Approve it needs a publish token
// that is not the author's; authors withdraw instead.
func (s *ProposalService) Reject(ctx context.Context, pr Principal, id, note string) (Proposal, error) {
	p, pr, err := s.proposalAs(ctx, pr, id)
	if err != nil {
		return Proposal{}, err
	}
	if err := checkReviewer(pr, p.subject(), "reject"); err != nil {
		return Proposal{}, fmt.Errorf("%w (authors withdraw their own proposals)", err)
	}
	return s.decide(ctx, pr, id, ProposalRejected, note)
}

// Withdraw closes an open proposal. Only its author may withdraw it.
func (s *ProposalService) Withdraw(ctx context.Context, pr Principal, id, note string) (Proposal, error) {
	p, pr, err := s.proposalAs(ctx, pr, id)
	if err != nil {
		return Proposal{}, err
	}
	if !p.By(pr) {
		return Proposal{}, fmt.Errorf("%w: only the author can withdraw proposal %s", ErrForbidden, id)
	}
	return s.decide(ctx, pr, id, ProposalWithdrawn, note)
}

func (s *ProposalService) decide(ctx context.Context, pr Principal, id string, status ProposalStatus, note string) (Proposal, error) {
	if err := checkNote(note); err != nil {
		return Proposal{}, err
	}
	d := Decision{Status: status, By: actorOf(pr), At: s.now().UTC(), Note: strings.TrimSpace(note)}
	p, err := s.proposals.DecideProposal(ctx, id, d)
	if err != nil {
		return Proposal{}, fmt.Errorf("%s %s: %w", status, id, err)
	}
	s.log.Info("proposal closed", "proposal", id, "status", status, "token_id", pr.TokenID)
	return p, nil
}

// subject is what the four-eyes checks need to know about a proposal or a
// deletion request, so one code decides who may approve, reject and
// self-approve both.
type subject struct {
	id     string
	team   string
	author Actor
	// noun names it in a refusal: "proposal", "deletion request".
	noun string
	// released says what approving it does to the namespace, for the
	// refusal of an owner who gave no reason: "X is released without review".
	released string
}

func (p Proposal) subject() subject {
	return subject{
		id: p.ID, team: p.Package.Team(), author: p.Author, noun: "proposal",
		released: fmt.Sprintf("%s@%s is released", p.Package, p.Version),
	}
}

// checkReviewer enforces four eyes: pr may decide (checkDecider) and is not
// the author (selfError).
func checkReviewer(pr Principal, sub subject, verb string) error {
	if err := checkDecider(pr, sub, verb); err != nil {
		return err
	}
	return selfError(pr, sub, verb)
}

// checkDecider checks that pr may decide about the team's proposals and
// deletion requests: a publish token of the team (for a user, maintainer or
// above there). A machine token a user made never decides: approving is a
// person's call, and such a token would let its maker approve their own.
func checkDecider(pr Principal, sub subject, verb string) error {
	switch {
	case pr.Machine():
		return fmt.Errorf("%w: a machine token cannot %s %ss; a maintainer does, signed in with loadout login", ErrForbidden, verb, sub.noun)
	case !pr.CanPublish(sub.team):
		return fmt.Errorf("%w: %s needs a publish token of team %s (a maintainer's, for a user)", ErrForbidden, verb, sub.team)
	}
	return nil
}

// selfError is the four-eyes refusal when pr is the author (the author's
// token, or one of the author's user), else nil. Token and user ids
// decide; matching labels are refused too as a guard against one person
// holding two host-made tokens, but labels are chosen freely and are
// informational only, so an empty label never matches.
func selfError(pr Principal, sub subject, verb string) error {
	switch {
	case actorOf(pr).same(sub.author):
		return fmt.Errorf("%w: the author cannot %s their own %s; ask a teammate", ErrForbidden, verb, sub.noun)
	case sameLabel(pr.Label, sub.author.Label):
		return fmt.Errorf("%w: your token has the same label (%q) as the author's; ask a teammate", ErrForbidden, pr.Label)
	}
	return nil
}

// checkOwnApproval lets the author approve their own proposal or request
// only as an owner of its namespace (a person: a token without a user has
// nobody to call an owner), who says so with asOwner and says why in the
// note. self is the four-eyes refusal everyone else gets. Owns is asked
// only here, so ordinary approvals cost no extra query.
func checkOwnApproval(ctx context.Context, owners Owners, pr Principal, sub subject, asOwner bool, note string, self error) error {
	if owners == nil || pr.UserID == "" {
		return self
	}
	owns, err := owners.Owns(ctx, pr.UserID, sub.team)
	if err != nil {
		return fmt.Errorf("approve %s: ownership of %s: %w", sub.id, sub.team, err)
	}
	switch {
	case !owns:
		return self
	case !asOwner:
		return fmt.Errorf("%w; as an owner of %s you may approve it yourself: loadout review approve %s --as-owner --note WHY", self, sub.team, sub.id)
	case strings.TrimSpace(note) == "":
		return fmt.Errorf("%w: say why %s without review; a note is required when you approve your own %s", ErrInvalid, sub.released, sub.noun)
	}
	return nil
}

// actorOf is the actor behind pr. A machine token counts as the user who
// made it, so its maker cannot approve what it proposed.
func actorOf(pr Principal) Actor {
	user := pr.UserID
	if user == "" {
		user = pr.CreatedBy
	}
	return Actor{TokenID: pr.TokenID, Label: pr.Label, UserID: user}
}

func sameLabel(a, b string) bool {
	a, b = strings.TrimSpace(a), strings.TrimSpace(b)
	return a != "" && strings.EqualFold(a, b)
}

func checkNote(note string) error {
	if len(note) > MaxNoteSize || !utf8.ValidString(note) {
		return fmt.Errorf("%w: note must be valid UTF-8 of at most %d bytes", ErrInvalid, MaxNoteSize)
	}
	return nil
}

// AddReview attaches a review result, such as a comparison report, to an
// open proposal. Any token of the team may attach one. data is optional:
// the same result as JSON, which the web pages show side by side; nil
// means text only.
func (s *ProposalService) AddReview(ctx context.Context, pr Principal, id string, text, data []byte) (Review, error) {
	_, pr, err := s.proposalAs(ctx, pr, id)
	if err != nil {
		return Review{}, err
	}
	switch {
	case len(strings.TrimSpace(string(text))) == 0:
		return Review{}, fmt.Errorf("%w: review is empty", ErrInvalid)
	case len(text) > MaxReviewSize:
		return Review{}, fmt.Errorf("%w: review is over %d bytes", ErrInvalid, MaxReviewSize)
	case !utf8.Valid(text):
		return Review{}, fmt.Errorf("%w: review is not valid UTF-8", ErrInvalid)
	case len(data) > MaxReviewSize:
		return Review{}, fmt.Errorf("%w: review data is over %d bytes", ErrInvalid, MaxReviewSize)
	case data != nil && !json.Valid(data):
		return Review{}, fmt.Errorf("%w: review data is not valid JSON", ErrInvalid)
	}
	r := Review{Author: actorOf(pr), CreatedAt: s.now().UTC(), Size: len(text), DataSize: len(data)}
	r, err = s.proposals.AddReview(ctx, id, r, text, data, MaxReviews)
	if err != nil {
		return Review{}, fmt.Errorf("add review to %s: %w", id, err)
	}
	s.log.Info("review attached", "proposal", id, "review", r.Seq, "bytes", r.Size, "data_bytes", r.DataSize, "token_id", pr.TokenID)
	return r, nil
}

// ReviewText returns one attached review and its text.
func (s *ProposalService) ReviewText(ctx context.Context, pr Principal, id string, seq int) (Review, []byte, error) {
	if _, err := s.Proposal(ctx, pr, id); err != nil {
		return Review{}, nil, err
	}
	r, text, err := s.proposals.ReviewText(ctx, id, seq)
	if err != nil {
		return Review{}, nil, fmt.Errorf("review %d of %s: %w", seq, id, err)
	}
	return r, text, nil
}

// ReviewData returns one attached review and its structured data, or
// ErrNotFound when it was attached as text only.
func (s *ProposalService) ReviewData(ctx context.Context, pr Principal, id string, seq int) (Review, []byte, error) {
	if _, err := s.Proposal(ctx, pr, id); err != nil {
		return Review{}, nil, err
	}
	r, data, err := s.proposals.ReviewData(ctx, id, seq)
	if err != nil {
		return Review{}, nil, fmt.Errorf("review data %d of %s: %w", seq, id, err)
	}
	return r, data, nil
}
