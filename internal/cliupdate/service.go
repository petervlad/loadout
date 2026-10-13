// Package cliupdate remembers what registries say about loadout releases
// and decides when to tell the person, or their agent, that this CLI is
// behind. It opens no network connection: the client hands it the
// registry's two headers, and everything else comes from a file on this
// machine.
package cliupdate

import (
	"context"
	"errors"
	"log/slog"
	"net/url"
	"slices"
	"sync"
	"time"

	"github.com/petervlad/loadout/internal/buildinfo"
	"github.com/petervlad/loadout/internal/client"
	"github.com/petervlad/loadout/internal/semver"
	"github.com/petervlad/loadout/internal/target"
)

// lockWait bounds how long the line after a command and the session hook
// wait for another loadout holding the state file: news is not worth
// holding up a command, so they stay silent instead.
const lockWait = time.Second

// refresh is how old a registry's record may grow before a run that heard
// the same advice writes it again, keeping it from expiring.
const refresh = 24 * time.Hour

// Options configure a Service.
type Options struct {
	Store  *Store
	Build  buildinfo.Build
	Method buildinfo.Method
	// Now is the clock; nil uses time.Now.
	Now func() time.Time
	Log *slog.Logger
	// Quiet keeps the newer-release news to status, notices and version
	// (LOADOUT_UPDATE_NOTICE=off). A registry that needs a newer loadout
	// is still said, since it explains why commands fail.
	Quiet bool
}

// Service records advice and decides when to speak. It is safe for
// concurrent use.
type Service struct {
	store  *Store
	build  buildinfo.Build
	method buildinfo.Method
	now    func() time.Time
	log    *slog.Logger
	quiet  bool

	mu   sync.Mutex
	seen map[string]Advice // this run's advice, by registry URL
}

// New returns a Service over the state in o.Store.
func New(o Options) *Service {
	now := o.Now
	if now == nil {
		now = time.Now
	}
	log := o.Log
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &Service{store: o.Store, build: o.Build, method: o.Method, now: now, log: log, quiet: o.Quiet, seen: map[string]Advice{}}
}

// Saw collects one response's advice from the registry at url, in memory.
// A value that is not a version is dropped, never stored.
func (s *Service) Saw(url string, a client.CLIAdvice) {
	adv := Advice{URL: url, Latest: version(a.Latest), Minimum: version(a.Minimum)}
	if adv.Latest == (semver.Version{}) && adv.Minimum == (semver.Version{}) {
		return
	}
	adv.Seen = s.now()
	s.mu.Lock()
	defer s.mu.Unlock()
	s.seen[url] = adv
}

func version(s string) semver.Version {
	v, err := semver.Parse(s)
	if err != nil {
		return semver.Version{}
	}
	return v
}

// heard returns a copy of this run's advice.
func (s *Service) heard() []Advice {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Advice, 0, len(s.seen))
	for _, a := range s.seen {
		out = append(out, a)
	}
	return out
}

// Finish writes what this run heard (only when it is news, a record is a
// day old, or a clock moves, so most runs never write) and returns the line
// for a: a newer release at most once a day per version and clock, a
// registry that needs a newer loadout every time, else nothing. When the
// state cannot be saved (another loadout holds it longer than lockWait)
// only the too-old warning is returned, since no clock moved.
func (s *Service) Finish(ctx context.Context, a Audience) (string, error) {
	heard := s.heard()
	now := s.now()
	st := s.load()
	saved := st.clone()
	for _, adv := range heard {
		st.put(adv)
	}
	status := s.statusOf(st, now)
	tell := s.due(st, status, a.clock(), now)
	if !tell && !news(saved, heard, now) {
		return s.lineFor(status, a, tell), nil
	}
	ctx, cancel := context.WithTimeout(ctx, lockWait)
	defer cancel()
	err := s.store.Update(ctx, func(st *State) error {
		fresh := news(*st, heard, now)
		for _, adv := range heard {
			st.put(adv)
		}
		st.prune(now)
		// Decided again under the lock: an agent runs several commands at
		// once, and another may have said it since this one read the file.
		status = s.statusOf(*st, now)
		if tell = s.due(*st, status, a.clock(), now); tell {
			st.tell(a.clock(), Told{Version: status.Latest, At: now})
		} else if !fresh {
			return errNothingToSave
		}
		return nil
	})
	if errors.Is(err, errNothingToSave) {
		err = nil
	} else if err != nil {
		tell = false
	}
	return s.lineFor(status, a, tell), err
}

// lineFor is the line a hears now: the too-old warning every time, the
// newer release only when its clock is due.
func (s *Service) lineFor(status Status, a Audience, due bool) string {
	if len(status.TooOld) > 0 || due {
		return status.Line(a)
	}
	return ""
}

// due reports whether c should hear that status.Latest is out: this build
// is behind, the news is not silenced, and c has not heard of that version
// in the last day.
func (s *Service) due(st State, status Status, c clock, now time.Time) bool {
	if s.quiet || len(status.TooOld) > 0 || !status.Behind() {
		return false
	}
	told, ok := st.lastTold(c)
	return !ok || told.Version != status.Latest || now.Sub(told.At) >= refresh
}

// SessionMessage is what the session hook says, on the agents' clock: the
// commands an agent runs and the hook share it, so a session does not hear
// the same news twice in a day. When the clock cannot be saved within
// lockWait it says nothing new (a too-old warning still).
func (s *Service) SessionMessage(ctx context.Context) (target.SessionMessage, error) {
	now := s.now()
	st := s.load()
	status := s.statusOf(st, now)
	if len(status.TooOld) > 0 {
		return status.Session(), nil
	}
	if !s.due(st, status, clockAgent, now) {
		return target.SessionMessage{}, nil
	}
	tell := true
	ctx, cancel := context.WithTimeout(ctx, lockWait)
	defer cancel()
	err := s.store.Update(ctx, func(st *State) error {
		// Decided again under the lock, as in Finish.
		status = s.statusOf(*st, now)
		if tell = s.due(*st, status, clockAgent, now); !tell {
			return errNothingToSave
		}
		st.tell(clockAgent, Told{Version: status.Latest, At: now})
		return nil
	})
	if errors.Is(err, errNothingToSave) {
		err = nil
	}
	if (err != nil || !tell) && len(status.TooOld) == 0 {
		return target.SessionMessage{}, err
	}
	return status.Session(), err
}

// Pusher sends one message to the person's phone; pushover.Notifier is
// one.
type Pusher interface {
	Push(ctx context.Context, title, message string) error
}

// Push tells p about a newer release once per version, on its own clock,
// from sync (cron): a failed push is tried again on the next run. With
// Quiet only a registry that needs a newer loadout is pushed.
func (s *Service) Push(ctx context.Context, p Pusher) error {
	if err := s.push(ctx, p); !errors.Is(err, errNothingToSave) {
		return err
	}
	return nil
}

func (s *Service) push(ctx context.Context, p Pusher) error {
	now := s.now()
	st := s.load()
	status := s.statusOf(st, now)
	if !s.pushDue(st, status) {
		return nil
	}
	// Decided again under the lock, and the push sent while holding it, so
	// two syncs at once send one push. Returning an error saves nothing:
	// a failed push is tried again next run.
	return s.store.Update(ctx, func(st *State) error {
		status := s.statusOf(*st, now)
		if !s.pushDue(*st, status) {
			return errNothingToSave
		}
		title, message := status.Push()
		if err := p.Push(ctx, title, message); err != nil {
			return err
		}
		st.tell(clockPush, Told{Version: status.Latest, At: now})
		return nil
	})
}

// errNothingToSave ends an Update without writing, when the decision
// taken again under the lock is that nothing changed; callers drop it.
var errNothingToSave = errors.New("nothing to save")

// pushDue reports whether the push clock should hear of status.Latest.
func (s *Service) pushDue(st State, status Status) bool {
	if !status.Behind() || (s.quiet && len(status.TooOld) == 0) {
		return false
	}
	told, ok := st.lastTold(clockPush)
	return !ok || told.Version != status.Latest
}

// news reports whether heard says something st does not, or refreshes a
// record older than a day.
func news(st State, heard []Advice, now time.Time) bool {
	for _, a := range heard {
		old, ok := st.find(a.URL)
		if !ok || old.Latest != a.Latest || old.Minimum != a.Minimum || now.Sub(old.Seen) >= refresh {
			return true
		}
	}
	return false
}

// find returns the advice remembered for the registry at url.
func (st State) find(url string) (Advice, bool) {
	for _, a := range st.registries {
		if a.URL == url {
			return a, true
		}
	}
	return Advice{}, false
}

// load reads the state; a damaged or unreadable file is the empty state.
func (s *Service) load() State {
	st, err := s.store.Load()
	if err != nil {
		s.log.Debug("cli update state reads as empty", "err", err)
		return State{}
	}
	return st
}

// Status is what the remembered advice means for this build. It reads the
// file only and never writes, for status, notices and version.
func (s *Service) Status() Status {
	return s.statusOf(s.load(), s.now())
}

func (s *Service) statusOf(st State, now time.Time) Status {
	out := Status{Build: s.build, Method: s.method.For("")}
	for _, a := range st.registries {
		if now.Sub(a.Seen) > maxAge {
			continue
		}
		if a.Seen.After(out.Checked) {
			out.Checked = a.Seen
		}
		if c := a.Latest.Compare(out.Latest); c > 0 || (c == 0 && a.Latest != (semver.Version{}) && a.Seen.After(out.Seen)) {
			out.Latest, out.From, out.Seen = a.Latest, host(a.URL), a.Seen
			out.Method = s.method.For(a.URL)
		}
		if a.Minimum != (semver.Version{}) && s.build.Behind(a.Minimum) {
			out.TooOld = append(out.TooOld, a)
		}
	}
	// A registry that needs a newer loadout is the one to update from, even
	// when another has advertised a newer release.
	if len(out.TooOld) > 0 {
		worst := slices.MaxFunc(out.TooOld, func(a, b Advice) int { return a.Minimum.Compare(b.Minimum) })
		out.Method = s.method.For(worst.URL)
	}
	return out
}

// host names a registry as the person's own config writes it.
func host(raw string) string {
	if u, err := url.Parse(raw); err == nil && u.Host != "" {
		return u.Host
	}
	return raw
}

// Status is what the remembered advice means for this build.
type Status struct {
	Build  buildinfo.Build
	Method buildinfo.Method
	// Latest is the newest release any registry advertised in the last 30
	// days; From is that registry's host and Seen when it last said so.
	Latest semver.Version
	From   string
	Seen   time.Time
	// TooOld are the registries whose minimum is above this build.
	TooOld []Advice
	// Checked is when any registry last said anything.
	Checked time.Time
}

// Behind reports whether a newer release than this build is out.
func (s Status) Behind() bool { return s.Build.Behind(s.Latest) }
