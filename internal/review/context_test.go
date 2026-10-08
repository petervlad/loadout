package review_test

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"iter"
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

// treeRunner records every file in the folder it runs in, so a test can
// see what sat beside each side. It never starts a process.
type treeRunner struct {
	mu     sync.Mutex
	trees  map[string][]string // by agent name: slash paths under Dir
	agents map[string]string   // by agent name: the agent file's content
	caveat string
}

func (f *treeRunner) Version(context.Context) (string, error) { return "1.0.0 (fake)", nil }

func (f *treeRunner) Run(_ context.Context, req review.Request) (review.Answer, error) {
	var tree []string
	err := filepath.WalkDir(req.Dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, err := filepath.Rel(req.Dir, p)
		tree = append(tree, filepath.ToSlash(rel))
		return err
	})
	if err != nil {
		return review.Answer{}, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.trees[req.Agent] = tree
	for _, p := range tree {
		if strings.HasSuffix(p, "/"+req.Agent+".md") || strings.HasSuffix(p, "/"+req.Agent+".agent.md") {
			data, err := os.ReadFile(filepath.Join(req.Dir, p))
			if err != nil {
				return review.Answer{}, err
			}
			f.agents[req.Agent] = string(data)
		}
	}
	return review.Answer{Text: "ok"}, nil
}

// caveatRunner is a treeRunner whose tool has something to say in safe mode.
type caveatRunner struct{ *treeRunner }

func (c caveatRunner) Caveat(m review.Mode) string {
	if m == review.ModeSafe {
		return c.caveat
	}
	return ""
}

func newTreeRunner() *treeRunner {
	return &treeRunner{trees: map[string][]string{}, agents: map[string]string{}}
}

func packOf(t *testing.T, files map[string]string) *pack.Pack {
	t.Helper()
	in := make(map[string][]byte, len(files))
	for k, v := range files {
		in[k] = []byte(v)
	}
	p, err := pack.FromFiles(in)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func agentPack(t *testing.T, id, prompt string) *pack.Pack {
	t.Helper()
	name := id[strings.Index(id, "/")+1:]
	return packOf(t, map[string]string{
		pack.ManifestFile: "name: " + id + "\nkind: agent\nversion: 1.0.0\ndescription: d\n",
		pack.AgentFile:    "---\nname: " + name + "\ndescription: d\n---\n" + prompt + "\n",
	})
}

// teamPackages is a KB, a skill, an agent, and an agent whose name would
// take the candidate's place.
func teamPackages(t *testing.T) []*pack.Pack {
	t.Helper()
	return []*pack.Pack{
		packOf(t, map[string]string{
			pack.ManifestFile: "name: lime/ops\nkind: kb\nversion: 1.0.0\ndescription: How we run production.\n",
			"README.md":       "# Overview\n",
			"runbooks/a.md":   "# Restart\n",
		}),
		packOf(t, map[string]string{
			pack.ManifestFile: "name: lime/notes\nkind: skill\nversion: 2.0.0\ndescription: Notes.\n",
			"SKILL.md":        "---\nname: notes\ndescription: Write notes.\n---\nWrite them.\n",
		}),
		agentPack(t, "lime/helper", "HELP"),
		agentPack(t, "lime/candidate-planner", "IMPOSTOR"),
	}
}

func seqOf(ps ...*pack.Pack) iter.Seq2[*pack.Pack, error] {
	return func(yield func(*pack.Pack, error) bool) {
		for _, p := range ps {
			if !yield(p, nil) {
				return
			}
		}
	}
}

func treeComparer(t *testing.T, r review.Runner) (*review.Comparer, string) {
	t.Helper()
	tmp := t.TempDir()
	now := func() time.Time { return time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC) }
	return review.NewComparer(slog.New(slog.DiscardHandler), now, tmp,
		review.Tool{Name: claudecode.Name, Renderer: claudecode.New(), Runner: r},
		review.Tool{Name: copilot.Name, Renderer: copilot.New(), Runner: r},
	), tmp
}

func TestCompareRendersContextIntoBothSides(t *testing.T) {
	for _, tool := range []string{claudecode.Name, copilot.Name} {
		t.Run(tool, func(t *testing.T) {
			runner := newTreeRunner()
			c, _ := treeComparer(t, runner)
			rep, err := c.Compare(t.Context(), review.Options{
				Tool: tool, Mode: review.ModeFull,
				Current:   newPack(t, "0.1.0", "OLD PROMPT"),
				Candidate: newPack(t, "0.2.0", "NEW PROMPT"),
				Context:   seqOf(teamPackages(t)...),
				Prompts:   []string{"p"}, Runs: 1, Timeout: time.Minute,
			})
			if err != nil {
				t.Fatalf("Compare() error: %v", err)
			}
			cur, cand := runner.trees["current-planner"], runner.trees["candidate-planner"]
			if len(cur) == 0 || len(cand) == 0 {
				t.Fatalf("trees = %v, want one per side", runner.trees)
			}
			others := func(tree []string, own string) []string {
				return slices.DeleteFunc(slices.Clone(tree), func(p string) bool { return strings.Contains(p, own) })
			}
			if a, b := others(cur, "current-planner"), others(cand, "candidate-planner"); !slices.Equal(a, b) || len(a) < 4 {
				t.Errorf("team packages differ between the sides:\ncurrent   %v\ncandidate %v", a, b)
			}
			if got := runner.agents["candidate-planner"]; !strings.Contains(got, "NEW PROMPT") || strings.Contains(got, "IMPOSTOR") {
				t.Errorf("candidate agent = %q, want the candidate's own, never a team package", got)
			}
			var ids []string
			for _, p := range rep.Context {
				ids = append(ids, p.ID+"@"+p.Version+":"+p.Kind)
			}
			if want := []string{"lime/ops@1.0.0:kb", "lime/notes@2.0.0:skill", "lime/helper@1.0.0:agent"}; !slices.Equal(ids, want) {
				t.Errorf("Report.Context = %v, want %v", ids, want)
			}
			if len(rep.Notes) != 1 || !strings.Contains(rep.Notes[0], "lime/candidate-planner was left out") {
				t.Errorf("Report.Notes = %q, want the clashing package named", rep.Notes)
			}
			md := string(rep.Markdown(1 << 20))
			for _, want := range []string{"Both sides ran beside 3 team package(s):", "- `lime/ops` 1.0.0 (KB)", "- `lime/helper` 1.0.0 (agent)"} {
				if !strings.Contains(md, want) {
					t.Errorf("Markdown() is missing %q:\n%s", want, md)
				}
			}
		})
	}
}

func TestCompareContextLimits(t *testing.T) {
	big := packOf(t, map[string]string{
		pack.ManifestFile: "name: lime/wiki\nkind: kb\nversion: 1.0.0\ndescription: The wiki.\n",
		"README.md":       strings.Repeat("words ", 200),
	})
	failing := func(yield func(*pack.Pack, error) bool) {
		if yield(agentPack(t, "lime/helper", "H"), nil) {
			yield(nil, errors.New("digest mismatch"))
		}
	}
	tests := []struct {
		name     string
		packages int
		bytes    int64
		seq      iter.Seq2[*pack.Pack, error]
		want     string
	}{
		{"too many packages", 2, 1 << 20, seqOf(teamPackages(t)[:3]...), "more than 2 packages"},
		{"too many bytes", 100, 1000, seqOf(agentPack(t, "lime/helper", "H"), big), "more than 1000 bytes"},
		{"a fetch fails", 100, 1 << 20, failing, "digest mismatch"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			runner := newTreeRunner()
			c, tmp := treeComparer(t, runner)
			c.SetContextLimits(tc.packages, tc.bytes)
			_, err := c.Compare(t.Context(), review.Options{
				Tool: claudecode.Name, Mode: review.ModeSafe,
				Current: newPack(t, "0.1.0", "A"), Candidate: newPack(t, "0.2.0", "B"),
				Context: tc.seq, Prompts: []string{"p"}, Runs: 1, Timeout: time.Minute,
			})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Compare() error = %v, want %q", err, tc.want)
			}
			if tc.name != "a fetch fails" && !strings.Contains(err.Error(), "--no-context") {
				t.Errorf("Compare() error = %v, want it to name --no-context", err)
			}
			if len(runner.trees) != 0 {
				t.Errorf("Compare() ran the tool despite the limit")
			}
			if left, _ := os.ReadDir(tmp); len(left) != 0 {
				t.Errorf("throwaway folders left behind: %v", left)
			}
		})
	}
}

func TestCompareNotes(t *testing.T) {
	const caveat = "This tool cannot read files in safe mode."
	tests := []struct {
		mode review.Mode
		want []string
	}{
		{review.ModeSafe, []string{"You have no install.", caveat}},
		{review.ModeFull, []string{"You have no install."}},
	}
	for _, tc := range tests {
		t.Run(string(tc.mode), func(t *testing.T) {
			runner := newTreeRunner()
			runner.caveat = caveat
			c, _ := treeComparer(t, caveatRunner{runner})
			got, err := c.Caveat(claudecode.Name, tc.mode)
			if err != nil || (got != "") != (tc.mode == review.ModeSafe) {
				t.Errorf("Comparer.Caveat(%s) = %q, %v", tc.mode, got, err)
			}
			rep, err := c.Compare(t.Context(), review.Options{
				Tool: claudecode.Name, Mode: tc.mode, Notes: []string{"You have no install."},
				Candidate: newPack(t, "0.2.0", "B"), Prompts: []string{"p"}, Runs: 1, Timeout: time.Minute,
			})
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(rep.Notes, tc.want) || rep.Mode != tc.mode {
				t.Errorf("report notes %q, mode %q; want %q, %q", rep.Notes, rep.Mode, tc.want, tc.mode)
			}
			md := string(rep.Markdown(1 << 20))
			last := "**Note:** " + tc.want[len(tc.want)-1]
			if i := strings.Index(md, last); i < 0 || i > strings.Index(md, "Run with") {
				t.Errorf("Markdown() does not open with the notes:\n%s", md)
			}
		})
	}
}

func TestComparerCaveatUnknownTool(t *testing.T) {
	c, _ := treeComparer(t, newTreeRunner())
	if _, err := c.Caveat("cursor", review.ModeSafe); !errors.Is(err, review.ErrUnknownTool) {
		t.Errorf("Caveat(cursor) error = %v, want ErrUnknownTool", err)
	}
}

func TestReportMarkdownSides(t *testing.T) {
	answer := review.Outcome{Answer: review.Answer{Text: "hi"}}
	rep := review.Report{
		Tool: "claude", Package: "lime/planner", Runs: 1, Mode: review.ModeFull,
		Current:   &review.Version{Version: "1.2.0", Source: review.SourceInstalled},
		Candidate: review.Version{Version: "1.4.0", Source: review.SourceProposal, ProposalID: "fd9af97ec0b9"},
		Context:   []review.ContextPackage{{ID: "lime/ops", Version: "1.0.0", Kind: "kb"}},
		Prompts:   []review.PromptResult{{Prompt: "p", Runs: []review.RunPair{{Current: &answer, Candidate: answer}}}},
	}
	md := string(rep.Markdown(1 << 20))
	for _, want := range []string{
		"| Side | Installed 1.2.0 | Proposal fd9af97ec0b9 (1.4.0) |",
		"### Installed 1.2.0, run 1",
		"### Proposal fd9af97ec0b9 (1.4.0), run 1",
		"Mode: full access.",
		"- `lime/ops` 1.0.0 (KB)",
	} {
		if !strings.Contains(md, want) {
			t.Errorf("Markdown() is missing %q:\n%s", want, md)
		}
	}
	rep.Current.Source = review.SourceRelease
	if md := string(rep.Markdown(1 << 20)); !strings.Contains(md, "| Side | Release 1.2.0 |") {
		t.Errorf("Markdown() with a release side:\n%s", md)
	}
}

func TestOldReportStillDecodes(t *testing.T) {
	const old = `{"tool":"claude","tool_version":"2.1","package":"lime/planner",
		"current":{"version":"0.1.0","digest":"sha256:aa"},"candidate":{"version":"0.2.0","digest":"sha256:bb"},
		"runs":1,"prompts":[{"prompt":"p","runs":[{"current":{"answer":{"text":"a","duration_ns":1}},"candidate":{"answer":{"text":"b","duration_ns":1}}}]}],
		"generated_at":"2026-09-01T10:00:00Z"}`
	var rep review.Report
	if err := json.Unmarshal([]byte(old), &rep); err != nil {
		t.Fatalf("old report does not decode: %v", err)
	}
	if rep.Mode != "" || rep.Current.Source != "" || rep.Context != nil || rep.Notes != nil {
		t.Errorf("old report = %+v, want the new fields empty", rep)
	}
	md := string(rep.Markdown(1 << 20))
	for _, want := range []string{"### Current 0.1.0, run 1", "### Candidate 0.2.0, run 1"} {
		if !strings.Contains(md, want) {
			t.Errorf("Markdown() is missing %q:\n%s", want, md)
		}
	}
	for _, unwanted := range []string{"| Side |", "Mode:", "team package", "**Note:**"} {
		if strings.Contains(md, unwanted) {
			t.Errorf("Markdown() of an old report shows %q:\n%s", unwanted, md)
		}
	}
	data, err := json.Marshal(rep)
	if err != nil || strings.Contains(string(data), `"mode"`) || strings.Contains(string(data), `"source"`) {
		t.Errorf("re-encoded old report = %s, %v; want no new fields", data, err)
	}
}
