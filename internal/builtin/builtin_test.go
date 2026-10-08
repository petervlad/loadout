package builtin

import (
	"bytes"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/petervlad/loadout/internal/detect"
	"github.com/petervlad/loadout/internal/install"
	"github.com/petervlad/loadout/internal/lock"
	"github.com/petervlad/loadout/internal/pack"
	"github.com/petervlad/loadout/internal/semver"
	"github.com/petervlad/loadout/internal/syncstate"
	"github.com/petervlad/loadout/internal/target"
	"github.com/petervlad/loadout/internal/target/claudecode"
	"github.com/petervlad/loadout/internal/target/copilot"
)

var allSkills = []string{"loadout-setup", "loadout-author", "loadout-review", "loadout-admin"}

func TestPacks(t *testing.T) {
	packs, err := Packs("v1.4.0")
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, p := range packs {
		names = append(names, p.ID().Name())
		if p.ID().Team() != Namespace || p.Kind() != pack.KindSkill || p.Version() != semver.New(1, 4, 0) {
			t.Errorf("%s: kind %s, version %s; want a loadout skill at 1.4.0", p.ID(), p.Kind(), p.Version())
		}
		s, _ := p.Skill()
		if s.Description != p.Manifest().Description {
			t.Errorf("%s: manifest description differs from the skill's", p.ID())
		}
		// Every skill carries the guardrails and points at the local skill.
		for _, want := range []string{"credentials.yaml", "--force", "--safe", "full access", "loadout login", "never calls a model", "loadout-local", "<project>-loadout", "Ask"} {
			if !strings.Contains(s.Body, want) {
				t.Errorf("%s does not mention %q", p.ID(), want)
			}
		}
	}
	if diff := cmp.Diff(allSkills, names); diff != "" {
		t.Errorf("skills (-want +got):\n%s", diff)
	}
}

// emDash is U+2014, written as a number so the file itself has none.
const emDash = rune(0x2014)

// TestNoEmDashes: the writing rules hold inside the skills too, in every
// set.
func TestNoEmDashes(t *testing.T) {
	for _, set := range Sets() {
		names, err := set.Names()
		if err != nil {
			t.Fatal(err)
		}
		for _, name := range names {
			files, err := set.Files(name)
			if err != nil {
				t.Fatal(err)
			}
			for path, data := range files {
				if bytes.ContainsRune(data, emDash) {
					t.Errorf("%s/%s has an em-dash", name, path)
				}
			}
		}
	}
}

func TestVersion(t *testing.T) {
	tests := map[string]string{
		"1.4.0":                                "1.4.0",
		"v2.0.1":                               "2.0.1",
		"v1.4.0-3-gabc12":                      "1.4.0",
		"1.4.0+meta":                           "1.4.0",
		"dev":                                  "0.0.1",
		"e6f9bbc":                              "0.0.1",
		"0.0.0":                                "0.0.1",
		"v1.4.1-0.20261004192155-b42ddb9b6aba": "1.4.0",
		"v0.0.0-20261004192155-b42ddb9b6aba":   "0.0.1",
	}
	for in, want := range tests {
		if got := Version(in).String(); got != want {
			t.Errorf("Version(%q) = %s, want %s", in, got, want)
		}
	}
}

// machine is a person's home with loadout's installer, as the CLI wires it.
type machine struct {
	t         *testing.T
	home      string
	state     string
	installer *install.Installer
}

func newMachine(t *testing.T, tools ...string) *machine {
	t.Helper()
	home := t.TempDir()
	for _, dir := range tools {
		if err := os.Mkdir(filepath.Join(home, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	log := slog.New(slog.DiscardHandler)
	locks := lock.NewStore(filepath.Join(home, ".loadout", "lock.yaml"), home)
	return &machine{
		t: t, home: home, state: filepath.Join(home, ".loadout", "builtin.yaml"),
		installer: install.New(locks, log, claudecode.New(), copilot.New()),
	}
}

// service returns the service of a CLI at version.
func (m *machine) service(version string) *Service {
	m.t.Helper()
	s, err := New(m.installer, detect.New(claudecode.New(), copilot.New()), Options{Version: version, StatePath: m.state, Home: m.home, Memory: m.memory(), Log: slog.New(slog.DiscardHandler)})
	if err != nil {
		m.t.Fatal(err)
	}
	return s
}

// memory is the machine's sync state.
func (m *machine) memory() *syncstate.Store {
	return syncstate.NewStore(filepath.Join(m.home, ".loadout", "syncstate.yaml"), m.home)
}

// held returns the recorded held-back skills, by name, with the version
// held back.
func (m *machine) held() map[string]string {
	m.t.Helper()
	st, err := m.memory().Load()
	if err != nil {
		m.t.Fatal(err)
	}
	out := map[string]string{}
	for _, b := range st.All() {
		out[b.ID.Name()] = b.Latest.String() + " " + strings.Join(b.HeldPaths(), ",")
	}
	return out
}

func (m *machine) skillFile(tool, name string) string {
	return filepath.Join(m.home, tool, "skills", name, "SKILL.md")
}

func (m *machine) exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// entry returns the lock entry of the built-in skill name.
func (m *machine) entry(name string) lock.Entry {
	m.t.Helper()
	entries, err := m.installer.Entries(m.t.Context())
	if err != nil {
		m.t.Fatal(err)
	}
	i := slices.IndexFunc(entries, func(e lock.Entry) bool { return e.ID.String() == Namespace+"/"+name })
	if i < 0 {
		m.t.Fatalf("%s is not installed", name)
	}
	return entries[i]
}

func actions(r Report) map[string]Action {
	out := map[string]Action{}
	for _, o := range r.Skills {
		out[o.Name] = o.Action
	}
	return out
}

func every(a Action) map[string]Action {
	out := map[string]Action{}
	for _, n := range allSkills {
		out[n] = a
	}
	return out
}

func TestEnsureNeedsTheWish(t *testing.T) {
	m := newMachine(t, ".claude")
	rep, err := m.service("1.0.0").Ensure(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Skills) != 0 || m.exists(m.skillFile(".claude", "loadout-setup")) {
		t.Errorf("Ensure without Want installed %v", rep.Skills)
	}
}

func TestEnsureInstallsUpdatesAndKeepsEdits(t *testing.T) {
	m := newMachine(t, ".claude", ".copilot")
	s := m.service("1.0.0")
	if err := s.Want(t.Context()); err != nil {
		t.Fatal(err)
	}
	rep, err := s.Ensure(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff(every(ActionInstalled), actions(rep)); diff != "" {
		t.Errorf("first ensure (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff([]string{"claude", "copilot"}, rep.Targets); diff != "" {
		t.Errorf("targets (-want +got):\n%s", diff)
	}
	for _, tool := range []string{".claude", ".copilot"} {
		if data, err := os.ReadFile(m.skillFile(tool, "loadout-setup")); err != nil || !strings.Contains(string(data), "managed by loadout: loadout/loadout-setup@1.0.0") {
			t.Errorf("%s skill = %q, %v", tool, data, err)
		}
	}
	e := m.entry("loadout-setup")
	if e.Source != Source || e.Remote != "" || e.Policy != lock.PolicyAuto {
		t.Errorf("lock entry = source %q, remote %q, policy %s; want builtin, none, auto", e.Source, e.Remote, e.Policy)
	}

	// The same CLI again: nothing to do, and an edit does not nag.
	edited := m.skillFile(".claude", "loadout-review")
	if err := os.WriteFile(edited, []byte("my own notes\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if rep, err = s.Ensure(t.Context()); err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff(every(ActionUnchanged), actions(rep)); diff != "" {
		t.Errorf("same version (-want +got):\n%s", diff)
	}

	// A new CLI updates, except the edited skill, which is kept.
	if rep, err = m.service("1.1.0").Ensure(t.Context()); err != nil {
		t.Fatal(err)
	}
	want := every(ActionUpdated)
	want["loadout-review"] = ActionKept
	if diff := cmp.Diff(want, actions(rep)); diff != "" {
		t.Errorf("new version (-want +got):\n%s", diff)
	}
	if data, _ := os.ReadFile(edited); string(data) != "my own notes\n" {
		t.Errorf("edited skill was overwritten: %q", data)
	}
	if e := m.entry("loadout-setup"); e.Version != semver.New(1, 1, 0) {
		t.Errorf("loadout-setup at %s, want 1.1.0", e.Version)
	}
	// The kept skill is remembered as held back, for status and the hooks.
	if diff := cmp.Diff(map[string]string{"loadout-review": "1.1.0 .claude/skills/loadout-review/SKILL.md"}, m.held()); diff != "" {
		t.Errorf("held records (-want +got):\n%s", diff)
	}
	// Replacing the edit forgets it.
	if _, err := m.service("1.1.0").Setup(t.Context(), nil, true); err != nil {
		t.Fatal(err)
	}
	if held := m.held(); len(held) != 0 {
		t.Errorf("held records after setup --force: %v", held)
	}
}

func TestEnsureWithoutAToolWritesNothing(t *testing.T) {
	m := newMachine(t)
	s := m.service("1.0.0")
	if err := s.Want(t.Context()); err != nil {
		t.Fatal(err)
	}
	rep, err := s.Ensure(t.Context())
	if err != nil || !rep.NoTool {
		t.Fatalf("Ensure = %+v, %v; want NoTool", rep, err)
	}
	for _, dir := range []string{".claude", ".copilot"} {
		if m.exists(filepath.Join(m.home, dir)) {
			t.Errorf("%s was created", dir)
		}
	}

	// Copilot appears later: the recorded wish installs the skills there.
	if err := os.Mkdir(filepath.Join(m.home, ".copilot"), 0o755); err != nil {
		t.Fatal(err)
	}
	if rep, err = s.Ensure(t.Context()); err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff(every(ActionInstalled), actions(rep)); diff != "" {
		t.Errorf("after copilot appeared (-want +got):\n%s", diff)
	}
	if !m.exists(m.skillFile(".copilot", "loadout-admin")) || m.exists(filepath.Join(m.home, ".claude")) {
		t.Error("want the skills in ~/.copilot only")
	}

	// Then Claude Code: the skills are added for it too.
	if err := os.Mkdir(filepath.Join(m.home, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	if rep, err = s.Ensure(t.Context()); err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff(every(ActionUpdated), actions(rep)); diff != "" || !m.exists(m.skillFile(".claude", "loadout-admin")) {
		t.Errorf("after claude appeared (-want +got):\n%s", diff)
	}
}

func TestDeclineAndSetup(t *testing.T) {
	m := newMachine(t, ".claude")
	s := m.service("1.0.0")
	if err := s.Want(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Ensure(t.Context()); err != nil {
		t.Fatal(err)
	}
	id, _ := pack.NewID(Namespace, "loadout-review")
	if _, err := m.installer.Uninstall(t.Context(), id, "", false); err != nil {
		t.Fatal(err)
	}
	if err := s.Decline(t.Context(), id); err != nil {
		t.Fatal(err)
	}
	other, _ := pack.NewID("lime", "loadout-review")
	if err := s.Decline(t.Context(), other); err != nil || s.IsBuiltin(other) {
		t.Errorf("a registry package is not built in: %v", err)
	}

	// A newer CLI does not bring the declined skill back.
	rep, err := m.service("1.1.0").Ensure(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if got := actions(rep)["loadout-review"]; got != ActionDeclined || m.exists(m.skillFile(".claude", "loadout-review")) {
		t.Errorf("declined skill: %s, on disk %v", got, m.exists(m.skillFile(".claude", "loadout-review")))
	}

	// setup --skills does.
	if rep, err = s.Setup(t.Context(), nil, false); err != nil {
		t.Fatal(err)
	}
	if got := actions(rep)["loadout-review"]; got != ActionInstalled || !m.exists(m.skillFile(".claude", "loadout-review")) {
		t.Errorf("after setup: %s", got)
	}
}

func TestSetupWithATargetCreatesItsFolder(t *testing.T) {
	m := newMachine(t)
	s := m.service("1.0.0")
	rep, err := s.Setup(t.Context(), nil, false)
	if err != nil || !rep.NoTool {
		t.Fatalf("Setup without a tool = %+v, %v; want NoTool", rep, err)
	}
	if rep, err = s.Setup(t.Context(), []string{"claude"}, false); err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff(every(ActionInstalled), actions(rep)); diff != "" || !m.exists(m.skillFile(".claude", "loadout-setup")) {
		t.Errorf("setup --target claude (-want +got):\n%s", diff)
	}
	if _, err := s.Setup(t.Context(), []string{"cursor"}, false); err == nil {
		t.Error("an unknown target was accepted")
	}
}

func TestSetupForceReplacesAnEdit(t *testing.T) {
	m := newMachine(t, ".claude")
	s := m.service("1.0.0")
	if _, err := s.Setup(t.Context(), nil, false); err != nil {
		t.Fatal(err)
	}
	edited := m.skillFile(".claude", "loadout-author")
	if err := os.WriteFile(edited, []byte("mine\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	rep, err := s.Setup(t.Context(), nil, false)
	if err != nil {
		t.Fatal(err)
	}
	if o := rep.Skills[slices.IndexFunc(rep.Skills, func(o Outcome) bool { return o.Name == "loadout-author" })]; o.Action != ActionKept || o.Err == nil {
		t.Errorf("setup over an edit = %s, %v; want kept with the conflict", o.Action, o.Err)
	}
	if _, err := s.Setup(t.Context(), nil, true); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(edited); string(data) == "mine\n" {
		t.Error("--force kept the edit")
	}
}

func TestPinnedAndOtherPackagesAreLeftAlone(t *testing.T) {
	m := newMachine(t, ".claude")
	s := m.service("1.0.0")
	if _, err := s.Setup(t.Context(), nil, false); err != nil {
		t.Fatal(err)
	}
	setup, _ := pack.NewID(Namespace, "loadout-setup")
	if _, err := m.installer.SetPolicy(t.Context(), setup, "", lock.PolicyPin); err != nil {
		t.Fatal(err)
	}
	// A registry team called loadout installs a package with the same id.
	author, _ := pack.NewID(Namespace, "loadout-author")
	p := m.service("1.0.0").Packs()[slices.IndexFunc(s.Packs(), func(p *pack.Pack) bool { return p.ID() == author })]
	if _, err := m.installer.Install(t.Context(), p, install.Options{Scope: target.Scope{Kind: target.ScopeUser, Root: m.home}, Targets: []string{"claude"}, Source: "https://registry.example.com", Remote: "office", Force: true}); err != nil {
		t.Fatal(err)
	}

	rep, err := m.service("2.0.0").Ensure(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	got := actions(rep)
	if got["loadout-setup"] != ActionPinned || got["loadout-author"] != ActionOther || got["loadout-admin"] != ActionUpdated {
		t.Errorf("actions = %v", got)
	}
	if e := m.entry("loadout-setup"); e.Version != semver.New(1, 0, 0) {
		t.Errorf("pinned skill moved to %s", e.Version)
	}
}

// TestEverySkillHasAnIntro: the Start here page presents every built-in
// skill in plain words, with at least one thing to ask, and no intro is
// left over from a skill that is gone.
func TestEverySkillHasAnIntro(t *testing.T) {
	names, err := Names()
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range names {
		i, ok := IntroOf(n)
		if !ok || i.Title == "" || i.Summary == "" || len(i.Asks) == 0 {
			t.Errorf("%s: intro %+v; want a title, a summary and something to ask", n, i)
		}
	}
	for n := range intros {
		if !slices.Contains(names, n) {
			t.Errorf("intro for %s, which is not a built-in skill", n)
		}
	}
}
