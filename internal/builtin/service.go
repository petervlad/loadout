package builtin

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"time"

	"github.com/petervlad/loadout/internal/detect"
	"github.com/petervlad/loadout/internal/install"
	"github.com/petervlad/loadout/internal/lock"
	"github.com/petervlad/loadout/internal/pack"
	"github.com/petervlad/loadout/internal/syncstate"
	"github.com/petervlad/loadout/internal/target"
)

// Installer is the part of install.Installer the service uses.
type Installer interface {
	Install(ctx context.Context, p *pack.Pack, opts install.Options) (install.Result, error)
	Entries(ctx context.Context) ([]lock.Entry, error)
}

var _ Installer = (*install.Installer)(nil)

// Detector finds the AI tools whose folder exists; *detect.Detector is one.
type Detector interface {
	Targets(scope target.Scope, supported []string) ([]string, error)
}

var _ Detector = (*detect.Detector)(nil)

// Memory keeps what loadout learned between runs, so status and the
// session hooks can say that a skill's new text is held back by an edit;
// *syncstate.Store is one.
type Memory interface {
	Update(ctx context.Context, fn func(*syncstate.State) error) error
}

var _ Memory = (*syncstate.Store)(nil)

// Action is what happened to one built-in skill.
type Action string

// What Ensure and Setup do with each skill.
const (
	ActionInstalled Action = "installed"
	ActionUpdated   Action = "updated"
	ActionUnchanged Action = "unchanged"
	// ActionKept means a file the person edited, or made themselves, is
	// in the way; nothing was written.
	ActionKept Action = "kept"
	// ActionDeclined means the person uninstalled the skill.
	ActionDeclined Action = "declined"
	// ActionPinned means the install's policy is pin.
	ActionPinned Action = "pinned"
	// ActionOther means a registry package with the same id is installed;
	// it is the person's, so it is left alone.
	ActionOther Action = "other package"
)

// Outcome is the result for one skill.
type Outcome struct {
	Name string
	// Set is the name of the set the skill belongs to.
	Set    string
	Action Action
	// Err explains ActionKept.
	Err error
}

// Report is the result of Ensure or Setup.
type Report struct {
	// NoTool is set when no AI tool's folder exists and none was named:
	// nothing was written, and the wish is recorded for sync.
	NoTool bool
	// Targets are the tools the skills were installed for.
	Targets []string
	Skills  []Outcome
}

// Names returns the skills whose outcome is one of actions.
func (r Report) Names(actions ...Action) []string {
	var out []string
	for _, o := range r.Skills {
		if slices.Contains(actions, o.Action) {
			out = append(out, o.Name)
		}
	}
	return out
}

// Service installs the built-in skills for the person and keeps them in
// step with the CLI.
type Service struct {
	packs     []setPack
	installer Installer
	detector  Detector
	state     stateFile
	memory    Memory
	home      string
	log       *slog.Logger
	now       func() time.Time
}

// setPack is a built-in skill with the set it belongs to.
type setPack struct {
	*pack.Pack
	set Set
}

// Options configure a Service.
type Options struct {
	// Version is the CLI version; the skills get the matching package
	// version.
	Version string
	// StatePath is the file that records whether the person wants the
	// skills and which ones they uninstalled.
	StatePath string
	// Home is the person's home folder: the skills are user scope.
	Home string
	// Memory, if set, records skills whose new text an edit holds back.
	Memory Memory
	// Now is the clock for those records; nil is time.Now.
	Now func() time.Time
	Log *slog.Logger
}

// New returns a service for the embedded skills of every set.
func New(installer Installer, detector Detector, opts Options) (*Service, error) {
	var packs []setPack
	for _, set := range Sets() {
		ps, err := PacksOf(set, opts.Version)
		if err != nil {
			return nil, err
		}
		for _, p := range ps {
			packs = append(packs, setPack{Pack: p, set: set})
		}
	}
	if opts.StatePath == "" || opts.Home == "" {
		return nil, errors.New("built-in skills need a state path and a home folder")
	}
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	return &Service{packs: packs, installer: installer, detector: detector, state: stateFile{path: opts.StatePath}, memory: opts.Memory, home: opts.Home, log: opts.Log, now: now}, nil
}

// Packs returns the built-in packages of every set.
func (s *Service) Packs() []*pack.Pack {
	out := make([]*pack.Pack, len(s.packs))
	for i, p := range s.packs {
		out[i] = p.Pack
	}
	return out
}

// IsBuiltin reports whether id names a built-in skill of any set.
func (s *Service) IsBuiltin(id pack.ID) bool {
	return slices.ContainsFunc(s.packs, func(p setPack) bool { return p.ID() == id })
}

// SetOf returns the set of the built-in skill id.
func (s *Service) SetOf(id pack.ID) (Set, bool) {
	i := slices.IndexFunc(s.packs, func(p setPack) bool { return p.ID() == id })
	if i < 0 {
		return Set{}, false
	}
	return s.packs[i].set, true
}

// Want records that the person wants the skills, as login does.
func (s *Service) Want(context.Context) error {
	_, err := s.state.update(func(st *state) { st.Wanted = true })
	return err
}

// Decline records that the person uninstalled id, so login and sync never
// add it back. It does nothing for a package that is not built in.
func (s *Service) Decline(_ context.Context, id pack.ID) error {
	if !s.IsBuiltin(id) {
		return nil
	}
	_, err := s.state.update(func(st *state) {
		if !st.declined(id.Name()) {
			st.Declined = append(st.Declined, id.Name())
		}
	})
	return err
}

// Ensure brings the skills in line with this CLI for every AI tool found.
// The everyday skills are handled when the person wants them; a skill of
// another set, such as the host skills, only when it is already installed,
// so sync updates them and never adds them. A skill is written only when it is missing,
// when this CLI ships other text, or when a tool appeared that it is not
// installed for; so a skill someone edited is left alone until there is
// a new version, and even then never overwritten. Declined and pinned
// skills are left as they are. No tool folder means nothing is written.
func (s *Service) Ensure(ctx context.Context) (Report, error) {
	st, err := s.state.load()
	if err != nil {
		return Report{}, err
	}
	return s.apply(ctx, request{state: st})
}

// Setup installs the skills now, as `loadout setup --skills` asks: it
// forgets earlier uninstalls and records the wish. targets names the tools
// (their folders are created if needed, since the person asked); empty
// means the tools found. With force it overwrites a skill's edited files.
func (s *Service) Setup(ctx context.Context, targets []string, force bool) (Report, error) {
	return s.SetupSets(ctx, []string{SetEveryday}, targets, force)
}

// SetupSets installs the named sets now, as `loadout setup --skills` and
// `--host-skills` ask. Like Setup it forgets uninstalls of those skills; it
// records the wish for the everyday skills only, so asking for the host
// skills does not make login and sync add the rest.
func (s *Service) SetupSets(ctx context.Context, sets []string, targets []string, force bool) (Report, error) {
	for _, name := range sets {
		if _, ok := SetNamed(name); !ok {
			return Report{}, fmt.Errorf("there is no set of built-in skills called %q", name)
		}
	}
	st, err := s.state.update(func(st *state) {
		st.Wanted = st.Wanted || slices.Contains(sets, SetEveryday)
		st.Declined = slices.DeleteFunc(st.Declined, func(n string) bool {
			return slices.ContainsFunc(s.packs, func(p setPack) bool {
				return p.ID().Name() == n && slices.Contains(sets, p.set.Name)
			})
		})
	})
	if err != nil {
		return Report{}, err
	}
	return s.apply(ctx, request{state: st, targets: targets, sets: sets, force: force})
}

// request is one run of Ensure or Setup.
type request struct {
	state   state
	targets []string
	// sets are the sets asked for now. Their skills are installed even when
	// the lock says they are up to date, so an edit is reported (or, with
	// force, replaced).
	sets  []string
	force bool
}

// wants decides whether a skill is considered in this run: one of the sets
// asked for now, one of an Auto set the person wants, or one of another set
// that is already installed.
func (req request) wants(set Set, prev *lock.Entry) (asked, considered bool) {
	asked = slices.Contains(req.sets, set.Name)
	return asked, asked || (set.Auto && req.state.Wanted) || (!set.Auto && prev != nil)
}

func (s *Service) apply(ctx context.Context, req request) (Report, error) {
	scope := target.Scope{Kind: target.ScopeUser, Root: s.home}
	entries, err := s.installer.Entries(ctx)
	if err != nil {
		return Report{}, err
	}
	type job struct {
		p     setPack
		prev  *lock.Entry
		asked bool
	}
	var jobs []job
	for _, p := range s.packs {
		var prev *lock.Entry
		if i := slices.IndexFunc(entries, func(e lock.Entry) bool { return e.ID == p.ID() && e.Scope == scope }); i >= 0 {
			prev = &entries[i]
		}
		if asked, ok := req.wants(p.set, prev); ok {
			jobs = append(jobs, job{p, prev, asked})
		}
	}
	if len(jobs) == 0 {
		return Report{}, nil
	}
	if len(req.targets) == 0 {
		found, err := s.detector.Targets(scope, nil)
		if errors.Is(err, detect.ErrNoTool) {
			return Report{NoTool: true}, nil
		}
		if err != nil {
			return Report{}, err
		}
		req.targets = found
	}
	rep := Report{Targets: slices.Clone(req.targets)}
	var held []heldSkill
	for _, j := range jobs {
		if err := ctx.Err(); err != nil {
			return rep, err
		}
		o, err := s.one(ctx, req, j.p, j.prev, scope, j.asked)
		if err != nil {
			return rep, err
		}
		rep.Skills = append(rep.Skills, o)
		held = append(held, heldSkill{pack: j.p.Pack, prev: j.prev, outcome: o})
	}
	s.remember(ctx, scope, held)
	return rep, nil
}

// heldSkill is one skill's outcome with what remember needs.
type heldSkill struct {
	pack    *pack.Pack
	prev    *lock.Entry
	outcome Outcome
}

// remember records each installed skill whose new text an edit keeps out,
// and forgets the ones in step. A skill another package owns is left to
// sync. A failure only warns: the skills themselves are fine.
func (s *Service) remember(ctx context.Context, scope target.Scope, skills []heldSkill) {
	if s.memory == nil {
		return
	}
	now := s.now()
	err := s.memory.Update(ctx, func(st *syncstate.State) error {
		for _, h := range skills {
			k := syncstate.Key{ID: h.pack.ID(), Scope: scope}
			ce, kept := errors.AsType[*install.ConflictError](h.outcome.Err)
			switch {
			case h.outcome.Action == ActionOther:
			case kept && h.outcome.Action == ActionKept && h.prev != nil:
				b := syncstate.Behind{
					ID: k.ID, Scope: scope, Version: h.prev.Version, Latest: h.pack.Version(),
					Held: syncstate.Blockers(scope.Root, ce), Since: now, Checked: now,
					Told: true, // no notifier hears of built-in skills
				}
				if old, ok := st.Find(k); ok && old.Latest.Compare(b.Latest) == 0 {
					b.Since = old.Since
				}
				st.Put(b)
			default:
				st.Drop(k)
			}
		}
		return nil
	})
	if err != nil {
		s.log.Warn("could not record loadout's skills held back by your edits", "err", err)
	}
}

// one installs a single skill if it needs it; prev is its install, if any.
// asked is set when the person named the skill's set in this run.
func (s *Service) one(ctx context.Context, req request, p setPack, prev *lock.Entry, scope target.Scope, asked bool) (Outcome, error) {
	o := Outcome{Name: p.ID().Name(), Set: p.set.Name}
	switch {
	case req.state.declined(o.Name):
		o.Action = ActionDeclined
		return o, nil
	case prev != nil && prev.Source != Source:
		o.Action = ActionOther
		return o, nil
	case asked:
	case prev != nil && prev.Policy == lock.PolicyPin:
		o.Action = ActionPinned
		return o, nil
	case prev != nil && prev.Digest == p.Digest() && covers(prev.Targets(), req.targets):
		o.Action = ActionUnchanged
		return o, nil
	}
	opts := install.Options{Scope: scope, Targets: req.targets, Source: Source, Force: req.force && asked}
	if prev == nil {
		opts.Policy = lock.PolicyAuto
	}
	res, err := s.installer.Install(ctx, p.Pack, opts)
	if _, ok := errors.AsType[*install.ConflictError](err); ok {
		o.Action, o.Err = ActionKept, err
		return o, nil
	}
	if err != nil {
		return o, fmt.Errorf("install %s: %w", p.ID(), err)
	}
	switch {
	case res.Unchanged:
		o.Action = ActionUnchanged
	case prev != nil:
		o.Action = ActionUpdated
	default:
		o.Action = ActionInstalled
	}
	s.log.Debug("built-in skill", "name", o.Name, "action", o.Action, "version", p.Version())
	return o, nil
}

// covers reports whether have includes every tool in want.
func covers(have, want []string) bool {
	for _, w := range want {
		if !slices.Contains(have, w) {
			return false
		}
	}
	return true
}
