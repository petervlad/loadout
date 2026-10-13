// Package update brings installed packages up to date with their registry,
// following each install's policy: auto installs the new version, notify
// announces it, pin leaves it alone. A change to the team's model table is
// treated like a new version of every agent it changes.
package update

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/petervlad/loadout/internal/agent"
	"github.com/petervlad/loadout/internal/api"
	"github.com/petervlad/loadout/internal/install"
	"github.com/petervlad/loadout/internal/lock"
	"github.com/petervlad/loadout/internal/pack"
	"github.com/petervlad/loadout/internal/semver"
	"github.com/petervlad/loadout/internal/syncstate"
	"github.com/petervlad/loadout/internal/tier"
)

// ErrGone is what Registry.Latest returns for a package the registry no
// longer has: the operator deleted it. It is not a failure of the check.
var ErrGone = errors.New("package is no longer on its registry")

// Registry is what update needs from one remote registry.
type Registry interface {
	// Releases lists the published releases of id. A package the registry
	// does not know, or one with no release left, wraps ErrGone.
	Releases(ctx context.Context, id pack.ID) ([]Release, error)
	// Needs resolves what p needs to render: the highest release of each
	// package it names at the pinned major, and the definitions of the
	// agents it names that the team's view has.
	Needs(ctx context.Context, p *pack.Pack) (Needed, error)
	// Fetch downloads and verifies one version.
	Fetch(ctx context.Context, id pack.ID, version semver.Version) (*pack.Pack, error)
	// Tiers returns the team's resolved model table; the empty table for
	// a registry that has none.
	Tiers(ctx context.Context) (tier.Table, error)
}

// Release is one published version of a package.
type Release struct {
	Version semver.Version
	Kind    pack.Kind
}

// Needed is what a package renders from besides its own files.
type Needed struct {
	Requires []*pack.Pack
	Agents   []agent.Definition
}

// Source is the registry an install updates from, and the remote it is.
type Source struct {
	Registry Registry
	// Remote and URL name the resolved remote; they are recorded on an
	// updated install, so the lock follows a team moving registries.
	Remote string
	URL    string
}

// Connector opens the registry an install updates from: its team's
// registry, or else the remote it was installed from.
type Connector func(e lock.Entry) (Source, error)

// Installer is the part of install.Installer update uses.
type Installer interface {
	Entries(ctx context.Context) ([]lock.Entry, error)
	Install(ctx context.Context, p *pack.Pack, opts install.Options) (install.Result, error)
	Rerenders(ctx context.Context, p *pack.Pack, opts install.Options) (bool, error)
	// Edited returns the files of e that exist and differ from what
	// loadout wrote (slash paths, relative to the scope root); only checks
	// the paths in only when it is not empty.
	Edited(ctx context.Context, e lock.Entry, only []string) ([]string, error)
}

var _ Installer = (*install.Installer)(nil)

// Memory keeps what sync learned between runs; *syncstate.Store is one.
type Memory interface {
	Load() (syncstate.State, error)
	Update(ctx context.Context, fn func(*syncstate.State) error) error
}

var _ Memory = (*syncstate.Store)(nil)

// Notification says a newer version is available, or that the team's
// model settings changed what the installed version would write.
type Notification struct {
	ID      pack.ID
	Current semver.Version
	Latest  semver.Version
	Remote  string
	// ModelsChanged is set when only the model settings changed; Latest
	// is then Current.
	ModelsChanged bool
	// Template is the template release a conductor would be rendered from
	// ("acme/dev-loop@1.1.0") when only that is new; Latest is then Current.
	Template string
	// Held lists the files that keep the update out (slash paths, relative
	// to the scope root); empty when nothing is in the way.
	Held []string
}

// Notifier tells a person about updates that are available or held back.
// sync calls it once per version of an install, not on every run.
type Notifier interface {
	Notify(ctx context.Context, n Notification) error
}

// Outcome is what sync did with one install.
type Outcome string

// Sync outcomes.
const (
	OutcomeUpToDate  Outcome = "up to date"
	OutcomeUpdated   Outcome = "updated"
	OutcomeAvailable Outcome = "update available"
	OutcomePinned    Outcome = "pinned"
	// OutcomeBlocked means an auto update, or the team's new model
	// settings, could not be written because files are in the way:
	// usually the person's own edits, which loadout never overwrites.
	OutcomeBlocked Outcome = "held back by your edits"
	// OutcomeShadowed means a closer layer's package owns every file of
	// this install, so its updates change nothing and are not applied.
	OutcomeShadowed Outcome = "shadowed by a closer layer"
	OutcomeFailed   Outcome = "failed"
	// OutcomeGone means the registry deleted the package. The install is
	// left as it is, files included: they may hold the person's own work,
	// and removing what nobody asked to remove is never sync's job.
	OutcomeGone Outcome = "removed from the registry"
	// OutcomeRerendered means the same version was written again because
	// the team's model table changed what it renders (policy auto).
	OutcomeRerendered Outcome = "model settings applied"
	// OutcomeModelsChanged reports such a change without applying it
	// (policy notify); installing the package again applies it.
	OutcomeModelsChanged Outcome = "model settings changed"
	// OutcomeTemplateUpdated means a conductor was rendered again from a
	// newer release of its template (policy auto).
	OutcomeTemplateUpdated Outcome = "pipeline template updated"
	// OutcomeTemplateChanged reports such a release without applying it
	// (policy notify); installing the binding again applies it.
	OutcomeTemplateChanged Outcome = "pipeline template changed"
)

// Result is the outcome for one install.
type Result struct {
	Entry   lock.Entry
	Latest  semver.Version
	Outcome Outcome
	// Models is the digest of the team's model table, for the model
	// outcomes; empty otherwise.
	Models string
	// Template is the template release a conductor is behind or was
	// rendered from again, for the template outcomes; empty otherwise.
	Template string
	// Notes are lines to print after the table: a newer template major
	// exists, or the pinned major is gone from the registry.
	Notes []string
	// Held lists the files that keep the update out: every conflict for
	// OutcomeBlocked, and the edited files of a notify install whose
	// update (OutcomeAvailable, OutcomeModelsChanged) an install would
	// refuse.
	Held []syncstate.Blocker
	// Err explains OutcomeFailed and OutcomeBlocked.
	Err error
	// remote is the resolved remote, for the notification.
	remote string
}

// Behind reports whether the install is behind its registry: an update or
// new model settings are out and not applied.
func (r Result) Behind() bool {
	switch r.Outcome {
	case OutcomeAvailable, OutcomeBlocked, OutcomeModelsChanged, OutcomeTemplateChanged:
		return true
	}
	return false
}

// ModelsOnly reports whether only the team's model settings are new.
func (r Result) ModelsOnly() bool { return r.Latest.Compare(r.Entry.Version) == 0 && r.Template == "" }

// TemplateOnly reports whether only the template of a conductor is new.
func (r Result) TemplateOnly() bool {
	return r.Latest.Compare(r.Entry.Version) == 0 && r.Template != ""
}

// HeldPaths returns the paths of the files in the way.
func (r Result) HeldPaths() []string {
	out := make([]string, len(r.Held))
	for i, h := range r.Held {
		out[i] = h.Path
	}
	return out
}

// Service runs syncs.
type Service struct {
	installer Installer
	connect   Connector
	notifier  Notifier
	memory    Memory
	log       *slog.Logger
	now       func() time.Time
}

// New returns an update service. memory remembers what each sync learned,
// so the notifier hears of each version once and status can say it later.
// A nil notifier means no channel is set up: nothing is marked told, so a
// channel set up later still hears of pending updates.
func New(installer Installer, connect Connector, notifier Notifier, memory Memory, log *slog.Logger) *Service {
	return &Service{installer: installer, connect: connect, notifier: notifier, memory: memory, log: log, now: time.Now}
}

// Sync checks every install that came from a registry. A failure for one
// install is reported in its Result and does not stop the others. After
// the checks it tells the notifier what is new since the last sync and
// records what it learned.
func (s *Service) Sync(ctx context.Context) ([]Result, error) {
	results, err := s.check(ctx)
	if err != nil {
		return results, err
	}
	prev, err := s.memory.Load()
	if err != nil {
		// Without the record every update is news again: telling twice
		// beats telling never.
		s.log.Warn("could not read what the last sync learned", "err", err)
		prev = syncstate.State{}
	}
	records := s.tell(ctx, &prev, results)
	s.remember(ctx, results, records)
	return results, nil
}

func (s *Service) check(ctx context.Context) ([]Result, error) {
	entries, err := s.installer.Entries(ctx)
	if err != nil {
		return nil, err
	}
	type key struct{ namespace, remote string }
	sources := map[key]source{}
	var results []Result
	for _, e := range entries {
		if e.Remote == "" {
			continue // installed from a folder; nothing to sync against
		}
		if err := ctx.Err(); err != nil {
			return results, err
		}
		k := key{namespace: e.ID.Team(), remote: e.Remote}
		src, ok := sources[k]
		if !ok {
			src = s.open(ctx, e)
			sources[k] = src
		}
		if src.err != nil {
			results = append(results, Result{Entry: e, Outcome: OutcomeFailed, Err: src.err})
			continue
		}
		results = append(results, s.syncOne(ctx, src, e))
	}
	return results, nil
}

// source is an opened Source with the model table of its team, fetched
// once for all its installs.
type source struct {
	Source
	models tier.Table
	err    error
}

func (s *Service) open(ctx context.Context, e lock.Entry) source {
	src, err := s.connect(e)
	if err != nil {
		return source{err: err}
	}
	// Without the table an update would write the built-in models over the
	// team's, so a failure here fails the source's installs.
	models, err := src.Registry.Tiers(ctx)
	if err != nil {
		return source{err: fmt.Errorf("model settings of %s: %w", e.ID.Team(), err)}
	}
	return source{Source: src, models: models}
}

func (s *Service) syncOne(ctx context.Context, src source, e lock.Entry) Result {
	rels, err := src.Registry.Releases(ctx, e.ID)
	if errors.Is(err, ErrGone) {
		s.log.Info("sync checked", "id", e.ID, "scope", e.Scope, "current", e.Version, "outcome", OutcomeGone)
		return Result{Entry: e, Outcome: OutcomeGone, remote: cmp.Or(src.Remote, e.Remote)}
	}
	if err != nil {
		return s.failed(e, fmt.Errorf("check %s: %w", e.ID, err))
	}
	latest, kind, ok := newest(rels)
	if !ok {
		return s.failed(e, fmt.Errorf("check %s: %s has no releases", e.ID, e.ID))
	}
	var tpl template
	if kind.HasNeeds() {
		if tpl, err = s.checkTemplate(ctx, src, e); err != nil {
			return s.failed(e, err)
		}
	}
	res := Result{Entry: e, Latest: latest}
	if latest.Compare(e.Version) <= 0 {
		res = s.checkRender(ctx, src, e, tpl)
		res.remote = cmp.Or(src.Remote, e.Remote)
		res.Notes = tpl.notes
		s.log.Info("sync checked", "id", e.ID, "scope", e.Scope, "current", e.Version, "models", src.models.Digest(), "template", res.Template, "outcome", res.Outcome, "held", len(res.Held))
		return res
	}

	switch {
	case e.FullyShadowed():
		res.Outcome = OutcomeShadowed
	case e.Policy == lock.PolicyPin:
		res.Outcome = OutcomePinned
	case e.Policy == lock.PolicyAuto:
		res = s.apply(ctx, src, e, latest)
	default: // notify, and anything unexpected: never change files silently
		res.Outcome = OutcomeAvailable
		res.Held = s.edited(ctx, e)
	}
	res.remote = cmp.Or(src.Remote, e.Remote)
	if res.Outcome != OutcomeFailed {
		res.Notes = tpl.notes
	}
	s.log.Info("sync checked", "id", e.ID, "scope", e.Scope, "current", e.Version, "latest", latest, "outcome", res.Outcome, "held", len(res.Held))
	return res
}

// newest returns the highest version in rels and its kind.
func newest(rels []Release) (semver.Version, pack.Kind, bool) {
	var best Release
	for i, r := range rels {
		if i == 0 || r.Version.Compare(best.Version) > 0 {
			best = r
		}
	}
	return best.Version, best.Kind, len(rels) > 0
}

// edited returns the files of a notify install the person edited, which
// would hold the update back when they install it. It reads only the
// files on disk; a failure only costs the warning.
func (s *Service) edited(ctx context.Context, e lock.Entry) []syncstate.Blocker {
	paths, err := s.installer.Edited(ctx, e, nil)
	if err != nil {
		s.log.Warn("could not check for edited files", "id", e.ID, "err", err)
		return nil
	}
	out := make([]syncstate.Blocker, len(paths))
	for i, p := range paths {
		out[i] = syncstate.Blocker{Path: p, Reason: string(install.ReasonModified)}
	}
	return out
}

// checkRender handles an install that is up to date: when the team's model
// table or the template a conductor renders from changed since it was
// written, auto writes it again, notify says so, and pin leaves it. A
// change that does not touch the package's files (a skill, an agent
// without a hint) is only recorded.
func (s *Service) checkRender(ctx context.Context, src source, e lock.Entry, tpl template) Result {
	res := Result{Entry: e, Latest: e.Version, Outcome: OutcomeUpToDate}
	models := e.Models != src.models.Digest()
	if (!models && !tpl.changed()) || e.FullyShadowed() || e.Policy == lock.PolicyPin {
		return res
	}
	if models {
		res.Models = src.models.Digest()
	}
	res.Template = tpl.release
	p, err := src.Registry.Fetch(ctx, e.ID, e.Version)
	if err != nil {
		return s.failed(e, fmt.Errorf("download %s@%s: %w", e.ID, e.Version, err))
	}
	opts, err := s.options(ctx, src, e, p)
	if err != nil {
		return s.failed(e, err)
	}
	if e.Policy != lock.PolicyAuto {
		changed, err := s.installer.Rerenders(ctx, p, opts)
		if err != nil {
			return s.failed(e, fmt.Errorf("render %s@%s: %w", e.ID, e.Version, err))
		}
		if changed {
			res.Outcome = OutcomeModelsChanged
			if res.Template != "" {
				res.Outcome = OutcomeTemplateChanged
			}
			res.Held = s.edited(ctx, e)
			return res
		}
	}
	r, err := s.installer.Install(ctx, p, opts)
	if ce, ok := errors.AsType[*install.ConflictError](err); ok {
		// When no file would change (a skill, a knowledge base) the install
		// only records the table's digest: the edits hold nothing back.
		if changed, rerr := s.installer.Rerenders(ctx, p, opts); rerr == nil && !changed {
			return Result{Entry: e, Latest: e.Version, Outcome: OutcomeUpToDate}
		}
		return Result{Entry: e, Latest: e.Version, Outcome: OutcomeBlocked, Models: res.Models, Template: res.Template, Held: syncstate.Blockers(e.Scope.Root, ce), Err: err}
	}
	if err != nil {
		return s.failed(e, fmt.Errorf("install %s@%s: %w", e.ID, e.Version, err))
	}
	if !r.Unchanged {
		res.Outcome = OutcomeRerendered
		if res.Template != "" {
			res.Outcome = OutcomeTemplateUpdated
		}
	}
	return res
}

func (s *Service) apply(ctx context.Context, src source, e lock.Entry, latest semver.Version) Result {
	p, err := src.Registry.Fetch(ctx, e.ID, latest)
	if err != nil {
		return s.failed(e, fmt.Errorf("download %s@%s: %w", e.ID, latest, err))
	}
	opts, err := s.options(ctx, src, e, p)
	if err != nil {
		return s.failed(e, err)
	}
	_, err = s.installer.Install(ctx, p, opts)
	if ce, ok := errors.AsType[*install.ConflictError](err); ok {
		return Result{Entry: e, Latest: latest, Outcome: OutcomeBlocked, Held: syncstate.Blockers(e.Scope.Root, ce), Err: err}
	}
	if err != nil {
		return s.failed(e, fmt.Errorf("install %s@%s: %w", e.ID, latest, err))
	}
	return Result{Entry: e, Latest: latest, Outcome: OutcomeUpdated}
}

// options installs e again from src with the team's current models, and
// with the templates and agents p renders from. The same depth keeps layer
// precedence: files a closer layer owns stay shadowed and are never
// overwritten by an update.
func (s *Service) options(ctx context.Context, src source, e lock.Entry, p *pack.Pack) (install.Options, error) {
	opts := install.Options{
		Scope:   e.Scope,
		Targets: e.Targets(),
		Source:  cmp.Or(src.URL, e.Source),
		Remote:  cmp.Or(src.Remote, e.Remote),
		Policy:  e.Policy,
		Depth:   e.Depth,
		Models:  src.models,
	}
	if needs := p.Needs(); len(needs.Packages) > 0 || len(needs.Agents) > 0 {
		n, err := src.Registry.Needs(ctx, p)
		if err != nil {
			return opts, fmt.Errorf("resolve what %s@%s renders from: %w", e.ID, p.Version(), err)
		}
		opts.Requires, opts.Agents = n.Requires, n.Agents
	}
	return opts, nil
}

// template is what a conductor's install knows about the template it
// renders from.
type template struct {
	// release is the template release a new render would use
	// ("acme/dev-loop@1.1.0"), empty when the recorded one is current.
	release string
	notes   []string
}

func (t template) changed() bool { return t.release != "" }

// checkTemplate compares the template releases e was rendered from with the
// registry: a newer release in the pinned major makes the conductor stale
// (template.release), a newer major or a vanished pinned major is a note.
// An entry with no recorded requirements (its side file was missing or
// damaged) is stale too, so one sync renders it again.
func (s *Service) checkTemplate(ctx context.Context, src source, e lock.Entry) (template, error) {
	type pinned struct {
		id       pack.ID
		major    int
		recorded semver.Version
		known    bool
	}
	var pins []pinned
	for _, r := range e.Requires {
		pins = append(pins, pinned{r.ID, r.Version.Major(), r.Version, true})
	}
	if len(pins) == 0 {
		p, err := src.Registry.Fetch(ctx, e.ID, e.Version)
		if err != nil {
			return template{}, fmt.Errorf("download %s@%s: %w", e.ID, e.Version, err)
		}
		for _, r := range p.Needs().Packages {
			pins = append(pins, pinned{id: r.ID, major: r.Major})
		}
	}
	var t template
	var fresh []string
	for _, pin := range pins {
		rels, err := src.Registry.Releases(ctx, pin.id)
		if err != nil && !errors.Is(err, api.ErrNotFound) {
			return t, fmt.Errorf("check %s: %w", pin.id, err)
		}
		var best semver.Version
		found, higher := false, semver.Version{}
		for _, r := range rels {
			switch {
			case r.Version.Major() == pin.major && (!found || r.Version.Compare(best) > 0):
				best, found = r.Version, true
			case r.Version.Major() > pin.major && r.Version.Compare(higher) > 0:
				higher = r.Version
			}
		}
		if higher.Major() > pin.major {
			t.notes = append(t.notes, fmt.Sprintf("%s %s is out. %s uses major %d. Move it with `uses: %s@%d` and publish.", pin.id, higher, e.ID, pin.major, pin.id, higher.Major()))
		}
		switch {
		case !found:
			t.notes = append(t.notes, fmt.Sprintf("%s uses %s@%d, which the registry no longer lists. The conductor you have keeps working. Point `uses:` at a major that exists and publish.", e.ID, pin.id, pin.major))
		case !pin.known || best.Compare(pin.recorded) > 0:
			fresh = append(fresh, fmt.Sprintf("%s@%s", pin.id, best))
		}
	}
	t.release = strings.Join(fresh, ", ")
	return t, nil
}

// failed records a failure. It is returned to the caller, who shows it, so
// it is only logged at debug level (never shown twice).
func (s *Service) failed(e lock.Entry, err error) Result {
	s.log.Debug("sync failed", "id", e.ID, "remote", e.Remote, "err", err)
	return Result{Entry: e, Outcome: OutcomeFailed, Err: err}
}

// tell notifies about every install that is behind and whose news the
// notifier has not had: a first sight, a newer release, an update that
// became held back (or free again), or a notification that failed last
// time. It returns the records to keep, one per install behind.
func (s *Service) tell(ctx context.Context, prev *syncstate.State, results []Result) map[syncstate.Key]syncstate.Behind {
	now := s.now()
	out := map[syncstate.Key]syncstate.Behind{}
	for _, r := range results {
		if !r.Behind() {
			continue
		}
		b := syncstate.Behind{
			ID: r.Entry.ID, Scope: r.Entry.Scope, Version: r.Entry.Version, Latest: r.Latest,
			Models: r.Models, Template: r.Template, Held: r.Held, Since: now, Checked: now,
		}
		old, had := prev.Find(b.Key())
		same := had && old.Latest.Compare(b.Latest) == 0 && old.Models == b.Models && old.Template == b.Template
		if same {
			b.Since = old.Since
		}
		if same && old.Told && old.IsHeld() == b.IsHeld() {
			b.Told = true
		} else {
			b.Told = s.notify(ctx, r)
		}
		out[b.Key()] = b
	}
	return out
}

// notify tells the notifier about r and reports whether it got through. A
// failure only warns; the next sync tries again.
func (s *Service) notify(ctx context.Context, r Result) bool {
	n := Notification{
		ID: r.Entry.ID, Current: r.Entry.Version, Latest: r.Latest, Remote: cmp.Or(r.remote, r.Entry.Remote),
		ModelsChanged: r.ModelsOnly(), Template: r.Template, Held: r.HeldPaths(),
	}
	if s.notifier == nil {
		return false // no channel: the news stays pending for one set up later
	}
	if err := s.notifier.Notify(ctx, n); err != nil {
		s.log.Warn("notification failed", "id", r.Entry.ID, "err", err)
		return false
	}
	return true
}

// remember records what this sync learned, in one write: installs behind
// get their record, installs it found in step lose theirs, and a failed
// check keeps the old one, so a registry outage forgets nothing. A failed
// write only warns: the sync itself went through.
func (s *Service) remember(ctx context.Context, results []Result, records map[syncstate.Key]syncstate.Behind) {
	err := s.memory.Update(ctx, func(st *syncstate.State) error {
		for _, r := range results {
			k := syncstate.Key{ID: r.Entry.ID, Scope: r.Entry.Scope}
			switch {
			case r.Outcome == OutcomeFailed:
			case r.Behind():
				st.Put(records[k])
			default:
				st.Drop(k)
			}
		}
		return nil
	})
	if err != nil {
		s.log.Warn("could not record what sync learned; status and the session hooks miss it until the next sync", "err", err)
	}
}
