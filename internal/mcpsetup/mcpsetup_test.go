package mcpsetup

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/petervlad/loadout/internal/target"
	"github.com/petervlad/loadout/internal/target/claudecode"
	"github.com/petervlad/loadout/internal/target/copilot"
)

const personMCP = "{\n  \"mcpServers\": {\n    \"github\": {\n      \"type\": \"http\",\n      \"url\": \"https://api.githubcopilot.com/mcp/\"\n    }\n  },\n  \"ratio\": 1.50e3\n}\n"

type machine struct {
	t    *testing.T
	home string
}

// newMachine returns a temporary home with the given tool folders.
func newMachine(t *testing.T, dirs ...string) machine {
	t.Helper()
	home := t.TempDir()
	for _, d := range dirs {
		if err := os.MkdirAll(filepath.Join(home, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return machine{t: t, home: home}
}

func (m machine) path(rel string) string { return filepath.Join(m.home, filepath.FromSlash(rel)) }

func (m machine) write(rel, data string) {
	m.t.Helper()
	if err := os.WriteFile(m.path(rel), []byte(data), 0o600); err != nil {
		m.t.Fatal(err)
	}
}

func (m machine) read(rel string) string {
	m.t.Helper()
	data, err := os.ReadFile(m.path(rel))
	if err != nil {
		m.t.Fatal(err)
	}
	return string(data)
}

func (m machine) exists(rel string) bool {
	_, err := os.Lstat(m.path(rel))
	return err == nil
}

// fakeClaude stands in for the claude program: it keeps user-scope servers
// in ~/.claude.json the way Claude Code does, other keys and all, and
// records every call. The real program never runs in a test.
type fakeClaude struct {
	m     machine
	calls [][]string
	fail  error
	// failAdd, when set, fails the next add-json call once.
	failAdd bool
}

func (f *fakeClaude) run(_ context.Context, name string, args ...string) ([]byte, error) {
	f.calls = append(f.calls, append([]string{name}, args...))
	if f.fail != nil {
		return []byte("boom"), f.fail
	}
	if f.failAdd && args[1] == "add-json" {
		f.failAdd = false
		return []byte("boom"), errors.New("exit status 1")
	}
	doc := map[string]any{"numStartups": 7}
	if data, err := os.ReadFile(f.m.path(".claude.json")); err == nil {
		_ = json.Unmarshal(data, &doc)
	}
	servers, _ := doc["mcpServers"].(map[string]any)
	if servers == nil {
		servers = map[string]any{}
	}
	switch args[1] {
	case "add-json":
		var entry any
		if err := json.Unmarshal([]byte(args[5]), &entry); err != nil {
			return nil, err
		}
		if _, ok := servers[args[4]]; ok {
			return []byte("already exists"), errors.New("exit status 1")
		}
		servers[args[4]] = entry
	case "remove":
		delete(servers, args[4])
	}
	doc["mcpServers"] = servers
	out, _ := json.MarshalIndent(doc, "", "  ")
	f.m.write(".claude.json", string(out))
	return []byte("ok"), nil
}

func (f *fakeClaude) added() []string {
	var out []string
	for _, c := range f.calls {
		if c[2] == "add-json" {
			out = append(out, c[6])
		}
	}
	return out
}

type tokens struct {
	made []string
	err  map[string]error
}

func (k *tokens) EnsureToken(_ context.Context, s Space) error {
	k.made = append(k.made, s.Namespace)
	return k.err[s.Namespace]
}

type opts struct {
	tools    []Tool
	exe      string
	onPath   string
	noClaude bool
	tokens   TokenEnsurer
}

func (m machine) service(o opts) (*Service, *fakeClaude) {
	m.t.Helper()
	if len(o.tools) == 0 {
		o.tools = []Tool{claudecode.New(), copilot.New()}
	}
	if o.exe == "" {
		o.exe = "/usr/local/bin/loadout"
	}
	if o.onPath == "" {
		o.onPath = o.exe
	}
	fake := &fakeClaude{m: m}
	s, err := New(Options{
		Home:       m.home,
		WishPath:   m.path(".loadout/mcp.yaml"),
		Executable: func() (string, error) { return o.exe, nil },
		LookPath: func(file string) (string, error) {
			switch {
			case file == "claude" && o.noClaude:
				return "", errors.New("not found")
			case file == "claude":
				return "/usr/local/bin/claude", nil
			}
			return o.onPath, nil
		},
		Run:    fake.run,
		Tokens: o.tokens,
		Log:    slog.New(slog.DiscardHandler),
	}, o.tools...)
	if err != nil {
		m.t.Fatal(err)
	}
	return s, fake
}

var lime = Space{Namespace: "lime", Endpoint: "https://registry/mcp/t/lime"}

func actions(r Report) map[string]Action {
	out := map[string]Action{}
	for _, o := range r.Tools {
		out[o.Tool+" "+o.Server] = o.Action
	}
	return out
}

func TestEnsureAddsAndRemovesWithoutTouchingTheRest(t *testing.T) {
	m := newMachine(t, ".claude", ".copilot")
	m.write(".copilot/mcp-config.json", personMCP)
	s, claude := m.service(opts{})

	rep := s.Ensure(t.Context(), []Space{lime, {Namespace: "acme"}})
	got := actions(rep)
	for _, k := range []string{"claude loadout-memory-lime", "claude loadout-memory-acme", "copilot loadout-memory-lime", "copilot loadout-memory-acme"} {
		if got[k] != ActionAdded {
			t.Errorf("Ensure() %s = %q, want added (all: %v)", k, got[k], got)
		}
	}
	file := m.read(".copilot/mcp-config.json")
	if !strings.Contains(file, `"https://api.githubcopilot.com/mcp/"`) || !strings.Contains(file, `"ratio": 1.50e3`) || !strings.Contains(file, `"loadout-memory-lime"`) {
		t.Errorf("mcp-config.json after Ensure:\n%s", file)
	}
	if m.read(".copilot/mcp-config.json.loadout.bak") != personMCP {
		t.Error("no backup of the person's mcp-config.json")
	}
	want := `{"type":"stdio","command":"loadout","args":["mcp","--team","acme"]}`
	if a := claude.added(); len(a) != 2 || a[0] != want {
		t.Errorf("claude was asked to add %q, want acme first as %s", a, want)
	}

	// Again: nothing changes and claude is not asked anything.
	calls := len(claude.calls)
	rep = s.Ensure(t.Context(), []Space{lime, {Namespace: "acme"}})
	for k, a := range actions(rep) {
		if a != ActionUnchanged {
			t.Errorf("second Ensure() %s = %q, want unchanged", k, a)
		}
	}
	if len(claude.calls) != calls {
		t.Errorf("a second Ensure() ran claude again: %v", claude.calls[calls:])
	}

	// acme is switched off: its entry goes, the person's bytes stay.
	m.write(".copilot/mcp-config.json.loadout.bak", "kept")
	rep = s.Ensure(t.Context(), []Space{lime})
	got = actions(rep)
	if got["claude loadout-memory-acme"] != ActionRemoved || got["copilot loadout-memory-acme"] != ActionRemoved || got["copilot loadout-memory-lime"] != ActionUnchanged {
		t.Errorf("Ensure() without acme = %v", got)
	}
	if strings.Contains(m.read(".copilot/mcp-config.json"), "acme") || strings.Contains(m.read(".claude.json"), "acme") {
		t.Error("the acme entry is still there")
	}

	// No spaces at all: every entry goes and the file is the original.
	rep = s.Ensure(t.Context(), nil)
	if len(rep.Changed()) != 2 {
		t.Errorf("Ensure(nil) changed %+v, want both lime entries removed", rep.Changed())
	}
	if m.read(".copilot/mcp-config.json") != personMCP {
		t.Errorf("mcp-config.json after the last remove =\n%s\nwant the original", m.read(".copilot/mcp-config.json"))
	}
	if m.read(".copilot/mcp-config.json.loadout.bak") != "kept" {
		t.Error("the backup was overwritten")
	}
	if !strings.Contains(m.read(".claude.json"), "numStartups") {
		t.Error("claude's own keys are gone")
	}
}

// TestEntryNamesTheRemoteWhenThereAreSeveral: with more than one registry the
// entry carries --remote, so the bridge never picks the registry for a token.
// With one registry the entry is as it was, and gains the flag when a second
// registry appears.
func TestEntryNamesTheRemoteWhenThereAreSeveral(t *testing.T) {
	m := newMachine(t, ".claude", ".copilot")
	s, claude := m.service(opts{})

	one := Space{Namespace: "lime", Remote: "office"}
	s.Ensure(t.Context(), []Space{one})
	if a := claude.added(); len(a) != 1 || strings.Contains(a[0], "--remote") {
		t.Fatalf("claude was asked to add %q for one registry, want no --remote", a)
	}

	many := Space{Namespace: "lime", Remote: "office", Several: true}
	rep := s.Ensure(t.Context(), []Space{many})
	if got := actions(rep); got["claude loadout-memory-lime"] != ActionUpdated || got["copilot loadout-memory-lime"] != ActionUpdated {
		t.Errorf("Ensure() with a second registry = %v, want both entries updated", got)
	}
	want := `{"type":"stdio","command":"loadout","args":["mcp","--team","lime","--remote","office"]}`
	if a := claude.added(); len(a) != 2 || a[1] != want {
		t.Errorf("claude was asked to add %q, want %s last", a, want)
	}
	if file := m.read(".copilot/mcp-config.json"); !strings.Contains(file, `"--remote"`) || !strings.Contains(file, `"office"`) {
		t.Errorf("mcp-config.json lacks --remote office:\n%s", file)
	}

	// A space without a remote name gets no flag, however many registries.
	noName := Space{Namespace: "acme", Several: true}
	s.Ensure(t.Context(), []Space{many, noName})
	if a := claude.added(); strings.Contains(a[len(a)-1], "--remote") {
		t.Errorf("an entry without a remote name got --remote: %s", a[len(a)-1])
	}
}

// TestFailedUpdatePutsTheOldEntryBack: claude has no update, so the entry is
// removed and added again. If the add fails, the old entry comes back.
func TestFailedUpdatePutsTheOldEntryBack(t *testing.T) {
	m := newMachine(t, ".claude")
	s, claude := m.service(opts{})
	s.Ensure(t.Context(), []Space{{Namespace: "lime", Remote: "office"}})
	before := m.read(".claude.json")

	claude.failAdd = true
	rep := s.Ensure(t.Context(), []Space{{Namespace: "lime", Remote: "office", Several: true}})
	if got := actions(rep)["claude loadout-memory-lime"]; got != ActionRefused {
		t.Fatalf("Ensure() = %v, want the update refused", actions(rep))
	}
	if after := m.read(".claude.json"); after != before {
		t.Errorf("the entry after a failed update =\n%s\nwant the one before:\n%s", after, before)
	}
	// The next run, with claude working, finishes the update.
	rep = s.Ensure(t.Context(), []Space{{Namespace: "lime", Remote: "office", Several: true}})
	if got := actions(rep)["claude loadout-memory-lime"]; got != ActionUpdated {
		t.Errorf("the retry = %v, want updated", actions(rep))
	}
}

func TestEnsureOnlyKnowsTheToolsThatAreThere(t *testing.T) {
	m := newMachine(t)
	s, claude := m.service(opts{tokens: &tokens{}})
	rep := s.Ensure(t.Context(), []Space{lime})
	got := actions(rep)
	if got["claude "] != ActionNoTool || got["copilot "] != ActionNoTool {
		t.Errorf("Ensure() = %v, want no tool for both", got)
	}
	if len(claude.calls) != 0 {
		t.Errorf("claude ran with no ~/.claude: %v", claude.calls)
	}
	entries, _ := os.ReadDir(m.home)
	if len(entries) != 0 {
		t.Errorf("Ensure() made %v in a home without tools", entries)
	}
}

func TestEnsureSkipsClaudeThatIsNotOnThePath(t *testing.T) {
	m := newMachine(t, ".claude", ".copilot")
	s, claude := m.service(opts{noClaude: true})
	rep := s.Ensure(t.Context(), []Space{lime})
	var refused Outcome
	for _, o := range rep.Of(ActionRefused) {
		refused = o
	}
	if refused.Tool != "claude" || refused.Err == nil || !strings.Contains(refused.Err.Error(), "not on the PATH") {
		t.Errorf("Ensure() = %+v, want claude refused with a sentence", rep.Tools)
	}
	if len(claude.calls) != 0 {
		t.Error("claude ran although it is not on the PATH")
	}
	if !m.exists(".copilot/mcp-config.json") {
		t.Error("Copilot was not set up because Claude Code could not be")
	}
	// Nothing to do and no claude: quiet.
	if rep := s.Ensure(t.Context(), nil); len(rep.Of(ActionRefused)) != 0 {
		t.Errorf("Ensure(nil) without claude = %+v, want no warning", rep.Tools)
	}
}

func TestEnsureFromOfflineChangesNothing(t *testing.T) {
	m := newMachine(t, ".claude", ".copilot")
	s, claude := m.service(opts{})
	s.Ensure(t.Context(), []Space{lime})
	before := m.read(".copilot/mcp-config.json")
	calls := len(claude.calls)

	rep := s.EnsureFrom(t.Context(), func(context.Context) (Listing, error) { return Listing{}, errors.New("no route to host") })
	if rep.Unreachable == nil || len(rep.Tools) != 0 {
		t.Errorf("EnsureFrom() = %+v, want unreachable and nothing done", rep)
	}
	if m.read(".copilot/mcp-config.json") != before || len(claude.calls) != calls {
		t.Error("a network failure changed the configuration")
	}
	rep = s.EnsureFrom(t.Context(), func(context.Context) (Listing, error) { return Listing{Complete: true}, nil })
	if len(rep.Changed()) != 2 {
		t.Errorf("EnsureFrom() with no spaces changed %+v, want both entries removed", rep.Changed())
	}
}

// TestIncompleteListingRemovesNothing: when a registry did not give a list
// (older, or it refused the token), the entries stay, and new ones are added.
func TestIncompleteListingRemovesNothing(t *testing.T) {
	m := newMachine(t, ".claude", ".copilot")
	s, claude := m.service(opts{})
	s.Ensure(t.Context(), []Space{lime, {Namespace: "acme"}})

	rep := s.EnsureFrom(t.Context(), func(context.Context) (Listing, error) {
		return Listing{Spaces: []Space{{Namespace: "pear"}}}, nil
	})
	if len(rep.Of(ActionRemoved)) != 0 {
		t.Errorf("an incomplete listing removed %+v", rep.Of(ActionRemoved))
	}
	got := actions(rep)
	if got["claude loadout-memory-pear"] != ActionAdded || got["copilot loadout-memory-pear"] != ActionAdded {
		t.Errorf("an incomplete listing did not add the listed team: %v", got)
	}
	for _, name := range []string{"lime", "acme", "pear"} {
		if !strings.Contains(m.read(".copilot/mcp-config.json"), "loadout-memory-"+name) || !strings.Contains(m.read(".claude.json"), "loadout-memory-"+name) {
			t.Errorf("the entry for %s is gone", name)
		}
	}
	_ = claude

	// Complete and empty: now they go.
	rep = s.EnsureFrom(t.Context(), func(context.Context) (Listing, error) { return Listing{Complete: true}, nil })
	if len(rep.Of(ActionRemoved)) != 6 {
		t.Errorf("a complete empty listing removed %d entries, want 6", len(rep.Of(ActionRemoved)))
	}
}

func TestTokenFailureKeepsWhatIsThere(t *testing.T) {
	m := newMachine(t, ".copilot")
	k := &tokens{err: map[string]error{"acme": errors.New("login expired")}}
	s, _ := m.service(opts{tools: []Tool{copilot.New()}, tokens: k})
	rep := s.Ensure(t.Context(), []Space{lime, {Namespace: "acme"}})
	if len(rep.Failed) != 1 || rep.Failed[0].Space.Namespace != "acme" {
		t.Fatalf("Failed = %+v, want acme", rep.Failed)
	}
	if got := m.read(".copilot/mcp-config.json"); strings.Contains(got, "acme") || !strings.Contains(got, "lime") {
		t.Errorf("mcp-config.json =\n%s\nwant lime only", got)
	}

	// An entry that exists stays while its token cannot be renewed.
	k.err["lime"] = errors.New("login expired")
	rep = s.Ensure(t.Context(), []Space{lime})
	if len(rep.Changed()) != 0 || !strings.Contains(m.read(".copilot/mcp-config.json"), "lime") {
		t.Errorf("Ensure() with a failing token = %+v, want the entry left alone", rep)
	}
}

func TestDeclinedToolIsLeftAlone(t *testing.T) {
	m := newMachine(t, ".claude", ".copilot")
	s, claude := m.service(opts{})
	s.Ensure(t.Context(), []Space{lime})

	rep, err := s.Decline(t.Context(), "copilot")
	if err != nil || actions(rep)["copilot loadout-memory-lime"] != ActionRemoved {
		t.Fatalf("Decline() = %+v, %v", rep, err)
	}
	if m.exists(".copilot/mcp-config.json") {
		t.Error("Copilot's file held only loadout's entry and should be gone")
	}
	rep = s.Ensure(t.Context(), []Space{lime})
	if actions(rep)["copilot "] != ActionDeclined || m.exists(".copilot/mcp-config.json") {
		t.Errorf("Ensure() after a decline = %v", actions(rep))
	}
	if w, _ := s.Wish(); !slices.Equal(w.Declined, []string{"copilot"}) {
		t.Errorf("wish = %+v", w)
	}
	if !strings.Contains(m.read(".loadout/mcp.yaml"), "- copilot") {
		t.Errorf("wish file:\n%s", m.read(".loadout/mcp.yaml"))
	}
	if strings.Contains(strings.Join(slices.Concat(claude.calls...), " "), "remove") {
		t.Error("declining Copilot removed Claude Code's entry")
	}

	if err := s.Allow("copilot"); err != nil {
		t.Fatal(err)
	}
	if rep = s.Ensure(t.Context(), []Space{lime}); actions(rep)["copilot loadout-memory-lime"] != ActionAdded {
		t.Errorf("Ensure() after allow = %v", actions(rep))
	}
	if _, err := s.Decline(t.Context(), "emacs"); err == nil || !strings.Contains(err.Error(), "choose from claude, copilot") {
		t.Errorf("Decline(emacs) = %v", err)
	}
	if rep, err := s.Decline(t.Context()); err != nil || len(rep.Changed()) != 2 {
		t.Errorf("Decline() all = %+v, %v", rep, err)
	}
}

func TestEnsureRefusesLinksAndBadFiles(t *testing.T) {
	tests := []struct {
		name  string
		setup func(m machine)
		want  string
	}{
		{"linked file", func(m machine) {
			m.write(".copilot/real.json", "{}")
			if err := os.Symlink(m.path(".copilot/real.json"), m.path(".copilot/mcp-config.json")); err != nil {
				m.t.Fatal(err)
			}
		}, "~/.copilot/mcp-config.json is a symbolic link"},
		{"linked folder", func(m machine) {
			if err := os.Rename(m.path(".copilot"), m.path("dotfiles")); err != nil {
				m.t.Fatal(err)
			}
			if err := os.Symlink(m.path("dotfiles"), m.path(".copilot")); err != nil {
				m.t.Fatal(err)
			}
		}, "~/.copilot is a symbolic link"},
		{"invalid JSON", func(m machine) { m.write(".copilot/mcp-config.json", "{ \"mcpServers\": {}, }") }, "loadout leaves the file as it is"},
		{"the person's server of that name", func(m machine) {
			m.write(".copilot/mcp-config.json", `{"mcpServers": {"loadout-memory-lime": {"command": "mine"}}}`)
		}, "not loadout's"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := newMachine(t, ".copilot")
			tc.setup(m)
			before, _ := os.ReadFile(m.path(".copilot/mcp-config.json"))
			s, _ := m.service(opts{tools: []Tool{copilot.New()}})
			rep := s.Ensure(t.Context(), []Space{lime})
			refused := rep.Of(ActionRefused)
			if len(refused) != 1 || refused[0].Err == nil || !strings.Contains(refused[0].Err.Error(), tc.want) {
				t.Fatalf("Ensure() = %+v, want refused with %q", rep.Tools, tc.want)
			}
			if after, _ := os.ReadFile(m.path(".copilot/mcp-config.json")); string(after) != string(before) {
				t.Error("a refused Ensure changed the file")
			}
			if m.exists(".copilot/mcp-config.json.loadout.bak") || m.exists("dotfiles/mcp-config.json") {
				t.Error("a refused Ensure wrote a backup or a file")
			}
		})
	}
}

func TestBackupIsKeptOnceAndOnlyForThePersonsBytes(t *testing.T) {
	m := newMachine(t, ".copilot")
	s, _ := m.service(opts{tools: []Tool{copilot.New()}})
	s.Ensure(t.Context(), []Space{lime})
	if m.exists(".copilot/mcp-config.json.loadout.bak") {
		t.Error("a backup was made of a file that loadout created")
	}
	s.Ensure(t.Context(), []Space{lime, {Namespace: "acme"}})
	if m.exists(".copilot/mcp-config.json.loadout.bak") {
		t.Error("a backup was made of a file that holds only loadout's entries")
	}
	s.Ensure(t.Context(), nil)

	m.write(".copilot/mcp-config.json", personMCP)
	s.Ensure(t.Context(), []Space{lime})
	first := m.read(".copilot/mcp-config.json.loadout.bak")
	s.Ensure(t.Context(), []Space{lime, {Namespace: "acme"}})
	s.Ensure(t.Context(), nil)
	if first != personMCP || m.read(".copilot/mcp-config.json.loadout.bak") != personMCP {
		t.Errorf("backup = %q, want the person's file once", m.read(".copilot/mcp-config.json.loadout.bak"))
	}
}

// racer writes the MCP file itself while loadout changes it, as the tool
// might.
type racer struct {
	*copilot.Adapter
	m     machine
	times int
}

func (r *racer) WithMCPServer(cur []byte, s target.MCPServer) ([]byte, error) {
	if r.times > 0 {
		r.times--
		r.m.write(".copilot/mcp-config.json", fmt.Sprintf("{\"banner\": %d}\n", r.times))
	}
	return r.Adapter.WithMCPServer(cur, s)
}

func TestAFileChangedMeanwhile(t *testing.T) {
	m := newMachine(t, ".copilot")
	m.write(".copilot/mcp-config.json", "{\"banner\": 9}\n")
	once := &racer{Adapter: copilot.New(), m: m, times: 1}
	s, _ := m.service(opts{tools: []Tool{once}})
	rep := s.Ensure(t.Context(), []Space{lime})
	if len(rep.Of(ActionAdded)) != 1 {
		t.Fatalf("Ensure() = %+v, want added on the second try", rep.Tools)
	}
	if got := m.read(".copilot/mcp-config.json"); !strings.Contains(got, `"banner": 0`) || !strings.Contains(got, "loadout-memory-lime") {
		t.Errorf("mcp-config.json = %s, want the tool's change and the entry", got)
	}

	m2 := newMachine(t, ".copilot")
	m2.write(".copilot/mcp-config.json", "{\"banner\": 9}\n")
	always := &racer{Adapter: copilot.New(), m: m2, times: 99}
	s2, _ := m2.service(opts{tools: []Tool{always}})
	rep = s2.Ensure(t.Context(), []Space{lime})
	if r := rep.Of(ActionRefused); len(r) != 1 || !errors.Is(r[0].Err, errChanged) {
		t.Errorf("Ensure() = %+v, want refused: the file kept changing", rep.Tools)
	}
	if strings.Contains(m2.read(".copilot/mcp-config.json"), "loadout") {
		t.Error("loadout wrote over a file that kept changing")
	}
}

func TestCommand(t *testing.T) {
	tests := []struct {
		name, exe, onPath, want string
	}{
		{"on the PATH", "/usr/local/bin/loadout", "/usr/local/bin/loadout", "loadout"},
		{"not on the PATH", "/opt/tools/loadout", "/usr/bin/other", "/opt/tools/loadout"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := newMachine(t, ".copilot", ".claude")
			s, claude := m.service(opts{exe: tc.exe, onPath: tc.onPath})
			s.Ensure(t.Context(), []Space{lime})
			if got := m.read(".copilot/mcp-config.json"); !strings.Contains(got, fmt.Sprintf("%q", tc.want)) {
				t.Errorf("Copilot's command is not %q:\n%s", tc.want, got)
			}
			if a := claude.added(); len(a) != 1 || !strings.Contains(a[0], fmt.Sprintf(`"command":%q`, tc.want)) {
				t.Errorf("claude was asked to add %q, want command %q", a, tc.want)
			}
		})
	}
}

func TestMovedBinaryUpdatesTheEntries(t *testing.T) {
	m := newMachine(t, ".copilot", ".claude")
	old, _ := m.service(opts{exe: "/old/loadout", onPath: "/usr/bin/other"})
	old.Ensure(t.Context(), []Space{lime})
	s, claude := m.service(opts{})
	rep := s.Ensure(t.Context(), []Space{lime})
	got := actions(rep)
	if got["claude loadout-memory-lime"] != ActionUpdated || got["copilot loadout-memory-lime"] != ActionUpdated {
		t.Errorf("Ensure() = %v, want both updated", got)
	}
	if a := claude.added(); len(a) != 1 || !strings.Contains(a[0], `"command":"loadout"`) {
		t.Errorf("claude was asked to add %q", a)
	}
	if strings.Contains(m.read(".claude.json")+m.read(".copilot/mcp-config.json"), "/old/") {
		t.Error("the old command is still there")
	}
}

func TestClaudeFailureIsAWarningNotAnError(t *testing.T) {
	m := newMachine(t, ".claude", ".copilot")
	s, claude := m.service(opts{})
	claude.fail = errors.New("exit status 1")
	rep := s.Ensure(t.Context(), []Space{lime})
	r := rep.Of(ActionRefused)
	if len(r) != 1 || r[0].Tool != "claude" || !strings.Contains(r[0].Err.Error(), "boom") {
		t.Errorf("Ensure() = %+v, want claude refused with its output", rep.Tools)
	}
	if strings.Contains(r[0].Err.Error(), "stdio") {
		t.Errorf("the error repeats the JSON: %v", r[0].Err)
	}
	if len(rep.Of(ActionAdded)) != 1 {
		t.Error("Copilot was not set up because Claude Code failed")
	}
}

func TestClaudeJSONIsNeverWritten(t *testing.T) {
	m := newMachine(t, ".claude")
	const own = "{\"numStartups\":1,\"mcpServers\":{\"loadout-memory-old\":{\"type\":\"stdio\",\"command\":\"loadout\",\"args\":[\"mcp\",\"--team\",\"old\"]}}}"
	m.write(".claude.json", own)
	s, claude := m.service(opts{tools: []Tool{claudecode.New()}})
	claude.fail = errors.New("exit status 1")
	s.Ensure(t.Context(), []Space{lime})
	if m.read(".claude.json") != own || m.exists(".claude.json.loadout.bak") {
		t.Error("loadout changed ~/.claude.json itself")
	}
	if len(claude.calls) == 0 || claude.calls[0][0] != "claude" {
		t.Errorf("calls = %v", claude.calls)
	}
}

func TestOddNamespacesAreRefused(t *testing.T) {
	m := newMachine(t, ".copilot")
	s, _ := m.service(opts{tools: []Tool{copilot.New()}})
	rep := s.Ensure(t.Context(), []Space{{Namespace: "a b"}, {Namespace: "../x"}, {Namespace: ""}, {Namespace: "lime"}})
	if len(rep.Failed) != 3 || !strings.Contains(m.read(".copilot/mcp-config.json"), "lime") {
		t.Errorf("Failed = %+v", rep.Failed)
	}
}

func TestDuplicateSpacesCountOnce(t *testing.T) {
	m := newMachine(t, ".copilot")
	k := &tokens{}
	s, _ := m.service(opts{tools: []Tool{copilot.New()}, tokens: k})
	s.Ensure(t.Context(), []Space{lime, lime})
	if len(k.made) != 1 {
		t.Errorf("tokens made for %v, want once", k.made)
	}
}

func TestNewRefusesAToolWithoutMCP(t *testing.T) {
	_, err := New(Options{Home: t.TempDir(), WishPath: "x"}, plain{})
	if err == nil || !strings.Contains(err.Error(), "no team memory setup") {
		t.Errorf("New() = %v", err)
	}
	if _, err := New(Options{}); err == nil {
		t.Error("New() without a home folder succeeded")
	}
}

type plain struct{}

func (plain) Name() string                      { return "plain" }
func (plain) Product() string                   { return "Plain" }
func (plain) MarkerDir(target.ScopeKind) string { return ".plain" }

func TestDamagedWish(t *testing.T) {
	m := newMachine(t, ".copilot")
	if err := os.MkdirAll(m.path(".loadout"), 0o755); err != nil {
		t.Fatal(err)
	}
	m.write(".loadout/mcp.yaml", "version: 1\nsurprise: yes\n")
	s, _ := m.service(opts{tools: []Tool{copilot.New()}})
	rep := s.Ensure(t.Context(), []Space{lime})
	if rep.Unreachable == nil || m.exists(".copilot/mcp-config.json") {
		t.Errorf("Ensure() with a damaged wish = %+v, want nothing done", rep)
	}
	if _, err := s.Decline(t.Context()); err == nil || !strings.Contains(err.Error(), "is damaged") {
		t.Errorf("Decline() = %v", err)
	}
}

func TestEnsureRaisesTheDescriptionLimitOnlyWhenUnset(t *testing.T) {
	const own = "{\n  \"model\": \"opus\"\n}\n"
	m := newMachine(t, ".claude")
	m.write(".claude/settings.json", own)
	s, _ := m.service(opts{})

	rep := s.Ensure(t.Context(), []Space{lime})
	if len(rep.Env) != 1 || rep.Env[0].Action != ActionAdded || rep.Env[0].Value != "24000" {
		t.Fatalf("Ensure().Env = %+v, want the limit added once", rep.Env)
	}
	got := m.read(".claude/settings.json")
	if !strings.Contains(got, `"CLAUDE_CODE_MAX_MCP_DESCRIPTION_LENGTH": "24000"`) || !strings.Contains(got, `"model": "opus"`) {
		t.Errorf("settings.json after Ensure:\n%s", got)
	}
	if m.read(".claude/settings.json.loadout.bak") != own {
		t.Error("no backup of the person's settings.json")
	}

	// The entry is there now, so nothing is set again, even after the
	// person changed the value.
	m.write(".claude/settings.json", strings.Replace(got, "24000", "5000", 1))
	if rep = s.Ensure(t.Context(), []Space{lime}); len(rep.Env) != 0 {
		t.Errorf("second Ensure().Env = %+v, want none", rep.Env)
	}
	if !strings.Contains(m.read(".claude/settings.json"), `"5000"`) {
		t.Error("the person's value was replaced")
	}
}

func TestEnsureLeavesAVariableThePersonSet(t *testing.T) {
	const own = `{"env": {"CLAUDE_CODE_MAX_MCP_DESCRIPTION_LENGTH": "9000"}}`
	m := newMachine(t, ".claude")
	m.write(".claude/settings.json", own)
	s, _ := m.service(opts{})
	rep := s.Ensure(t.Context(), []Space{lime})
	if len(rep.Env) != 1 || rep.Env[0].Action != ActionUnchanged || m.read(".claude/settings.json") != own {
		t.Errorf("Env = %+v, settings.json = %s", rep.Env, m.read(".claude/settings.json"))
	}
}

func TestNoLimitWithoutAnEntryForClaude(t *testing.T) {
	m := newMachine(t, ".claude", ".copilot")
	s, _ := m.service(opts{})
	if rep := s.Ensure(t.Context(), nil); len(rep.Env) != 0 || m.exists(".claude/settings.json") {
		t.Errorf("Ensure(nil) set a limit: %+v", rep.Env)
	}
	m.write(".claude/settings.json", "{")
	rep := s.Ensure(t.Context(), []Space{lime})
	if len(rep.Env) != 1 || rep.Env[0].Action != ActionRefused || m.read(".claude/settings.json") != "{" {
		t.Errorf("a settings.json that does not parse: Env = %+v", rep.Env)
	}
}
