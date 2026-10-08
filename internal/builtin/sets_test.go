package builtin

import (
	"bytes"
	"io/fs"
	"maps"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/petervlad/loadout/deploy"
	"github.com/petervlad/loadout/internal/pack"
)

var hostSkills = []string{"loadout-host-setup", "loadout-host-provision"}

func TestHostPacks(t *testing.T) {
	set, ok := SetNamed(SetHost)
	if !ok || set.Auto {
		t.Fatalf("host set = %+v, %v; want a set that is not Auto", set, ok)
	}
	packs, err := PacksOf(set, "v1.4.0")
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, p := range packs {
		names = append(names, p.ID().Name())
		if p.ID().Team() != Namespace || p.Kind() != pack.KindSkill {
			t.Errorf("%s: kind %s; want a loadout skill", p.ID(), p.Kind())
		}
	}
	if diff := cmp.Diff(hostSkills, names); diff != "" {
		t.Errorf("host skills (-want +got):\n%s", diff)
	}
	// The pages for people with a registry list the everyday set only.
	every, _ := Names()
	if diff := cmp.Diff(allSkills, every); diff != "" {
		t.Errorf("Names (-want +got):\n%s", diff)
	}
	if _, ok := SetNamed("nope"); ok {
		t.Error("an unknown set was found")
	}
}

// TestHostAssetsAreTheDeployFiles: the deploy folder is the only copy of
// the files the provision skill ships.
func TestHostAssetsAreTheDeployFiles(t *testing.T) {
	files, err := host.Files("loadout-host-provision")
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	err = fs.WalkDir(deploy.FS, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		n++
		want, err := deploy.FS.ReadFile(p)
		if err != nil {
			return err
		}
		if got, ok := files["assets/"+p]; !ok || !bytes.Equal(got, want) {
			t.Errorf("assets/%s is missing or differs from deploy/%s", p, p)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for p := range files {
		if a, ok := strings.CutPrefix(p, "assets/"); ok {
			if _, err := deploy.FS.ReadFile(a); err != nil {
				t.Errorf("%s is not a deploy file", p)
			}
		}
	}
	setup, err := host.Files("loadout-host-setup")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := setup["assets/loadout.env.example"]; !ok || n < 10 {
		t.Errorf("setup assets = %v, %d deploy files", slices.Sorted(maps.Keys(setup)), n)
	}
}

func hostActions(r Report) map[string]Action {
	out := map[string]Action{}
	for _, o := range r.Skills {
		if o.Set == SetHost {
			out[o.Name] = o.Action
		}
	}
	return out
}

func everyHost(a Action) map[string]Action {
	return map[string]Action{hostSkills[0]: a, hostSkills[1]: a}
}

// TestHostSkillsAreNeverAddedBySync: login and sync install the everyday
// skills only; setup for the host set adds the host skills, sync then keeps
// them in step, and neither wish leaks into the other.
func TestHostSkillsAreNeverAddedBySync(t *testing.T) {
	m := newMachine(t, ".claude")
	s := m.service("1.0.0")
	if err := s.Want(t.Context()); err != nil {
		t.Fatal(err)
	}
	rep, err := s.Ensure(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff(every(ActionInstalled), actions(rep)); diff != "" {
		t.Errorf("ensure with the wish (-want +got):\n%s", diff)
	}
	if m.exists(m.skillFile(".claude", hostSkills[0])) {
		t.Fatal("ensure added a host skill")
	}

	// Asking for the host skills installs them, and only them.
	rep, err = s.SetupSets(t.Context(), []string{SetHost}, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff(everyHost(ActionInstalled), hostActions(rep)); diff != "" {
		t.Errorf("setup host (-want +got):\n%s", diff)
	}
	if e := m.entry(hostSkills[1]); e.Source != Source || e.Remote != "" {
		t.Errorf("host lock entry = %+v; want source builtin, no remote", e)
	}
	if _, err := os.Stat(m.skillFile(".claude", hostSkills[1])); err != nil {
		t.Error(err)
	}

	// A new CLI updates what is installed.
	rep, err = m.service("1.1.0").Ensure(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	want := every(ActionUpdated)
	maps.Copy(want, everyHost(ActionUpdated))
	if diff := cmp.Diff(want, actions(rep)); diff != "" {
		t.Errorf("new version (-want +got):\n%s", diff)
	}
	if e := m.entry(hostSkills[0]); e.Version.String() != "1.1.0" {
		t.Errorf("host skill at %s, want 1.1.0", e.Version)
	}

	// An uninstalled host skill is not added back by sync, and the other
	// is still updated. Asking again brings it back.
	id, _ := pack.NewID(Namespace, hostSkills[0])
	if _, err := m.installer.Uninstall(t.Context(), id, "", false); err != nil {
		t.Fatal(err)
	}
	if err := s.Decline(t.Context(), id); err != nil {
		t.Fatal(err)
	}
	rep, err = m.service("1.2.0").Ensure(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if got := hostActions(rep); !cmp.Equal(got, map[string]Action{hostSkills[1]: ActionUpdated}) {
		t.Errorf("after uninstall, host actions = %v", got)
	}
	rep, err = s.SetupSets(t.Context(), []string{SetHost}, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	if got := hostActions(rep)[hostSkills[0]]; got != ActionInstalled {
		t.Errorf("setup after uninstall = %s, want installed", got)
	}
}

// TestHostSetupAloneDoesNotWantTheEverydaySkills: a person who only asked
// for the host skills gets nothing else from sync, and sync keeps the host
// skills in step without the wish.
func TestHostSetupAloneDoesNotWantTheEverydaySkills(t *testing.T) {
	m := newMachine(t, ".claude")
	s := m.service("1.0.0")
	if _, err := s.SetupSets(t.Context(), []string{SetHost}, nil, false); err != nil {
		t.Fatal(err)
	}
	if m.exists(m.skillFile(".claude", "loadout-setup")) {
		t.Fatal("the host set installed an everyday skill")
	}
	rep, err := m.service("1.1.0").Ensure(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff(everyHost(ActionUpdated), actions(rep)); diff != "" {
		t.Errorf("ensure without the wish (-want +got):\n%s", diff)
	}
	if m.exists(m.skillFile(".claude", "loadout-setup")) {
		t.Error("ensure added an everyday skill nobody asked for")
	}
	// Setting up the everyday skills later leaves the host ones alone.
	rep, err = m.service("1.1.0").Setup(t.Context(), nil, false)
	if err != nil {
		t.Fatal(err)
	}
	if got := hostActions(rep); !cmp.Equal(got, everyHost(ActionUnchanged)) {
		t.Errorf("setup --skills touched the host skills: %v", got)
	}
}

func TestSetupSetsRefusesAnUnknownSet(t *testing.T) {
	m := newMachine(t, ".claude")
	if _, err := m.service("1.0.0").SetupSets(t.Context(), []string{"nope"}, nil, false); err == nil {
		t.Error("an unknown set was accepted")
	}
}

// TestHostSkillEditIsKeptAndForced: an edit to a host skill is kept, and
// only --force on a request for its set replaces it.
func TestHostSkillEditIsKeptAndForced(t *testing.T) {
	m := newMachine(t, ".claude")
	s := m.service("1.0.0")
	if _, err := s.SetupSets(t.Context(), []string{SetHost}, nil, false); err != nil {
		t.Fatal(err)
	}
	edited := m.skillFile(".claude", hostSkills[0])
	if err := os.WriteFile(edited, []byte("mine\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Forcing the everyday set does not reach the host skill.
	if _, err := s.SetupSets(t.Context(), []string{SetEveryday}, nil, true); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(edited); string(data) != "mine\n" {
		t.Error("--skills --force replaced a host skill")
	}
	rep, err := s.SetupSets(t.Context(), []string{SetHost}, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	if got := hostActions(rep)[hostSkills[0]]; got != ActionKept {
		t.Errorf("host edit = %s, want kept", got)
	}
	if _, err := s.SetupSets(t.Context(), []string{SetHost}, nil, true); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(edited); string(data) == "mine\n" {
		t.Error("--host-skills --force kept the edit")
	}
}
