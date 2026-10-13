package memory

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/petervlad/loadout/internal/registry"
)

// Saved is the result of Save: either the new entry, or the entries that look
// like the draft and nothing saved.
type Saved struct {
	Entry      Entry   // zero when Duplicates is set
	Duplicates []Entry // at most MaxDuplicates, best first
}

// Done reports whether the draft was saved.
func (r Saved) Done() bool { return len(r.Duplicates) == 0 }

// Save stores a new entry in ns, which must be the draft's namespace. The
// person needs the Write right and a write left in today's 200. Unless force
// is set, a draft that looks like entries the person can read saves nothing
// and returns them.
func (s *Service) Save(ctx context.Context, c Caller, ns string, d Draft, force bool) (Saved, error) {
	if d.Namespace() != ns {
		return Saved{}, refuse(RuleNamespace, "namespace", "The memory belongs to another team than this one.")
	}
	a, err := s.writer(ctx, c, ns)
	if err != nil {
		return Saved{}, err
	}
	if err := s.checkLinks(ctx, a, d.Links()); err != nil {
		return Saved{}, s.refused(ctx, "save", c, ns, err)
	}
	if !force {
		dups, err := s.duplicates(ctx, a, d)
		if err != nil {
			return Saved{}, err
		}
		if len(dups) > 0 {
			return Saved{Duplicates: dups}, nil
		}
	}
	e, err := s.store.Insert(ctx, d, c, s.now(), s.dailyLimit())
	if err != nil {
		return Saved{}, s.writeFailed(ctx, c, fmt.Errorf("save memory: %w", err))
	}
	s.logWrite(ctx, "memory saved", c, e)
	return Saved{Entry: e}, nil
}

// Update applies ch to entry id, which the person read at version base. base
// 0 means the newest version. An entry that has moved on is ErrStale and
// nothing changes. A Change that only confirms writes a confirm version.
func (s *Service) Update(ctx context.Context, c Caller, id int64, base int, ch Change) (Entry, error) {
	e, a, err := s.entryOf(ctx, c, id)
	if err != nil {
		return Entry{}, err
	}
	if !a.Write {
		return Entry{}, notAllowed("change")
	}
	if !e.Active() {
		return Entry{}, fmt.Errorf("%w: memory %d is retracted", registry.ErrInvalid, id)
	}
	if base <= 0 {
		base = e.Version
	}
	if err := s.underDailyLimit(ctx, c); err != nil {
		return Entry{}, err
	}
	rev, err := ch.Revise(e, s.now())
	if err == nil && ch.Links != nil {
		err = s.checkLinks(ctx, a, rev.Links())
	}
	if err != nil {
		return Entry{}, s.refused(ctx, "update", c, e.Namespace, err)
	}
	out, err := s.store.Update(ctx, id, base, rev, c, s.now(), s.dailyLimit())
	if err != nil {
		return Entry{}, s.writeFailed(ctx, c, fmt.Errorf("update memory %d: %w", id, err))
	}
	s.logWrite(ctx, "memory updated", c, out)
	return out, nil
}

// Retract hides entry id and keeps its history. Its author may, and so may
// those who moderate its namespace. It does not count against the daily
// limit, so a person at the limit can still take back a mistake.
func (s *Service) Retract(ctx context.Context, c Caller, id int64, reason string) (Entry, error) {
	e, a, err := s.entryOf(ctx, c, id)
	if err != nil {
		return Entry{}, err
	}
	reason, err = CheckReason(reason)
	if err != nil {
		return Entry{}, s.refused(ctx, "retract", c, e.Namespace, err)
	}
	if e.AuthorID != c.UserID && !a.Moderate {
		return Entry{}, notAllowed("retract")
	}
	out, err := s.store.Retract(ctx, id, reason, c, s.now())
	if err != nil {
		return Entry{}, fmt.Errorf("retract memory %d: %w", id, err)
	}
	s.logWrite(ctx, "memory retracted", c, out)
	return out, nil
}

// Move lifts entry id from its team to the project above it, keeping its id,
// author and history. The person must moderate the team and the project must
// have memory on. No right on the project is needed.
func (s *Service) Move(ctx context.Context, c Caller, id int64) (Entry, error) {
	e, a, err := s.entryOf(ctx, c, id)
	if err != nil {
		return Entry{}, err
	}
	if !a.Moderate {
		return Entry{}, notAllowed("move")
	}
	project := parentOf(a.Layers, e.Namespace)
	if project == "" {
		return Entry{}, fmt.Errorf("%w: a memory that is not in a team has no project to move up to", registry.ErrInvalid)
	}
	if !slices.Contains(a.readable, project) {
		return Entry{}, fmt.Errorf("%w: team memory is off for %s, so nothing can move there. Ask an owner of %s to turn it on", registry.ErrInvalid, project, project)
	}
	out, err := s.store.Move(ctx, id, project, c, s.now())
	if err != nil {
		return Entry{}, fmt.Errorf("move memory %d: %w", id, err)
	}
	s.logWrite(ctx, "memory moved", c, out, "from", e.Namespace)
	return out, nil
}

// parentOf is the layer after ns, or "" if there is none.
func parentOf(layers []string, ns string) string {
	for _, l := range layers {
		if l != ns {
			return l
		}
	}
	return ""
}

// writer resolves ns for a person who needs to write: the Write right, and a
// write left in today's limit.
func (s *Service) writer(ctx context.Context, c Caller, ns string) (access, error) {
	a, err := s.resolve(ctx, c, ns)
	if err != nil {
		return access{}, err
	}
	if !a.Write {
		return access{}, notAllowed("save to")
	}
	return a, s.underDailyLimit(ctx, c)
}

func notAllowed(what string) error {
	return fmt.Errorf("%w: you may not %s this team's memory", registry.ErrForbidden, what)
}

// dailyLimit is the limit a store enforces in the transaction of a write, as
// a second line behind underDailyLimit.
func (s *Service) dailyLimit() WriteOption { return Within(s.now().Add(-WriteWindow), WritesPerDay) }

// writeFailed turns a store's refusal at the limit into the daily refusal.
func (s *Service) writeFailed(ctx context.Context, c Caller, err error) error {
	if lr, ok := errors.AsType[*WriteLimitError](err); ok {
		r := DailyRefusal(lr.Oldest)
		s.log.WarnContext(ctx, "memory refused", "rule", r.Rule, "user", c.UserID)
		return r
	}
	return err
}

// underDailyLimit refuses the write after the 200th in a rolling day.
func (s *Service) underDailyLimit(ctx context.Context, c Caller) error {
	w, err := s.store.WritesSince(ctx, c.UserID, s.now().Add(-WriteWindow))
	if err != nil {
		return fmt.Errorf("count writes: %w", err)
	}
	if w.Count >= WritesPerDay {
		err := DailyRefusal(w.Oldest)
		s.log.WarnContext(ctx, "memory refused", "rule", err.Rule, "user", c.UserID)
		return err
	}
	return nil
}

// checkLinks refuses a link to an entry the layers cannot read, or one that
// is retracted. The store only knows whether an id exists.
func (s *Service) checkLinks(ctx context.Context, a access, links []int64) error {
	if len(links) == 0 {
		return nil
	}
	found, err := s.store.GetMany(ctx, links)
	if err != nil {
		return fmt.Errorf("read linked memories: %w", err)
	}
	ok := 0
	for _, l := range found {
		if l.Active() && slices.Contains(a.readable, l.Namespace) {
			ok++
		}
	}
	if ok != len(links) {
		return refuse(RuleMaxLinks, "links", "A link must be the id of an active memory of this team or its project.")
	}
	return nil
}

// refused logs a Refusal by its rule and position, never its text, and
// returns err unchanged.
func (s *Service) refused(ctx context.Context, op string, c Caller, ns string, err error) error {
	if r, ok := errors.AsType[*Refusal](err); ok {
		s.log.WarnContext(ctx, "memory refused", "op", op, "namespace", ns, "user", c.UserID,
			"rule", r.Rule, "field", r.Field, "line", r.Line, "column", r.Column)
	}
	return err
}

// logWrite logs what happened with sizes and ids only. The text of an entry
// and of a reason never reach a log.
func (s *Service) logWrite(ctx context.Context, msg string, c Caller, e Entry, extra ...any) {
	s.log.InfoContext(ctx, msg, append([]any{
		"id", e.ID, "namespace", e.Namespace, "user", c.UserID, "version", e.Version,
		"name_bytes", len(e.Name), "description_bytes", len(e.Description), "body_bytes", len(e.Body),
	}, extra...)...)
}
