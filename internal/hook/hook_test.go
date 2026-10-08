package hook

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/petervlad/loadout/internal/target"
	"github.com/petervlad/loadout/internal/target/claudecode"
	"github.com/petervlad/loadout/internal/target/copilot"
)

// personSettings is a settings.json of the person's own, as Claude Code
// writes it.
const personSettings = "{\n  \"model\": \"opus\",\n  \"hooks\": {\n    \"Stop\": [\n      {\n        \"hooks\": [\n          {\n            \"type\": \"command\",\n            \"command\": \"say done\"\n          }\n        ]\n      }\n    ]\n  }\n}\n"

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

// service returns a service for this machine, whose loadout the PATH
// finds as loadout.
func (m machine) service(tools ...Tool) *Service {
	m.t.Helper()
	if len(tools) == 0 {
		tools = []Tool{claudecode.New(), copilot.New()}
	}
	s, err := New(Options{
		Home:       m.home,
		WishPath:   m.path(".loadout/hooks.yaml"),
		Executable: func() (string, error) { return "/usr/local/bin/loadout", nil },
		LookPath:   func(string) (string, error) { return "/usr/local/bin/loadout", nil },
		Log:        slog.New(slog.DiscardHandler),
	}, tools...)
	if err != nil {
		m.t.Fatal(err)
	}
	return s
}

func actions(r Report) map[string]Action {
	out := map[string]Action{}
	for _, o := range r.Tools {
		out[o.Tool] = o.Action
	}
	return out
}

func TestSetupAndRemoveKeepThePersonsFile(t *testing.T) {
	m := newMachine(t, ".claude", ".copilot")
	m.write(".claude/settings.json", personSettings)
	s := m.service()

	rep, err := s.Setup(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := actions(rep); got["claude"] != ActionAdded || got["copilot"] != ActionAdded {
		t.Fatalf("Setup() = %v, want both added", got)
	}
	settings := m.read(".claude/settings.json")
	if !strings.Contains(settings, `"command": "say done"`) || !strings.Contains(settings, `"command": "loadout notices --hook claude 2>/dev/null || true"`) {
		t.Errorf("settings.json after setup:\n%s", settings)
	}
	if !strings.Contains(m.read(".copilot/hooks/loadout.json"), "notices --hook copilot") {
		t.Error("Copilot's hook file was not written")
	}
	if m.read(".claude/settings.json.loadout.bak") != personSettings {
		t.Error("no backup of the person's settings.json")
	}

	// Again: nothing changes.
	rep, err = s.Setup(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := actions(rep); got["claude"] != ActionUnchanged || got["copilot"] != ActionUnchanged {
		t.Errorf("second Setup() = %v, want unchanged", got)
	}

	// Remove gives back the person's bytes and deletes loadout's own file;
	// the backup stays, and is not rewritten by later changes.
	m.write(".claude/settings.json.loadout.bak", "kept")
	rep, err = s.Remove(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if got := actions(rep); got["claude"] != ActionRemoved || got["copilot"] != ActionRemoved {
		t.Errorf("Remove() = %v, want removed", got)
	}
	if got := m.read(".claude/settings.json"); got != personSettings {
		t.Errorf("settings.json after remove =\n%s\nwant the original", got)
	}
	if m.exists(".copilot/hooks/loadout.json") {
		t.Error("Copilot's hook file is still there")
	}
	if m.read(".claude/settings.json.loadout.bak") != "kept" {
		t.Error("the backup was overwritten")
	}
	if w, _ := s.Wish(); !w.Declined || w.Wanted {
		t.Errorf("wish after remove = %+v, want declined", w)
	}
	if rep, _ := s.Ensure(t.Context()); len(rep.Tools) != 0 || strings.Contains(m.read(".claude/settings.json"), "loadout") {
		t.Errorf("Ensure() after a decline = %+v, want nothing", rep)
	}
	rep, err = s.Remove(t.Context())
	if err != nil || actions(rep)["claude"] != ActionAbsent {
		t.Errorf("Remove() twice = %v, %v; want absent", actions(rep), err)
	}
}

func TestSetupCreatesOnlyBelowTheToolsFolder(t *testing.T) {
	m := newMachine(t, ".claude")
	rep, err := m.service().Setup(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := actions(rep); got["claude"] != ActionAdded || got["copilot"] != ActionNoTool {
		t.Errorf("Setup() = %v, want claude added, no copilot", got)
	}
	if m.exists(".copilot") {
		t.Error("Setup() made ~/.copilot")
	}
	if m.exists(".claude/settings.json.loadout.bak") {
		t.Error("a backup of a file loadout made")
	}
	// A settings.json loadout made holds nothing else: removing deletes it.
	if _, err := m.service().Remove(t.Context()); err != nil {
		t.Fatal(err)
	}
	if m.exists(".claude/settings.json") {
		t.Error("Remove() left the settings.json loadout made")
	}
}

func TestSetupRefusesLinksAndBadFiles(t *testing.T) {
	tests := []struct {
		name  string
		setup func(m machine)
		want  string
	}{
		{"linked settings.json", func(m machine) {
			m.write(".claude/real.json", "{}")
			if err := os.Symlink(m.path(".claude/real.json"), m.path(".claude/settings.json")); err != nil {
				m.t.Fatal(err)
			}
		}, "~/.claude/settings.json is a symbolic link"},
		{"linked .claude", func(m machine) {
			if err := os.Rename(m.path(".claude"), m.path("dotfiles")); err != nil {
				m.t.Fatal(err)
			}
			if err := os.Symlink(m.path("dotfiles"), m.path(".claude")); err != nil {
				m.t.Fatal(err)
			}
		}, "~/.claude is a symbolic link"},
		{"invalid JSON", func(m machine) { m.write(".claude/settings.json", "{ \"model\": \"opus\", }") }, "loadout leaves the file as it is"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := newMachine(t, ".claude")
			tc.setup(m)
			before, _ := os.ReadFile(m.path(".claude/settings.json"))
			rep, err := m.service(claudecode.New()).Setup(t.Context(), nil)
			if err != nil {
				t.Fatal(err)
			}
			o := rep.Tools[0]
			if o.Action != ActionRefused || o.Err == nil || !strings.Contains(o.Err.Error(), tc.want) || !strings.Contains(o.Snippet, "notices --hook claude") {
				t.Errorf("Setup() = %+v, want refused with %q and a snippet", o, tc.want)
			}
			if after, _ := os.ReadFile(m.path(".claude/settings.json")); string(after) != string(before) {
				t.Error("a refused setup changed the file")
			}
			if m.exists(".claude/settings.json.loadout.bak") {
				t.Error("a refused setup wrote a backup")
			}
		})
	}
}

// racer writes the hook file itself while loadout changes it, as Claude
// Code might.
type racer struct {
	*claudecode.Adapter
	m     machine
	times int
}

func (r *racer) WithHook(cur []byte, cmd target.HookCommand) ([]byte, error) {
	if r.times > 0 {
		r.times--
		r.m.write(".claude/settings.json", fmt.Sprintf("{\"model\": \"sonnet\", \"n\": %d}\n", r.times))
	}
	return r.Adapter.WithHook(cur, cmd)
}

func TestAFileChangedMeanwhile(t *testing.T) {
	m := newMachine(t, ".claude")
	m.write(".claude/settings.json", "{\"model\": \"opus\"}\n")
	once := &racer{Adapter: claudecode.New(), m: m, times: 1}
	rep, err := m.service(once).Setup(t.Context(), nil)
	if err != nil || rep.Tools[0].Action != ActionAdded {
		t.Fatalf("Setup() = %+v, %v; want added on the second try", rep, err)
	}
	if got := m.read(".claude/settings.json"); !strings.Contains(got, "sonnet") || !strings.Contains(got, "notices --hook claude") {
		t.Errorf("settings.json = %s, want the tool's change and the hook", got)
	}

	m2 := newMachine(t, ".claude")
	m2.write(".claude/settings.json", "{\"model\": \"opus\"}\n")
	always := &racer{Adapter: claudecode.New(), m: m2, times: 99}
	rep, err = m2.service(always).Setup(t.Context(), nil)
	if err != nil || rep.Tools[0].Action != ActionRefused || !errors.Is(rep.Tools[0].Err, errChanged) {
		t.Errorf("Setup() = %+v, %v; want refused: the file kept changing", rep, err)
	}
	if strings.Contains(m2.read(".claude/settings.json"), "loadout") {
		t.Error("loadout wrote over a file that kept changing")
	}
}

func TestCommand(t *testing.T) {
	tests := []struct {
		name, exe, onPath string
		windows           bool
		posix, ps         string
	}{
		{
			"on the PATH", "/usr/local/bin/loadout", "/usr/local/bin/loadout", false,
			"loadout notices --hook claude 2>/dev/null || true",
			"if (Get-Command loadout -ErrorAction SilentlyContinue) { loadout notices --hook claude 2>$null }; exit 0",
		},
		{
			"not on the PATH", "/Users/o'neil/go/bin/loadout", "", false,
			`'/Users/o'\''neil/go/bin/loadout' notices --hook claude 2>/dev/null || true`,
			"if (Test-Path -LiteralPath '/Users/o''neil/go/bin/loadout') { & '/Users/o''neil/go/bin/loadout' notices --hook claude 2>$null }; exit 0",
		},
		{
			"another loadout on the PATH", "/opt/new/loadout", "/usr/local/bin/loadout", true,
			"'/opt/new/loadout' notices --hook claude 2>/dev/null || true",
			"if (Test-Path -LiteralPath '/opt/new/loadout') { & '/opt/new/loadout' notices --hook claude 2>$null }; exit 0",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := &Service{
				executable: func() (string, error) { return tc.exe, nil },
				lookPath: func(string) (string, error) {
					if tc.onPath == "" {
						return "", errors.New("not found")
					}
					return tc.onPath, nil
				},
				windows: tc.windows,
			}
			got := s.command("claude")
			if got.POSIX != tc.posix || got.PowerShell != tc.ps || got.Windows != tc.windows {
				t.Errorf("command() =\n%+v\nwant\n%q\n%q", got, tc.posix, tc.ps)
			}
		})
	}
}

func TestSetupNamesTools(t *testing.T) {
	m := newMachine(t, ".claude", ".copilot")
	rep, err := m.service().Setup(t.Context(), []string{"copilot"})
	if err != nil || len(rep.Tools) != 1 || rep.Tools[0].Tool != "copilot" {
		t.Errorf("Setup(copilot) = %+v, %v", rep, err)
	}
	if m.exists(".claude/settings.json") {
		t.Error("Setup(copilot) wrote Claude's settings")
	}
	if _, err := m.service().Setup(t.Context(), []string{"vim"}); err == nil || !strings.Contains(err.Error(), "claude, copilot") {
		t.Errorf("Setup(vim) error = %v, want the tools to choose from", err)
	}
	if w, _ := m.service().Wish(); !w.Wanted {
		t.Error("Setup() did not record the wish")
	}
}

func TestDamagedWish(t *testing.T) {
	m := newMachine(t, ".claude", ".loadout")
	m.write(".loadout/hooks.yaml", "version: 1\nwanted: true\nsurprise: 1\n")
	if _, err := m.service().Wish(); err == nil || !strings.Contains(err.Error(), "loadout setup --hooks") {
		t.Errorf("Wish() error = %v, want a remedy", err)
	}
}
