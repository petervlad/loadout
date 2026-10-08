package update

import (
	"context"
	"errors"
	"log/slog"
	"slices"
	"testing"

	"github.com/petervlad/loadout/internal/install"
	"github.com/petervlad/loadout/internal/lock"
	"github.com/petervlad/loadout/internal/pack"
	"github.com/petervlad/loadout/internal/semver"
	"github.com/petervlad/loadout/internal/syncstate"
	"github.com/petervlad/loadout/internal/target"
	"github.com/petervlad/loadout/internal/tier"
)

type fakeRegistry struct {
	latest   map[string]semver.Version
	fetchErr error
	models   tier.Table
	tierErr  error
	fetched  *[]string
}

func (r fakeRegistry) Tiers(context.Context) (tier.Table, error) { return r.models, r.tierErr }

func (r fakeRegistry) Latest(_ context.Context, id pack.ID) (semver.Version, error) {
	v, ok := r.latest[id.String()]
	if !ok {
		return semver.Version{}, errors.New("not found")
	}
	return v, nil
}

func (r fakeRegistry) Fetch(_ context.Context, id pack.ID, v semver.Version) (*pack.Pack, error) {
	if r.fetchErr != nil {
		return nil, r.fetchErr
	}
	if r.fetched != nil {
		*r.fetched = append(*r.fetched, id.String()+"@"+v.String())
	}
	return pack.FromFiles(map[string][]byte{
		pack.ManifestFile: []byte("name: " + id.String() + "\nkind: agent\nversion: " + v.String() + "\ndescription: d\n"),
		pack.AgentFile:    []byte("---\nname: " + id.Name() + "\ndescription: d\n---\nprompt\n"),
	})
}

type fakeInstaller struct {
	entries    []lock.Entry
	installed  []string
	opts       []install.Options
	installErr error
	// rerenders answers Rerenders; unchanged marks every install Unchanged.
	rerenders bool
	unchanged bool
	// edited answers Edited, by package id.
	edited map[string][]string
}

func (f *fakeInstaller) Edited(_ context.Context, e lock.Entry, _ []string) ([]string, error) {
	return f.edited[e.ID.String()], nil
}

// memMemory keeps the sync state in memory.
type memMemory struct {
	st        syncstate.State
	loadErr   error
	updateErr error
}

func (m *memMemory) Load() (syncstate.State, error) {
	if m.loadErr != nil {
		return syncstate.State{}, m.loadErr
	}
	var st syncstate.State
	for _, b := range m.st.All() {
		st.Put(b)
	}
	return st, nil
}

func (m *memMemory) Update(_ context.Context, fn func(*syncstate.State) error) error {
	if m.updateErr != nil {
		return m.updateErr
	}
	st, err := m.Load()
	if err != nil {
		st = syncstate.State{}
	}
	if err := fn(&st); err != nil {
		return err
	}
	m.st = st
	return nil
}

func (f *fakeInstaller) Rerenders(context.Context, *pack.Pack, install.Options) (bool, error) {
	return f.rerenders, nil
}

func (f *fakeInstaller) Entries(context.Context) ([]lock.Entry, error) { return f.entries, nil }

func (f *fakeInstaller) Install(_ context.Context, p *pack.Pack, opts install.Options) (install.Result, error) {
	if f.installErr != nil {
		return install.Result{}, f.installErr
	}
	f.opts = append(f.opts, opts)
	f.installed = append(f.installed, p.ID().String()+"@"+p.Version().String())
	return install.Result{Unchanged: f.unchanged}, nil
}

type recordingNotifier struct {
	got []Notification
	err error
}

func (n *recordingNotifier) Notify(_ context.Context, x Notification) error {
	n.got = append(n.got, x)
	return n.err
}

func entry(t *testing.T, name, version string, policy lock.Policy, remote string) lock.Entry {
	t.Helper()
	id, err := pack.NewID("lime", name)
	if err != nil {
		t.Fatal(err)
	}
	v, err := semver.Parse(version)
	if err != nil {
		t.Fatal(err)
	}
	return lock.Entry{
		ID: id, Version: v, Policy: policy, Remote: remote,
		Scope: target.Scope{Kind: target.ScopeUser, Root: "/home/a"},
		Files: []lock.FileRecord{{Target: "claude", Path: ".claude/agents/" + name + ".md"}},
	}
}

func TestSyncFollowsPolicy(t *testing.T) {
	inst := &fakeInstaller{entries: []lock.Entry{
		entry(t, "auto-agent", "1.0.0", lock.PolicyAuto, "office"),
		entry(t, "notify-agent", "1.0.0", lock.PolicyNotify, "office"),
		entry(t, "pinned-agent", "1.0.0", lock.PolicyPin, "office"),
		entry(t, "current-agent", "2.0.0", lock.PolicyAuto, "office"),
		entry(t, "local-agent", "1.0.0", lock.PolicyAuto, ""),
	}}
	reg := fakeRegistry{latest: map[string]semver.Version{
		"lime/auto-agent":    semver.New(1, 1, 0),
		"lime/notify-agent":  semver.New(1, 1, 0),
		"lime/pinned-agent":  semver.New(1, 1, 0),
		"lime/current-agent": semver.New(2, 0, 0),
	}}
	notes := &recordingNotifier{err: errors.New("pushover down")} // must not fail the sync
	svc := New(inst, func(lock.Entry) (Source, error) { return Source{Registry: reg}, nil }, notes, &memMemory{}, slog.New(slog.DiscardHandler))

	results, err := svc.Sync(t.Context())
	if err != nil {
		t.Fatalf("Sync() error: %v", err)
	}
	got := map[string]Outcome{}
	for _, r := range results {
		got[r.Entry.ID.Name()] = r.Outcome
	}
	want := map[string]Outcome{
		"auto-agent":    OutcomeUpdated,
		"notify-agent":  OutcomeAvailable,
		"pinned-agent":  OutcomePinned,
		"current-agent": OutcomeUpToDate,
	}
	for name, w := range want {
		if got[name] != w {
			t.Errorf("%s outcome = %q, want %q", name, got[name], w)
		}
	}
	if _, ok := got["local-agent"]; ok {
		t.Error("a folder install was synced")
	}
	if len(inst.installed) != 1 || inst.installed[0] != "lime/auto-agent@1.1.0" {
		t.Errorf("installed = %v, want only lime/auto-agent@1.1.0", inst.installed)
	}
	if len(notes.got) != 1 || notes.got[0].ID.Name() != "notify-agent" {
		t.Errorf("notifications = %+v, want one for notify-agent", notes.got)
	}
}

func TestSyncFailuresAreIsolated(t *testing.T) {
	tests := []struct {
		name        string
		installErr  error
		fetchErr    error
		connectErr  error
		wantOutcome Outcome
	}{
		{name: "edited file", installErr: &install.ConflictError{Conflicts: []install.Conflict{{Path: "/x", Reason: install.ReasonModified}}}, wantOutcome: OutcomeBlocked},
		{name: "download fails", fetchErr: errors.New("timeout"), wantOutcome: OutcomeFailed},
		{name: "remote gone", connectErr: errors.New("remote office does not exist"), wantOutcome: OutcomeFailed},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			inst := &fakeInstaller{
				entries:    []lock.Entry{entry(t, "a", "1.0.0", lock.PolicyAuto, "office"), entry(t, "b", "1.0.0", lock.PolicyAuto, "office")},
				installErr: tc.installErr,
			}
			reg := fakeRegistry{
				latest:   map[string]semver.Version{"lime/a": semver.New(2, 0, 0), "lime/b": semver.New(2, 0, 0)},
				fetchErr: tc.fetchErr,
			}
			connect := func(lock.Entry) (Source, error) {
				if tc.connectErr != nil {
					return Source{}, tc.connectErr
				}
				return Source{Registry: reg}, nil
			}
			svc := New(inst, connect, &recordingNotifier{}, &memMemory{}, slog.New(slog.DiscardHandler))
			results, err := svc.Sync(t.Context())
			if err != nil {
				t.Fatalf("Sync() error: %v", err)
			}
			if len(results) != 2 {
				t.Fatalf("got %d results, want 2 (one failure must not stop the rest)", len(results))
			}
			for _, r := range results {
				if r.Outcome != tc.wantOutcome || r.Err == nil {
					t.Errorf("%s: outcome %q err %v, want %q with an error", r.Entry.ID, r.Outcome, r.Err, tc.wantOutcome)
				}
			}
		})
	}
}

func TestSyncKeepsLayerPrecedence(t *testing.T) {
	by, err := pack.NewID("lime", "planner")
	if err != nil {
		t.Fatal(err)
	}
	shadow := func(e lock.Entry, keepFiles bool) lock.Entry {
		e.Depth = 1
		e.Shadowed = []lock.Shadow{{Target: "copilot", Path: ".copilot/agents/x.md", By: by}}
		if !keepFiles {
			e.Shadowed = append(e.Shadowed, lock.Shadow{Target: e.Files[0].Target, Path: e.Files[0].Path, By: by})
			e.Files = nil
		}
		return e
	}
	tests := []struct {
		name        string
		entry       lock.Entry
		wantOutcome Outcome
		wantInstall bool
	}{
		{"fully shadowed auto is left alone", shadow(entry(t, "a", "1.0.0", lock.PolicyAuto, "office"), false), OutcomeShadowed, false},
		{"fully shadowed notify does not notify", shadow(entry(t, "b", "1.0.0", lock.PolicyNotify, "office"), false), OutcomeShadowed, false},
		{"partly shadowed auto updates at its depth", shadow(entry(t, "c", "1.0.0", lock.PolicyAuto, "office"), true), OutcomeUpdated, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			inst := &fakeInstaller{entries: []lock.Entry{tc.entry}}
			reg := fakeRegistry{latest: map[string]semver.Version{tc.entry.ID.String(): semver.New(2, 0, 0)}}
			notes := &recordingNotifier{}
			results, err := New(inst, func(lock.Entry) (Source, error) { return Source{Registry: reg}, nil }, notes, &memMemory{}, slog.New(slog.DiscardHandler)).Sync(t.Context())
			if err != nil || len(results) != 1 {
				t.Fatalf("Sync() = %+v, %v; want one result", results, err)
			}
			if results[0].Outcome != tc.wantOutcome {
				t.Errorf("outcome = %q, want %q", results[0].Outcome, tc.wantOutcome)
			}
			if len(notes.got) != 0 {
				t.Errorf("notified %+v, want nothing", notes.got)
			}
			if got := len(inst.opts) == 1; got != tc.wantInstall {
				t.Fatalf("installed %v, want install: %v", inst.installed, tc.wantInstall)
			}
			if tc.wantInstall {
				o := inst.opts[0]
				if o.Depth != 1 || len(o.Targets) != 2 {
					t.Errorf("Install options = %+v, want depth 1 and both targets (shadowed included)", o)
				}
			}
		})
	}
}

func TestSyncConnectsPerTeamAndRecordsTheResolvedRemote(t *testing.T) {
	north, err := pack.NewID("north", "y")
	if err != nil {
		t.Fatal(err)
	}
	other := entry(t, "b", "1.0.0", lock.PolicyAuto, "office")
	other.ID = north
	inst := &fakeInstaller{entries: []lock.Entry{
		entry(t, "a", "1.0.0", lock.PolicyAuto, "office"),
		entry(t, "c", "1.0.0", lock.PolicyAuto, "office"),
		other,
	}}
	reg := fakeRegistry{latest: map[string]semver.Version{
		"lime/a": semver.New(1, 1, 0), "lime/c": semver.New(1, 1, 0), "north/y": semver.New(1, 1, 0),
	}}
	var calls []string
	connect := func(e lock.Entry) (Source, error) {
		calls = append(calls, e.ID.Team())
		return Source{Registry: reg, Remote: e.ID.Team() + "-reg", URL: "https://" + e.ID.Team()}, nil
	}
	results, err := New(inst, connect, &recordingNotifier{}, &memMemory{}, slog.New(slog.DiscardHandler)).Sync(t.Context())
	if err != nil {
		t.Fatalf("Sync() error: %v", err)
	}
	if len(results) != 3 {
		t.Fatalf("got %d results, want 3", len(results))
	}
	if want := []string{"lime", "north"}; !slices.Equal(calls, want) {
		t.Errorf("connected for %v, want once per team %v", calls, want)
	}
	for i, o := range inst.opts {
		team := inst.entries[i].ID.Team()
		if o.Remote != team+"-reg" || o.Source != "https://"+team {
			t.Errorf("install %d recorded remote %q source %q, want the resolved %s-reg", i, o.Remote, o.Source, team)
		}
	}
}
