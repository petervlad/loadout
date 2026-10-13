package review

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// fakeTool writes a shell script that stands in for an AI CLI: it records
// its arguments, working directory, standard input and environment, then
// prints output. Tests never start a real AI tool.
func fakeTool(t *testing.T, output string) (bin, record string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the fake tool is a shell script")
	}
	dir := t.TempDir()
	record = filepath.Join(dir, "record")
	out := filepath.Join(dir, "output")
	if err := os.WriteFile(out, []byte(output), 0o600); err != nil {
		t.Fatal(err)
	}
	script := "#!/bin/sh\n" +
		"{ printf 'args:'; for a in \"$@\"; do printf ' [%s]' \"$a\"; done; echo; echo \"dir: $(pwd)\"; echo \"token: ${LOADOUT_TOKEN:-unset}\"; echo \"allow: ${COPILOT_ALLOW_ALL:-unset}\"; printf 'stdin: '; cat; echo; } > '" + record + "'\n" +
		"if [ \"$1\" = --version ]; then echo '1.2.3 (Fake Tool)'; exit 0; fi\n" +
		"if [ -n \"$FAKE_SLEEP\" ]; then sleep \"$FAKE_SLEEP\"; fi\n" +
		"if [ -n \"$FAKE_FAIL\" ]; then echo 'quota exceeded' >&2; exit 3; fi\n" +
		"cat '" + out + "'\n" +
		"if [ -n \"$FAKE_EXIT\" ]; then exit \"$FAKE_EXIT\"; fi\n"
	bin = filepath.Join(dir, "tool")
	if err := os.WriteFile(bin, []byte(script), 0o700); err != nil { //nolint:gosec // an executable test script
		t.Fatal(err)
	}
	return bin, record
}

func readRecord(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestClaudeCodeRun(t *testing.T) {
	const out = `{"type":"result","subtype":"success","is_error":false,"duration_ms":2300,"result":"Here is the plan.","usage":{"input_tokens":3,"cache_creation_input_tokens":100,"cache_read_input_tokens":900,"output_tokens":42}}`
	bin, record := fakeTool(t, out)
	dir := t.TempDir()
	r := NewClaudeCode(bin, []string{"PATH=/usr/bin:/bin", "LOADOUT_TOKEN=ldt_secret"})

	v, err := r.Version(t.Context())
	if err != nil || v != "1.2.3 (Fake Tool)" {
		t.Errorf("Version() = %q, %v", v, err)
	}
	ans, err := r.Run(t.Context(), Request{Dir: dir, Agent: "candidate-planner", Prompt: "-plan TRK-1", Model: "haiku", Mode: ModeSafe})
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	want := Answer{Text: "Here is the plan.", Duration: 2300 * time.Millisecond, InputTokens: 1003, OutputTokens: 42}
	if ans != want {
		t.Errorf("Run() = %+v, want %+v", ans, want)
	}
	rec := readRecord(t, record)
	realDir, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{
		"args: [-p] [--output-format] [json] [--no-session-persistence] [--permission-mode] [dontAsk] [--agent] [candidate-planner] [--model] [haiku]\n",
		"dir: " + realDir + "\n",
		"token: unset\n",
		"stdin: -plan TRK-1\n",
	} {
		if !strings.Contains(rec, s) {
			t.Errorf("the tool saw:\n%s\nwant it to contain %q", rec, s)
		}
	}
}

func TestClaudeCodeReportsErrors(t *testing.T) {
	tests := []struct {
		name, output, env, want string
	}{
		{"error result", `{"type":"result","subtype":"error_max_turns","is_error":true,"result":"stopped"}`, "", "error_max_turns"},
		{"not json", "Invalid API key", "", "read claude output"},
		{"exit code", "", "FAKE_FAIL=1", "quota exceeded"},
		{"exit code with a json error", `{"type":"result","is_error":true,"result":"Not logged in · Please run /login"}`, "FAKE_EXIT=1", "Not logged in"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			bin, _ := fakeTool(t, tc.output)
			env := []string{"PATH=/usr/bin:/bin"}
			if tc.env != "" {
				env = append(env, tc.env)
			}
			_, err := NewClaudeCode(bin, env).Run(t.Context(), Request{Dir: t.TempDir(), Agent: "a", Prompt: "p", Mode: ModeSafe})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("Run() error = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestRunnerTimeoutKillsTheTool(t *testing.T) {
	bin, _ := fakeTool(t, "never")
	ctx, cancel := context.WithTimeout(t.Context(), 200*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := NewCopilotCLI(bin, []string{"PATH=/usr/bin:/bin", "FAKE_SLEEP=30"}).Run(ctx, Request{Dir: t.TempDir(), Agent: "a", Prompt: "p", Mode: ModeSafe})
	if err == nil || !strings.Contains(err.Error(), "deadline exceeded") {
		t.Errorf("Run() error = %v, want a deadline error", err)
	}
	if d := time.Since(start); d > 10*time.Second {
		t.Errorf("Run() took %s after its deadline, want the tool killed", d)
	}
}

func TestCopilotCLIRun(t *testing.T) {
	bin, record := fakeTool(t, "  The plan.\n")
	r := NewCopilotCLI(bin, []string{"PATH=/usr/bin:/bin"})
	ans, err := r.Run(t.Context(), Request{Dir: t.TempDir(), Agent: "current-planner", Prompt: "plan it", Mode: ModeSafe})
	if err != nil || ans.Text != "The plan." || ans.InputTokens != 0 {
		t.Errorf("Run() = %+v, %v; want the trimmed answer and no token counts", ans, err)
	}
	if rec := readRecord(t, record); !strings.Contains(rec, "args: [--prompt=plan it] [-s] [--agent] [current-planner]\n") {
		t.Errorf("the tool saw:\n%s", rec)
	}
}

func TestLimitedBuffer(t *testing.T) {
	b := &limitedBuffer{limit: 4}
	for _, p := range []string{"ab", "cdef", "g"} {
		if n, err := b.Write([]byte(p)); n != len(p) || err != nil {
			t.Errorf("Write(%q) = %d, %v; want %d, nil", p, n, err, len(p))
		}
	}
	if b.String() != "abcd" || !b.over {
		t.Errorf("buffer = %q, over %v; want abcd, true", b.String(), b.over)
	}
}

func TestRunWithoutAnAgent(t *testing.T) {
	tests := []struct {
		name     string
		output   string
		run      func(t *testing.T, bin string) error
		wantArgs string
	}{
		{
			name:   "claude",
			output: `{"type":"result","result":"ok"}`,
			run: func(t *testing.T, bin string) error {
				t.Helper()
				_, err := NewClaudeCode(bin, []string{"PATH=/usr/bin:/bin"}).Run(t.Context(), Request{Dir: t.TempDir(), Prompt: "p", Mode: ModeSafe})
				return err
			},
			wantArgs: "args: [-p] [--output-format] [json] [--no-session-persistence] [--permission-mode] [dontAsk]\n",
		},
		{
			name:   "copilot",
			output: "ok",
			run: func(t *testing.T, bin string) error {
				t.Helper()
				_, err := NewCopilotCLI(bin, []string{"PATH=/usr/bin:/bin"}).Run(t.Context(), Request{Dir: t.TempDir(), Prompt: "p", Model: "m", Mode: ModeSafe})
				return err
			},
			wantArgs: "args: [--prompt=p] [-s] [--model] [m]\n",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			bin, record := fakeTool(t, tc.output)
			if err := tc.run(t, bin); err != nil {
				t.Fatalf("Run() error: %v", err)
			}
			if rec := readRecord(t, record); !strings.Contains(rec, tc.wantArgs) {
				t.Errorf("the tool saw:\n%s\nwant %q", rec, tc.wantArgs)
			}
		})
	}
}

func TestRunnerModes(t *testing.T) {
	env := []string{"PATH=/usr/bin:/bin", "LOADOUT_TOKEN=ldt_secret", "COPILOT_ALLOW_ALL=true"}
	claude := func(bin string) Runner { return NewClaudeCode(bin, env) }
	cop := func(bin string) Runner { return NewCopilotCLI(bin, env) }
	const ok = `{"type":"result","result":"ok"}`
	tests := []struct {
		name     string
		output   string
		runner   func(bin string) Runner
		req      Request
		wantArgs string
		// wantAllow is what the tool saw in COPILOT_ALLOW_ALL.
		wantAllow string
	}{
		{
			name: "claude full", output: ok, runner: claude,
			req:       Request{Mode: ModeFull, Agent: "candidate-planner"},
			wantArgs:  "args: [-p] [--output-format] [json] [--no-session-persistence] [--permission-mode] [bypassPermissions] [--agent] [candidate-planner]\n",
			wantAllow: "true",
		},
		{
			name: "claude safe", output: ok, runner: claude,
			req:       Request{Mode: ModeSafe},
			wantArgs:  "args: [-p] [--output-format] [json] [--no-session-persistence] [--permission-mode] [dontAsk]\n",
			wantAllow: "true",
		},
		{
			name: "claude hides the reviewer's copy", output: ok, runner: claude,
			req:       Request{Mode: ModeFull, Hide: []string{"notes", `we"ird`}},
			wantArgs:  `args: [-p] [--output-format] [json] [--no-session-persistence] [--permission-mode] [bypassPermissions] [--settings] [{"skillOverrides":{"notes":"off","we\"ird":"off"}}]` + "\n",
			wantAllow: "true",
		},
		{
			name: "copilot full", output: "ok", runner: cop,
			req:       Request{Mode: ModeFull, Agent: "current-planner", Hide: []string{"notes"}},
			wantArgs:  "args: [--prompt=p] [-s] [--allow-all] [--agent] [current-planner]\n",
			wantAllow: "true",
		},
		{
			name: "copilot safe", output: "ok", runner: cop,
			req:       Request{Mode: ModeSafe},
			wantArgs:  "args: [--prompt=p] [-s]\n",
			wantAllow: "unset",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			bin, record := fakeTool(t, tc.output)
			tc.req.Dir, tc.req.Prompt = t.TempDir(), "p"
			if _, err := tc.runner(bin).Run(t.Context(), tc.req); err != nil {
				t.Fatalf("Run() error: %v", err)
			}
			rec := readRecord(t, record)
			for _, want := range []string{tc.wantArgs, "token: unset\n", "allow: " + tc.wantAllow + "\n"} {
				if !strings.Contains(rec, want) {
					t.Errorf("the tool saw:\n%s\nwant %q", rec, want)
				}
			}
		})
	}
}

func TestRunnersRefuseNoMode(t *testing.T) {
	bin, record := fakeTool(t, "ok")
	for _, r := range []Runner{NewClaudeCode(bin, nil), NewCopilotCLI(bin, nil)} {
		if _, err := r.Run(t.Context(), Request{Dir: t.TempDir(), Prompt: "p"}); err == nil || !strings.Contains(err.Error(), "mode") {
			t.Errorf("%T.Run() without a mode: error = %v, want refused", r, err)
		}
	}
	if _, err := os.Stat(record); !os.IsNotExist(err) {
		t.Error("a runner started the tool without a mode")
	}
}

func TestCaveats(t *testing.T) {
	cop := NewCopilotCLI("copilot", nil)
	if got := cop.Caveat(ModeSafe); !strings.Contains(got, "refuses every tool call in safe mode") || !strings.Contains(got, "--tool claude") {
		t.Errorf("CopilotCLI.Caveat(safe) = %q", got)
	}
	if got := cop.Caveat(ModeFull); got != "" {
		t.Errorf("CopilotCLI.Caveat(full) = %q, want none", got)
	}
	if _, ok := any(NewClaudeCode("claude", nil)).(Caveat); ok {
		t.Error("ClaudeCode has a caveat, want none: it reads skills in both modes")
	}
}
