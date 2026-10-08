package review

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// Output limits for an AI tool's process. An answer longer than this is an
// error rather than an unbounded buffer.
const (
	maxStdout = 4 << 20
	maxStderr = 64 << 10
	// waitDelay is how long a cancelled tool gets to exit after being killed
	// before its pipes are closed.
	waitDelay = 5 * time.Second
)

// ClaudeCode runs prompts with the Claude Code CLI:
//
//	claude -p --output-format json --no-session-persistence --permission-mode MODE [--settings JSON] [--agent NAME] [--model M]
//
// The prompt goes to standard input, never onto the command line. The
// working directory is the throwaway project folder, so --agent finds the
// rendered subagent in .claude/agents. --agent runs the whole session as
// that agent (its prompt, tools and model), which is what a teammate gets
// when they pick it. In full mode bypassPermissions lets the agent do what
// it would do for the reviewer; in safe mode dontAsk refuses any tool call
// that would need approval, since nobody is there to approve it.
// --no-session-persistence keeps review runs out of the reviewer's session
// history. --settings turns off the skills in Request.Hide for this run
// only and changes no file.
type ClaudeCode struct {
	bin string
	env []string
}

var _ Runner = (*ClaudeCode)(nil)

// NewClaudeCode returns a Claude Code runner. bin is the executable,
// normally "claude"; env is the environment the tool runs with (os.Environ
// in production), with loadout's own variables removed.
func NewClaudeCode(bin string, env []string) *ClaudeCode {
	return &ClaudeCode{bin: bin, env: withoutLoadoutEnv(env)}
}

// Version implements Runner.
func (c *ClaudeCode) Version(ctx context.Context) (string, error) {
	return toolVersion(ctx, c.bin, c.env, "--version")
}

// claudePermissionModes maps a Mode to Claude Code's --permission-mode.
var claudePermissionModes = map[Mode]string{
	ModeFull: "bypassPermissions",
	ModeSafe: "dontAsk",
}

// hideSettings returns the --settings JSON that turns the named skills off.
// Claude Code runs a personal skill (~/.claude/skills) over a project skill
// of the same name; the sides are rendered as current-NAME and
// candidate-NAME, so turning NAME off hides only the reviewer's own copy.
// The names are JSON-encoded, never spliced in as text.
func hideSettings(names []string) (string, error) {
	var s struct {
		SkillOverrides map[string]string `json:"skillOverrides"`
	}
	s.SkillOverrides = make(map[string]string, len(names))
	for _, n := range names {
		s.SkillOverrides[n] = "off"
	}
	data, err := json.Marshal(s)
	if err != nil {
		return "", fmt.Errorf("claude settings: %w", err)
	}
	return string(data), nil
}

// claudeResult is the part of `claude -p --output-format json` loadout reads.
type claudeResult struct {
	Type       string `json:"type"`
	Subtype    string `json:"subtype"`
	IsError    bool   `json:"is_error"`
	Result     string `json:"result"`
	DurationMS int64  `json:"duration_ms"`
	Usage      struct {
		InputTokens              int `json:"input_tokens"`
		CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
		CacheReadInputTokens     int `json:"cache_read_input_tokens"`
		OutputTokens             int `json:"output_tokens"`
	} `json:"usage"`
}

// Run implements Runner.
func (c *ClaudeCode) Run(ctx context.Context, req Request) (Answer, error) {
	perm, ok := claudePermissionModes[req.Mode]
	if !ok {
		return Answer{}, fmt.Errorf("claude: mode %q: want %s or %s", req.Mode, ModeFull, ModeSafe)
	}
	args := []string{"-p", "--output-format", "json", "--no-session-persistence", "--permission-mode", perm}
	if len(req.Hide) > 0 {
		settings, err := hideSettings(req.Hide)
		if err != nil {
			return Answer{}, err
		}
		args = append(args, "--settings", settings)
	}
	if req.Agent != "" {
		args = append(args, "--agent", req.Agent)
	}
	if req.Model != "" {
		args = append(args, "--model", req.Model)
	}
	start := time.Now()
	out, runErr := runTool(ctx, c.bin, args, c.env, req.Dir, req.Prompt)
	var res claudeResult
	if runErr != nil {
		// Claude Code exits non-zero on errors such as "Not logged in" but
		// still prints its JSON result, which says why.
		if json.Unmarshal(out, &res) == nil && res.IsError && res.Result != "" {
			return Answer{}, fmt.Errorf("claude reported an error: %s", firstLine(res.Result))
		}
		return Answer{}, runErr
	}
	if err := json.Unmarshal(out, &res); err != nil {
		return Answer{}, fmt.Errorf("read claude output: %w", err)
	}
	if res.IsError || res.Type != "result" {
		return Answer{}, fmt.Errorf("claude reported %s: %s", cmp.Or(res.Subtype, "an error"), firstLine(res.Result))
	}
	d := time.Duration(res.DurationMS) * time.Millisecond
	if d == 0 {
		d = time.Since(start)
	}
	return Answer{
		Text:         res.Result,
		Duration:     d,
		InputTokens:  res.Usage.InputTokens + res.Usage.CacheCreationInputTokens + res.Usage.CacheReadInputTokens,
		OutputTokens: res.Usage.OutputTokens,
	}, nil
}

// CopilotCLI runs prompts with GitHub Copilot CLI:
//
//	copilot --prompt=PROMPT -s [--allow-all] [--agent NAME] [--model M]
//
// Copilot CLI loads project agents from .github/agents in the working
// directory. -s prints only the answer, so it reports no token counts. In
// full mode --allow-all allows every tool, path and URL. In safe mode no
// allow flag is passed and COPILOT_ALLOW_ALL is dropped from the
// environment, since that variable would silently allow every tool again;
// Copilot then refuses every tool call (see Caveat). Copilot takes the
// prompt only as an argument; prompts are test prompts, never secrets. It
// has no known per-run way to hide one skill, so Request.Hide is ignored.
type CopilotCLI struct {
	bin string
	env []string
}

var (
	_ Runner = (*CopilotCLI)(nil)
	_ Caveat = (*CopilotCLI)(nil)
)

// copilotSafeCaveat is the notice for a safe-mode Copilot run. Copilot's
// help says --allow-all-tools is required for prompt mode.
const copilotSafeCaveat = "Copilot CLI refuses every tool call in safe mode, so the agent answers from the prompt alone: it cannot read skills, knowledge bases or files. Run without --safe, or with --tool claude, for a real review."

// NewCopilotCLI returns a Copilot CLI runner. bin is normally "copilot".
func NewCopilotCLI(bin string, env []string) *CopilotCLI {
	return &CopilotCLI{bin: bin, env: withoutLoadoutEnv(env)}
}

// Version implements Runner.
func (c *CopilotCLI) Version(ctx context.Context) (string, error) {
	return toolVersion(ctx, c.bin, c.env, "--version")
}

// Caveat implements Caveat.
func (c *CopilotCLI) Caveat(m Mode) string {
	if m == ModeSafe {
		return copilotSafeCaveat
	}
	return ""
}

// Run implements Runner.
func (c *CopilotCLI) Run(ctx context.Context, req Request) (Answer, error) {
	// --prompt=TEXT keeps a prompt that starts with "-" from reading as a flag.
	args := []string{"--prompt=" + req.Prompt, "-s"}
	env := c.env
	switch req.Mode {
	case ModeFull:
		args = append(args, "--allow-all")
	case ModeSafe:
		env = without(env, "COPILOT_ALLOW_ALL=")
	default:
		return Answer{}, fmt.Errorf("copilot: mode %q: want %s or %s", req.Mode, ModeFull, ModeSafe)
	}
	if req.Agent != "" {
		args = append(args, "--agent", req.Agent)
	}
	if req.Model != "" {
		args = append(args, "--model", req.Model)
	}
	start := time.Now()
	out, err := runTool(ctx, c.bin, args, env, req.Dir, "")
	if err != nil {
		return Answer{}, err
	}
	return Answer{Text: strings.TrimSpace(string(out)), Duration: time.Since(start)}, nil
}

func toolVersion(ctx context.Context, bin string, env []string, args ...string) (string, error) {
	out, err := runTool(ctx, bin, args, env, "", "")
	if err != nil {
		return "", err
	}
	return firstLine(string(out)), nil
}

// errOutputTooLarge is returned when a tool writes more than the limit.
var errOutputTooLarge = errors.New("output is too large")

// runTool runs bin and returns its standard output. When bin exits with an
// error it returns the output so far too, since some tools explain the
// error there. The process is killed when ctx ends. stdin may be empty.
func runTool(ctx context.Context, bin string, args, env []string, dir, stdin string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Env = env
	cmd.Dir = dir
	cmd.Stdin = strings.NewReader(stdin)
	stdout := &limitedBuffer{limit: maxStdout}
	stderr := &limitedBuffer{limit: maxStderr}
	cmd.Stdout, cmd.Stderr = stdout, stderr
	cmd.WaitDelay = waitDelay
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return nil, fmt.Errorf("%s: %w", bin, ctx.Err())
		}
		if msg := lastLines(stderr.String(), 3); msg != "" {
			return stdout.Bytes(), fmt.Errorf("%s: %w: %s", bin, err, msg)
		}
		return stdout.Bytes(), fmt.Errorf("%s: %w", bin, err)
	}
	if stdout.over {
		return nil, fmt.Errorf("%s: %w: over %d bytes", bin, errOutputTooLarge, maxStdout)
	}
	return stdout.Bytes(), nil
}

// withoutLoadoutEnv drops LOADOUT_* variables, so a registry token in
// LOADOUT_TOKEN never reaches the AI tool or the agent under review.
func withoutLoadoutEnv(env []string) []string { return without(env, "LOADOUT_") }

// without returns a copy of env without the entries that start with prefix.
func without(env []string, prefix string) []string {
	out := make([]string, 0, len(env))
	for _, kv := range env {
		if !strings.HasPrefix(kv, prefix) {
			out = append(out, kv)
		}
	}
	return out
}

// limitedBuffer keeps the first limit bytes written and notes the rest.
type limitedBuffer struct {
	buf   bytes.Buffer
	limit int
	over  bool
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	n := len(p)
	room := b.limit - b.buf.Len()
	if n > room {
		b.over = true
		p = p[:max(0, room)]
	}
	b.buf.Write(p) // bytes.Buffer.Write never fails
	return n, nil  // the rest is dropped on purpose; the process must not block
}

func (b *limitedBuffer) Bytes() []byte  { return b.buf.Bytes() }
func (b *limitedBuffer) String() string { return b.buf.String() }

func firstLine(s string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(s), "\n")
	return line
}

func lastLines(s string, n int) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, " | ")
}
