package review_test

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
	"sync"
	"testing"
	"time"

	"github.com/petervlad/loadout/internal/pack"
	"github.com/petervlad/loadout/internal/review"
	"github.com/petervlad/loadout/internal/target/claudecode"
	"github.com/petervlad/loadout/internal/target/copilot"
)

// fakeRunner answers with the rendered agent's prompt text, so a test can
// tell which version answered. It never starts a process.
type fakeRunner struct {
	mu       sync.Mutex
	requests []review.Request
	agentDir string // where the tool looks for agents, relative to Dir
	fail     map[string]error
	version  error
	// files are the agent files the runs found, whole.
	files []string
}

func (f *fakeRunner) Version(context.Context) (string, error) {
	if f.version != nil {
		return "", f.version
	}
	return "9.9.9 (fake)", nil
}

func (f *fakeRunner) Run(_ context.Context, req review.Request) (review.Answer, error) {
	f.mu.Lock()
	f.requests = append(f.requests, req)
	f.mu.Unlock()
	if err := f.fail[req.Agent]; err != nil {
		return review.Answer{}, err
	}
	entries, err := os.ReadDir(filepath.Join(req.Dir, f.agentDir))
	if err != nil {
		return review.Answer{}, err
	}
	if len(entries) != 1 || !strings.HasPrefix(entries[0].Name(), req.Agent+".") {
		return review.Answer{}, errors.New("the folder does not hold exactly the requested agent")
	}
	data, err := os.ReadFile(filepath.Join(req.Dir, f.agentDir, entries[0].Name()))
	if err != nil {
		return review.Answer{}, err
	}
	body := string(data)
	f.mu.Lock()
	f.files = append(f.files, body)
	f.mu.Unlock()
	return review.Answer{Text: body[strings.LastIndex(body, "-->")+3:], Duration: 1500 * time.Millisecond, InputTokens: 10, OutputTokens: 5}, nil
}

func newPack(t *testing.T, version, prompt string) *pack.Pack {
	t.Helper()
	p, err := pack.FromFiles(map[string][]byte{
		pack.ManifestFile: []byte("name: lime/planner\nkind: agent\nversion: " + version + "\ndescription: d\n"),
		pack.AgentFile:    []byte("---\nname: planner\ndescription: d\n---\n" + prompt + "\n"),
	})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func newComparer(t *testing.T, claude, cop *fakeRunner) *review.Comparer {
	t.Helper()
	now := func() time.Time { return time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC) }
	return review.NewComparer(slog.New(slog.DiscardHandler), now, t.TempDir(),
		review.Tool{Name: claudecode.Name, Renderer: claudecode.New(), Runner: claude},
		review.Tool{Name: copilot.Name, Renderer: copilot.New(), Runner: cop},
	)
}

func TestCompare(t *testing.T) {
	tests := []struct {
		tool     string
		agentDir string
	}{
		{claudecode.Name, filepath.Join(".claude", "agents")},
		{copilot.Name, filepath.Join(".github", "agents")},
	}
	for _, tc := range tests {
		t.Run(tc.tool, func(t *testing.T) {
			claude := &fakeRunner{agentDir: filepath.Join(".claude", "agents")}
			cop := &fakeRunner{agentDir: filepath.Join(".github", "agents")}
			c := newComparer(t, claude, cop)
			rep, err := c.Compare(t.Context(), review.Options{
				Tool:       tc.tool,
				Current:    newPack(t, "0.1.0", "OLD PROMPT"),
				Candidate:  newPack(t, "0.2.0", "NEW PROMPT"),
				Prompts:    []string{"plan A", "plan B"},
				Runs:       2,
				Timeout:    time.Minute,
				Mode:       review.ModeFull,
				ProposalID: "abc123",
			})
			if err != nil {
				t.Fatalf("Compare() error: %v", err)
			}
			fake := claude
			if tc.tool == copilot.Name {
				fake = cop
			}
			if len(fake.requests) != 8 {
				t.Fatalf("runner got %d requests, want 2 prompts x 2 runs x 2 versions = 8", len(fake.requests))
			}
			a, b := fake.requests[0], fake.requests[1]
			if a.Agent == b.Agent || a.Dir == b.Dir || a.Prompt != "plan A" {
				t.Errorf("requests %+v and %+v share an agent name or folder", a, b)
			}
			if a.Mode != review.ModeFull || len(a.Hide) != 0 {
				t.Errorf("request %+v, want full mode and nothing hidden for an agent", a)
			}
			for _, dir := range []string{a.Dir, b.Dir} {
				if _, err := os.Stat(dir); !errors.Is(err, os.ErrNotExist) {
					t.Errorf("throwaway folder %s still exists: %v", dir, err)
				}
			}
			if rep.Failures() != 0 || rep.ToolVersion != "9.9.9 (fake)" || len(rep.Prompts) != 2 || len(rep.Prompts[0].Runs) != 2 {
				t.Fatalf("report = %+v", rep)
			}
			if got := rep.Prompts[1].Runs[1]; got.Current == nil || !strings.Contains(got.Current.Answer.Text, "OLD PROMPT") || !strings.Contains(got.Candidate.Answer.Text, "NEW PROMPT") {
				t.Errorf("last run = %+v, want the old prompt from current and the new one from the candidate", got)
			}
			md := string(rep.Markdown(256 << 10))
			for _, want := range []string{"# Review of lime/planner 0.2.0 (proposal abc123)", "## Prompt 2", "> plan B", "### Current 0.1.0, run 2", "### Candidate 0.2.0, run 1", "NEW PROMPT", "| 1 | 1.5s | 10/5 | 1.5s | 10/5 |"} {
				if !strings.Contains(md, want) {
					t.Errorf("Markdown() is missing %q:\n%s", want, md)
				}
			}
		})
	}
}

func TestCompareWithoutARelease(t *testing.T) {
	claude := &fakeRunner{agentDir: filepath.Join(".claude", "agents")}
	c := newComparer(t, claude, &fakeRunner{})
	rep, err := c.Compare(t.Context(), review.Options{Tool: "claude", Candidate: newPack(t, "0.1.0", "P"), Prompts: []string{"x"}, Runs: 1, Timeout: time.Minute, Mode: review.ModeFull})
	if err != nil {
		t.Fatal(err)
	}
	if len(claude.requests) != 1 || rep.Current != nil || rep.Prompts[0].Runs[0].Current != nil {
		t.Errorf("Compare() without a release ran %d times, report %+v; want the candidate only", len(claude.requests), rep)
	}
	if md := string(rep.Markdown(1 << 20)); !strings.Contains(md, "none (first release)") {
		t.Errorf("Markdown() = %s, want the missing release named", md)
	}
}

func TestCompareRecordsFailures(t *testing.T) {
	claude := &fakeRunner{agentDir: filepath.Join(".claude", "agents"), fail: map[string]error{"candidate-planner": errors.New("rate limited")}}
	c := newComparer(t, claude, &fakeRunner{})
	rep, err := c.Compare(t.Context(), review.Options{Tool: "claude", Current: newPack(t, "0.1.0", "A"), Candidate: newPack(t, "0.2.0", "B"), Prompts: []string{"x"}, Runs: 1, Timeout: time.Minute, Mode: review.ModeFull})
	if err != nil {
		t.Fatalf("Compare() error = %v, want failures recorded in the report", err)
	}
	if rep.Failures() != 1 || !strings.Contains(string(rep.Markdown(1<<20)), "Failed: rate limited") {
		t.Errorf("report = %+v, want one recorded failure", rep)
	}
}

func TestCompareSetupErrors(t *testing.T) {
	good := review.Options{Tool: "claude", Candidate: newPack(t, "0.2.0", "B"), Prompts: []string{"x"}, Runs: 1, Timeout: time.Minute, Mode: review.ModeFull}
	tests := []struct {
		name   string
		mutate func(*review.Options)
		runner *fakeRunner
		want   string
	}{
		{"unknown tool", func(o *review.Options) { o.Tool = "cursor" }, &fakeRunner{}, "unknown tool"},
		{"tool missing", func(*review.Options) {}, &fakeRunner{version: errors.New("executable file not found")}, "not available"},
		{"too many runs", func(o *review.Options) { o.Runs = review.MaxRuns + 1 }, &fakeRunner{}, "runs"},
		{"no prompts", func(o *review.Options) { o.Prompts = nil }, &fakeRunner{}, "no prompts"},
		{"no mode", func(o *review.Options) { o.Mode = "" }, &fakeRunner{}, "mode"},
		{"unknown mode", func(o *review.Options) { o.Mode = "yolo" }, &fakeRunner{}, "mode"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			opts := good
			tc.mutate(&opts)
			_, err := newComparer(t, tc.runner, &fakeRunner{}).Compare(t.Context(), opts)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("Compare() error = %v, want %q", err, tc.want)
			}
			if len(tc.runner.requests) != 0 {
				t.Errorf("Compare() ran the tool %d times despite a setup error", len(tc.runner.requests))
			}
		})
	}
}

func TestMarkdownFitsTheLimit(t *testing.T) {
	long := strings.Repeat("word ```` ", 20000) // 200 KB with a long backtick run
	rep := review.Report{Tool: "claude", Package: "lime/planner", Candidate: review.Version{Version: "0.2.0"}, Runs: 1}
	for range 5 {
		rep.Prompts = append(rep.Prompts, review.PromptResult{Prompt: "p", Runs: []review.RunPair{{
			Current:   &review.Outcome{Answer: review.Answer{Text: long}},
			Candidate: review.Outcome{Answer: review.Answer{Text: long}},
		}}})
	}
	const limit = 256 << 10
	md := rep.Markdown(limit)
	if len(md) > limit {
		t.Errorf("len(Markdown(%d)) = %d, want at most the limit", limit, len(md))
	}
	if !strings.Contains(string(md), "shortened to fit the report") || !strings.Contains(string(md), "`````text") {
		t.Errorf("Markdown() did not shorten answers or pick a longer fence")
	}
}

func TestJSONFitsTheLimitAndRoundTrips(t *testing.T) {
	long := strings.Repeat("word <b> ", 25000) // 225 KB, and JSON escapes <
	rep := review.Report{Tool: "claude", Package: "lime/planner", Candidate: review.Version{Version: "0.2.0"}, Runs: 1}
	for range 5 {
		rep.Prompts = append(rep.Prompts, review.PromptResult{Prompt: "p", Runs: []review.RunPair{{
			Current:   &review.Outcome{Answer: review.Answer{Text: long, Duration: 1500 * time.Millisecond, InputTokens: 10}},
			Candidate: review.Outcome{Err: long},
		}}})
	}
	const limit = 256 << 10
	data, err := rep.JSON(limit)
	if err != nil {
		t.Fatalf("JSON() error: %v", err)
	}
	if len(data) > limit {
		t.Errorf("len(JSON(%d)) = %d, want at most the limit", limit, len(data))
	}
	var got review.Report
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("JSON() does not decode: %v", err)
	}
	if len(got.Prompts) != 5 || got.Prompts[0].Runs[0].Current.Answer.Duration != 1500*time.Millisecond || got.Prompts[0].Runs[0].Current.Answer.InputTokens != 10 {
		t.Errorf("JSON() round trip = %+v, want 5 prompts with timings", got.Prompts[0])
	}
	if a := got.Prompts[0].Runs[0].Current.Answer.Text; !strings.HasSuffix(a, "shortened to fit the report]") {
		t.Errorf("JSON() answer ends %q, want it shortened", a[max(0, len(a)-40):])
	}
	if rep.Prompts[0].Runs[0].Current.Answer.Text != long {
		t.Error("JSON() changed the report it encoded")
	}
}

// skillRunner stands in for a tool that finds skills on its own: it
// answers with the instructions of the one skill folder in the project.
type skillRunner struct {
	mu       sync.Mutex
	requests []review.Request
	folders  []string // the skill folder each run saw
}

func (f *skillRunner) Version(context.Context) (string, error) { return "1.0.0 (fake)", nil }

func (f *skillRunner) Run(_ context.Context, req review.Request) (review.Answer, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.requests = append(f.requests, req)
	skills := filepath.Join(req.Dir, ".claude", "skills")
	entries, err := os.ReadDir(skills)
	if err != nil {
		return review.Answer{}, err
	}
	if len(entries) != 1 || !entries[0].IsDir() {
		return review.Answer{}, errors.New("the project does not hold exactly one skill folder")
	}
	name := entries[0].Name()
	f.folders = append(f.folders, name)
	data, err := os.ReadFile(filepath.Join(skills, name, "SKILL.md"))
	if err != nil {
		return review.Answer{}, err
	}
	if !strings.Contains(string(data), "\nname: "+name+"\n") {
		return review.Answer{}, fmt.Errorf("SKILL.md in %s names another skill:\n%s", name, data)
	}
	if _, err := os.Stat(filepath.Join(skills, name, "references", "format.md")); err != nil {
		return review.Answer{}, err
	}
	body := string(data)
	return review.Answer{Text: body[strings.LastIndex(body, "-->")+3:]}, nil
}

func newSkillPack(t *testing.T, version, body string) *pack.Pack {
	t.Helper()
	p, err := pack.FromFiles(map[string][]byte{
		pack.ManifestFile:      []byte("name: lime/notes\nkind: skill\nversion: " + version + "\ndescription: d\n"),
		"SKILL.md":             []byte("---\nname: notes\ndescription: Take notes.\n---\n" + body + "\n"),
		"references/format.md": []byte("# Format\n"),
	})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestCompareSkill(t *testing.T) {
	for _, tool := range []string{claudecode.Name, copilot.Name} {
		t.Run(tool, func(t *testing.T) {
			runner := &skillRunner{}
			now := func() time.Time { return time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC) }
			c := review.NewComparer(slog.New(slog.DiscardHandler), now, t.TempDir(),
				review.Tool{Name: claudecode.Name, Renderer: claudecode.New(), Runner: runner},
				review.Tool{Name: copilot.Name, Renderer: copilot.New(), Runner: runner},
			)
			rep, err := c.Compare(t.Context(), review.Options{
				Tool:      tool,
				Current:   newSkillPack(t, "0.1.0", "OLD STEPS"),
				Candidate: newSkillPack(t, "0.2.0", "NEW STEPS"),
				Prompts:   []string{"take notes"},
				Runs:      1,
				Timeout:   time.Minute,
				Mode:      review.ModeFull,
			})
			if err != nil {
				t.Fatalf("Compare() error: %v", err)
			}
			if rep.Failures() != 0 {
				t.Fatalf("report has failures: %s", rep.Markdown(1<<20))
			}
			for _, req := range runner.requests {
				if req.Agent != "" {
					t.Errorf("request %+v names an agent, want none for a skill", req)
				}
				if !slices.Equal(req.Hide, []string{"notes"}) {
					t.Errorf("request hides %q, want the reviewer's own copy of notes hidden", req.Hide)
				}
			}
			if want := []string{"current-notes", "candidate-notes"}; !slices.Equal(runner.folders, want) {
				t.Errorf("skill folders = %v, want %v", runner.folders, want)
			}
			run := rep.Prompts[0].Runs[0]
			if run.Current == nil || !strings.Contains(run.Current.Answer.Text, "OLD STEPS") || !strings.Contains(run.Candidate.Answer.Text, "NEW STEPS") {
				t.Errorf("run = %+v, want each version's own instructions", run)
			}
		})
	}
}
