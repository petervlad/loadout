package registry

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/petervlad/loadout/internal/pack"
)

// ErrBackupFailed means the copy of the database that comes before a delete
// on the web could not be taken, so nothing was deleted.
var ErrBackupFailed = errors.New("the backup before the delete failed")

// Snapshots keeps a copy of the database before a change nothing undoes.
type Snapshots interface {
	// Take writes a consistent copy named after label and returns its
	// file name.
	Take(ctx context.Context, label string) (string, error)
}

// DeletionService implements the review of requests to delete a package:
// who may ask, approve, reject and withdraw. It follows the rules of
// ProposalService, with the same four-eyes checks, and never runs an agent.
type DeletionService struct {
	releases  Store
	deletions DeletionStore
	now       func() time.Time
	log       *slog.Logger
	// access narrows account principals; nil refuses them.
	access *Service
	// owners may approve their own requests as owner; nil keeps four eyes
	// for everyone.
	owners Owners
	// snapshots takes the copy before an approval deletes; nil refuses
	// approvals, since a delete that cannot be backed up is not done.
	snapshots Snapshots
	// mu makes approvals run one after the other, so two clicks take one
	// copy and delete once.
	mu sync.Mutex
}

// DeletionOption configures a DeletionService.
type DeletionOption func(*DeletionService)

// WithDeletionAccess lets account tokens act on requests, narrowed by reg to
// the namespace of each request's package.
func WithDeletionAccess(reg *Service) DeletionOption {
	return func(s *DeletionService) { s.access = reg }
}

// WithDeletionOwners lets owners approve their own requests as owner, o
// saying who owns a namespace.
func WithDeletionOwners(o Owners) DeletionOption { return func(s *DeletionService) { s.owners = o } }

// WithDeletionSnapshots sets where the copy before an approval comes from.
func WithDeletionSnapshots(sn Snapshots) DeletionOption {
	return func(s *DeletionService) { s.snapshots = sn }
}

// NewDeletionService returns a deletion service. releases is the same Store
// the registry Service publishes to. now is time.Now in production.
func NewDeletionService(releases Store, deletions DeletionStore, now func() time.Time, log *slog.Logger, opts ...DeletionOption) *DeletionService {
	s := &DeletionService{releases: releases, deletions: deletions, now: now, log: log}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

func (s *DeletionService) in(ctx context.Context, pr Principal, ns string) (Principal, error) {
	return narrow(ctx, s.access, pr, ns)
}

func (d Deletion) subject() subject {
	return subject{
		id: d.ID, team: d.Package.Team(), author: d.Author, noun: "deletion request",
		released: d.Package.String() + " is deleted",
	}
}

// checkReason checks the reason for a deletion: required, since the record
// of a delete must say why.
func checkReason(reason string) error {
	switch {
	case strings.TrimSpace(reason) == "":
		return fmt.Errorf("%w: say why the package should be deleted", ErrInvalid)
	case len(reason) > MaxNoteSize || !utf8.ValidString(reason):
		return fmt.Errorf("%w: the reason must be valid UTF-8 of at most %d bytes", ErrInvalid, MaxNoteSize)
	}
	return nil
}

// newDeletionID returns an id like del-3f9a1c2b7d4e: random, short enough to
// type, and told apart from a proposal's by its prefix.
func newDeletionID() (string, error) {
	raw := make([]byte, 6)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return fmt.Sprintf("del-%x", raw), nil
}

// Propose asks to delete a package. A person who reads the package's
// namespace may ask, so a team can ask for a layer above it; only that
// layer's maintainers decide. A machine token never asks: deleting is a
// person's call. The package needs a release or a proposal, and at most one
// request is open at a time.
func (s *DeletionService) Propose(ctx context.Context, pr Principal, id pack.ID, reason string) (Deletion, error) {
	pr, err := s.mayAsk(ctx, pr, id)
	if err != nil {
		return Deletion{}, err
	}
	if err := checkReason(reason); err != nil {
		return Deletion{}, err
	}
	fp, err := s.releases.PackageFootprint(ctx, id)
	if err != nil {
		return Deletion{}, fmt.Errorf("ask to delete %s: %w", id, err)
	}
	if len(fp.Deletions) > 0 {
		return Deletion{}, fmt.Errorf("ask to delete %s: %w: request %s is open; decide it or ask its author to withdraw it", id, ErrExists, fp.Deletions[0])
	}
	did, err := newDeletionID()
	if err != nil {
		return Deletion{}, fmt.Errorf("ask to delete %s: %w", id, err)
	}
	d := Deletion{
		ID:        did,
		Package:   id,
		Reason:    strings.TrimSpace(reason),
		Newest:    newestOf(fp),
		Author:    actorOf(pr),
		Status:    DeletionOpen,
		CreatedAt: s.now().UTC(),
	}
	if err := s.deletions.CreateDeletion(ctx, d); err != nil {
		return Deletion{}, fmt.Errorf("ask to delete %s: %w", id, err)
	}
	s.log.Info("deletion asked", "deletion", did, "package", id.String(), "token_id", pr.TokenID, "user_id", pr.UserID)
	return d, nil
}

// mayAsk checks that pr may ask to delete id, and returns it narrowed to
// the package's namespace.
func (s *DeletionService) mayAsk(ctx context.Context, pr Principal, id pack.ID) (Principal, error) {
	pr, err := s.in(ctx, pr, id.Team())
	if err != nil {
		return Principal{}, err
	}
	switch {
	case pr.Machine():
		return Principal{}, fmt.Errorf("%w: a machine token cannot ask to delete a package; a person asks, signed in with loadout login", ErrForbidden)
	case !pr.CanRead(id.Team()):
		return Principal{}, fmt.Errorf("%w: ask to delete from team %s", ErrForbidden, id.Team())
	}
	return pr, nil
}

// Footprint says what the package holds, and so what deleting it removes,
// for those who may ask to delete it. ErrNotFound when it holds nothing.
func (s *DeletionService) Footprint(ctx context.Context, pr Principal, id pack.ID) (PackageFootprint, error) {
	if _, err := s.mayAsk(ctx, pr, id); err != nil {
		return PackageFootprint{}, err
	}
	fp, err := s.releases.PackageFootprint(ctx, id)
	if err != nil {
		return PackageFootprint{}, fmt.Errorf("package %s: %w", id, err)
	}
	return fp, nil
}

// newestOf is the newest release in fp, none without one.
func newestOf(fp PackageFootprint) Base {
	if n := len(fp.Releases); n > 0 {
		return BaseAt(fp.Releases[n-1])
	}
	return Base{}
}

// Deletions lists the requests of namespace team (the principal's own when
// team is empty), newest first. An empty status means any.
func (s *DeletionService) Deletions(ctx context.Context, pr Principal, team string, status DeletionStatus) ([]Deletion, error) {
	if team != "" && pr.Account && !pr.CanRead(team) {
		var err error
		if pr, err = s.in(ctx, pr, team); err != nil {
			return nil, err
		}
	}
	if team == "" && !pr.Narrowed() {
		return nil, errNoView
	}
	if team == "" {
		team = pr.Team
	}
	if !pr.CanRead(team) {
		return nil, fmt.Errorf("%w: read team %s", ErrForbidden, team)
	}
	if status != "" && !status.Valid() {
		return nil, fmt.Errorf("%w: status %q: want open, approved, rejected, withdrawn or gone", ErrInvalid, status)
	}
	ds, err := s.deletions.Deletions(ctx, DeletionFilter{Team: team, Status: status, Limit: maxListed})
	if err != nil {
		return nil, fmt.Errorf("deletions of %s: %w", team, err)
	}
	return ds, nil
}

// Deletion returns one request. One in a namespace the principal cannot
// read is reported as not found, so its id cannot be probed.
func (s *DeletionService) Deletion(ctx context.Context, pr Principal, id string) (Deletion, error) {
	d, _, err := s.deletionAs(ctx, pr, id)
	return d, err
}

// deletionAs is Deletion, also returning pr narrowed to the request's
// namespace, which is where every decision about it is made.
func (s *DeletionService) deletionAs(ctx context.Context, pr Principal, id string) (Deletion, Principal, error) {
	d, err := s.deletions.Deletion(ctx, id)
	if err != nil {
		return Deletion{}, Principal{}, fmt.Errorf("deletion %s: %w", id, err)
	}
	if pr.Account {
		narrowed, err := s.in(ctx, pr, d.Package.Team())
		if errors.Is(err, ErrForbidden) {
			return Deletion{}, Principal{}, fmt.Errorf("deletion %s: %w", id, ErrNotFound)
		}
		if err != nil {
			return Deletion{}, Principal{}, err
		}
		pr = narrowed
	}
	if !pr.CanRead(d.Package.Team()) {
		return Deletion{}, Principal{}, fmt.Errorf("deletion %s: %w", id, ErrNotFound)
	}
	return d, pr, nil
}

// DeletionStanding is where an open request stands against its package now.
type DeletionStanding struct {
	// Footprint is what the package holds now, and so what approving it
	// removes. Zero when the package is gone.
	Footprint PackageFootprint
	// Newest is its newest release now.
	Newest Base
	// Stale says a release came out after the request was made, so it
	// cannot be approved: it was asked about something else.
	Stale bool
}

// Standing reports where d stands against its package now. Callers have
// read d, which is what lets them see its package.
func (s *DeletionService) Standing(ctx context.Context, d Deletion) (DeletionStanding, error) {
	fp, err := s.releases.PackageFootprint(ctx, d.Package)
	if errors.Is(err, ErrNotFound) {
		return DeletionStanding{}, nil
	}
	if err != nil {
		return DeletionStanding{}, fmt.Errorf("standing of %s: %w", d.ID, err)
	}
	newest := newestOf(fp)
	return DeletionStanding{Footprint: fp, Newest: newest, Stale: d.Newest.Before(newest)}, nil
}

// Approve deletes the package of an open request, so nobody gets it on the
// next sync. It needs a publish token of the namespace that is not the
// author's (four eyes), except for an owner approving their own request as
// owner with a note, which is recorded as deleted without review. A copy of
// the database comes first and a failure to take it deletes nothing. A
// release that came out after the request was made refuses it: it was asked
// about something else.
func (s *DeletionService) Approve(ctx context.Context, pr Principal, id string, in ApproveInput) (Deletion, error) {
	d, pr, err := s.deletionAs(ctx, pr, id)
	if err != nil {
		return Deletion{}, err
	}
	subj := d.subject()
	if err := checkDecider(pr, subj, "approve"); err != nil {
		return Deletion{}, err
	}
	if d.Status != DeletionOpen {
		return Deletion{}, fmt.Errorf("approve %s: %w: it is %s", id, ErrProposalClosed, d.Status)
	}
	self := selfError(pr, subj, "approve")
	if self != nil {
		if err := checkOwnApproval(ctx, s.owners, pr, subj, in.AsOwner, in.Note, self); err != nil {
			return Deletion{}, err
		}
	}
	if err := checkNote(in.Note); err != nil {
		return Deletion{}, err
	}
	if s.snapshots == nil {
		return Deletion{}, fmt.Errorf("%w: deleting a package needs a backup folder (LOADOUT_BACKUP_DIR) on the registry", ErrForbidden)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	st, err := s.Standing(ctx, d)
	if err != nil {
		return Deletion{}, err
	}
	if st.Stale {
		return Deletion{}, staleDeletion(d, st)
	}
	backup, err := s.snapshots.Take(ctx, "before-delete-"+d.Package.Team()+"-"+d.Package.Name())
	if err != nil {
		return Deletion{}, fmt.Errorf("back up before deleting %s, nothing was deleted: %w: %w", d.Package, ErrBackupFailed, err)
	}
	dec := Decision{Status: ProposalApproved, By: actorOf(pr), At: s.now().UTC(), Note: strings.TrimSpace(in.Note), SelfApproved: self != nil, Latest: d.Newest}
	done, err := s.deletions.ApproveDeletion(ctx, id, dec)
	if errors.Is(err, ErrStale) {
		return Deletion{}, staleDeletion(d, DeletionStanding{})
	}
	if err != nil {
		return Deletion{}, fmt.Errorf("approve %s: %w", id, err)
	}
	s.log.Info("package deleted after review", "deletion", id, "package", d.Package.String(), "self_approved", done.SelfApproved,
		"token_id", pr.TokenID, "user_id", pr.UserID, "author_token_id", d.Author.TokenID, "backup", backup,
		"releases", len(done.Removed.Releases), "proposals", done.Removed.Proposals, "reviews", done.Removed.Reviews, "prompts", done.Removed.Prompts)
	return done, nil
}

// staleDeletion is the refusal for a request whose package got a release
// after it was asked. st may be zero when the store found out, and the
// sentence then leaves the version out.
func staleDeletion(d Deletion, st DeletionStanding) error {
	what := "A release came out"
	if v := st.Newest.String(); v != "" {
		what = v + " came out"
	}
	return fmt.Errorf("approve %s: %w", d.ID, refusal(ErrBehind, "%s after this was asked, so it was asked about something else. Withdraw it and ask again to delete %s.", what, d.Package))
}

// Reject closes an open request. Like Approve it needs a publish token that
// is not the author's; authors withdraw instead.
func (s *DeletionService) Reject(ctx context.Context, pr Principal, id, note string) (Deletion, error) {
	d, pr, err := s.deletionAs(ctx, pr, id)
	if err != nil {
		return Deletion{}, err
	}
	if err := checkReviewer(pr, d.subject(), "reject"); err != nil {
		return Deletion{}, fmt.Errorf("%w (authors withdraw their own requests)", err)
	}
	return s.decide(ctx, pr, id, ProposalRejected, note)
}

// Withdraw closes an open request. Only its author may withdraw it.
func (s *DeletionService) Withdraw(ctx context.Context, pr Principal, id, note string) (Deletion, error) {
	d, pr, err := s.deletionAs(ctx, pr, id)
	if err != nil {
		return Deletion{}, err
	}
	if !d.By(pr) {
		return Deletion{}, fmt.Errorf("%w: only the author can withdraw request %s", ErrForbidden, id)
	}
	return s.decide(ctx, pr, id, ProposalWithdrawn, note)
}

func (s *DeletionService) decide(ctx context.Context, pr Principal, id string, status ProposalStatus, note string) (Deletion, error) {
	if err := checkNote(note); err != nil {
		return Deletion{}, err
	}
	dec := Decision{Status: status, By: actorOf(pr), At: s.now().UTC(), Note: strings.TrimSpace(note)}
	d, err := s.deletions.DecideDeletion(ctx, id, dec)
	if err != nil {
		return Deletion{}, fmt.Errorf("%s %s: %w", status, id, err)
	}
	s.log.Info("deletion request closed", "deletion", id, "status", status, "token_id", pr.TokenID)
	return d, nil
}

// RecordDelete deletes a package that someone with the right to do so
// deleted directly, and keeps the record: who, why and what went, in one
// transaction. The caller has checked who may and taken any copy of the
// database; this is the part that leaves the trace. Any open request for the
// package closes as gone.
func (s *DeletionService) RecordDelete(ctx context.Context, by Actor, id pack.ID, reason string) (PackageFootprint, error) {
	if err := checkReason(reason); err != nil {
		return PackageFootprint{}, err
	}
	did, err := newDeletionID()
	if err != nil {
		return PackageFootprint{}, fmt.Errorf("delete package %s: %w", id, err)
	}
	fp, err := s.deletions.RecordDeletion(ctx, Deletion{ID: did, Package: id, Reason: strings.TrimSpace(reason), Author: by, CreatedAt: s.now().UTC()})
	if err != nil {
		return PackageFootprint{}, fmt.Errorf("delete package %s: %w", id, err)
	}
	s.log.Info("package deleted", "deletion", did, "package", id.String(), "token_id", by.TokenID, "user_id", by.UserID,
		"releases", len(fp.Releases), "proposals", fp.Proposals, "reviews", fp.Reviews, "prompts", fp.Prompts, "courses", len(fp.Courses))
	return fp, nil
}
