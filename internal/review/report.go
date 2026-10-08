package review

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/petervlad/loadout/internal/pack"
)

// Report is the result of one comparison. Uploaded with --upload, it also
// travels as JSON next to the Markdown, so the registry's web pages can
// show the answers side by side.
type Report struct {
	Tool        string `json:"tool"`
	ToolVersion string `json:"tool_version"`
	Model       string `json:"model,omitempty"`
	ProposalID  string `json:"proposal_id,omitempty"`
	Package     string `json:"package"`
	// Current is nil when the package had no release to compare with.
	Current     *Version       `json:"current,omitempty"`
	Candidate   Version        `json:"candidate"`
	Runs        int            `json:"runs"`
	Prompts     []PromptResult `json:"prompts"`
	GeneratedAt time.Time      `json:"generated_at"`
	// Mode is the access the tool had; empty in reports made before modes.
	Mode Mode `json:"mode,omitempty"`
	// Context lists the team packages that sat beside each side.
	Context []ContextPackage `json:"context,omitempty"`
	// Notes are things a reader must know to judge the answers, such as a
	// tool that could not use any tool in safe mode.
	Notes []string `json:"notes,omitempty"`
}

// Version identifies one side of the comparison.
type Version struct {
	Version string `json:"version"`
	Digest  string `json:"digest"`
	// Source says where the side came from: SourceRelease,
	// SourceInstalled or SourceProposal; empty in old reports, which read
	// as a release.
	Source     string `json:"source,omitempty"`
	ProposalID string `json:"proposal_id,omitempty"`
}

// label names the side for a reader, such as "Installed 1.2.0" or
// "Proposal fd9af97ec0b9 (1.4.0)". Without a source it keeps the old
// "Current 1.2.0" style, with fallback as the word.
func (v Version) label(fallback string) string {
	switch v.Source {
	case SourceInstalled:
		return "Installed " + v.Version
	case SourceRelease:
		return "Release " + v.Version
	case SourceProposal:
		if v.ProposalID != "" {
			return fmt.Sprintf("Proposal %s (%s)", v.ProposalID, v.Version)
		}
		return "Proposal " + v.Version
	}
	return fallback + " " + v.Version
}

// ContextPackage is one team package rendered beside both sides.
type ContextPackage struct {
	ID      string `json:"id"`
	Version string `json:"version"`
	Kind    string `json:"kind"`
}

// PromptResult holds every run of one prompt.
type PromptResult struct {
	Prompt string    `json:"prompt"`
	Runs   []RunPair `json:"runs"`
}

// RunPair is one run of a prompt against each version. Current is nil when
// there was no current release.
type RunPair struct {
	Current   *Outcome `json:"current,omitempty"`
	Candidate Outcome  `json:"candidate"`
}

// Outcome is an answer, or the error that prevented one.
type Outcome struct {
	Answer Answer `json:"answer"`
	Err    string `json:"error,omitempty"`
}

// Failures counts the runs that produced no answer.
func (r Report) Failures() int {
	n := 0
	for _, p := range r.Prompts {
		for _, run := range p.Runs {
			if run.Current != nil && run.Current.Err != "" {
				n++
			}
			if run.Candidate.Err != "" {
				n++
			}
		}
	}
	return n
}

// Markdown renders the report. Answers are shortened as needed to keep the
// whole report within maxBytes; if even minimal answers do not fit, the
// report is returned over the limit and the caller decides.
func (r Report) Markdown(maxBytes int) []byte {
	answers := 0
	for _, p := range r.Prompts {
		answers += len(p.Runs) * 2
	}
	budget := maxBytes
	for {
		out := r.render(budget)
		if len(out) <= maxBytes || budget <= 256 || answers == 0 {
			return out
		}
		budget = min(budget/2, maxBytes/answers)
	}
}

// JSON encodes the report, shortening answers and errors as needed to keep
// it within maxBytes, like Markdown. If even minimal answers do not fit,
// the result is over the limit and the caller decides.
func (r Report) JSON(maxBytes int) ([]byte, error) {
	answers := 0
	for _, p := range r.Prompts {
		answers += len(p.Runs) * 2
	}
	budget := maxBytes
	for {
		out, err := json.Marshal(r.shortened(budget))
		if err != nil {
			return nil, fmt.Errorf("encode report: %w", err)
		}
		if len(out) <= maxBytes || budget <= 256 || answers == 0 {
			return out, nil
		}
		budget = min(budget/2, maxBytes/answers)
	}
}

// shortened returns a copy of r with every answer and error cut to budget
// bytes.
func (r Report) shortened(budget int) Report {
	cut := func(o Outcome) Outcome {
		o.Answer.Text = truncate(o.Answer.Text, budget)
		o.Err = truncate(o.Err, budget)
		return o
	}
	out := r
	out.Prompts = make([]PromptResult, len(r.Prompts))
	for i, p := range r.Prompts {
		runs := make([]RunPair, len(p.Runs))
		for j, run := range p.Runs {
			runs[j].Candidate = cut(run.Candidate)
			if run.Current != nil {
				c := cut(*run.Current)
				runs[j].Current = &c
			}
		}
		out.Prompts[i] = PromptResult{Prompt: p.Prompt, Runs: runs}
	}
	return out
}

func (r Report) render(answerBudget int) []byte {
	var b strings.Builder
	fmt.Fprintf(&b, "# Review of %s %s", r.Package, r.Candidate.Version)
	if r.ProposalID != "" {
		fmt.Fprintf(&b, " (proposal %s)", r.ProposalID)
	}
	b.WriteString("\n\n")
	for _, n := range r.Notes {
		fmt.Fprintf(&b, "**Note:** %s\n\n", n)
	}

	model := r.Model
	if model == "" {
		model = "tool default"
	}
	fmt.Fprintf(&b, "Run with %s %s, model: %s, %d run(s) per prompt, on %s. ", r.Tool, r.ToolVersion, model, r.Runs, r.GeneratedAt.Format("2006-01-02 15:04 MST"))
	b.WriteString("Generated by `loadout review` on the reviewer's machine with the reviewer's own AI tool.\n\n")
	if m := modeText[r.Mode]; m != "" {
		b.WriteString(m + "\n\n")
	}

	b.WriteString("| | Current | Candidate |\n| --- | --- | --- |\n")
	cur := Version{Version: "none (first release)", Digest: "-"}
	if r.Current != nil {
		cur = *r.Current
	}
	if cur.Source != "" || r.Candidate.Source != "" {
		fmt.Fprintf(&b, "| Side | %s | %s |\n", cur.label("Current"), r.Candidate.label("Candidate"))
	}
	fmt.Fprintf(&b, "| Version | %s | %s |\n", cur.Version, r.Candidate.Version)
	fmt.Fprintf(&b, "| Digest | %s | %s |\n", pack.Digest(cur.Digest).Short(), pack.Digest(r.Candidate.Digest).Short())
	r.renderContext(&b)
	if n := r.Failures(); n > 0 {
		fmt.Fprintf(&b, "\n**%d run(s) failed**; see below.\n", n)
	}

	for i, p := range r.Prompts {
		fmt.Fprintf(&b, "\n## Prompt %d\n\n", i+1)
		for line := range strings.SplitSeq(p.Prompt, "\n") {
			b.WriteString("> " + line + "\n")
		}
		b.WriteString("\n| Run | Current time | Current tokens (in/out) | Candidate time | Candidate tokens (in/out) |\n| --- | --- | --- | --- | --- |\n")
		for j, run := range p.Runs {
			ct, ck := "-", "-"
			if run.Current != nil {
				ct, ck = timing(*run.Current), tokens(*run.Current)
			}
			fmt.Fprintf(&b, "| %d | %s | %s | %s | %s |\n", j+1, ct, ck, timing(run.Candidate), tokens(run.Candidate))
		}
		for j, run := range p.Runs {
			if run.Current != nil {
				writeAnswer(&b, fmt.Sprintf("%s, run %d", cur.label("Current"), j+1), *run.Current, answerBudget)
			}
			writeAnswer(&b, fmt.Sprintf("%s, run %d", r.Candidate.label("Candidate"), j+1), run.Candidate, answerBudget)
		}
	}
	return []byte(b.String())
}

// modeText says what a mode let the tool do.
var modeText = map[Mode]string{
	ModeFull: "Mode: full access. The tool could read and change files, run commands and reach the web, as it does for the reviewer.",
	ModeSafe: "Mode: safe. The tool refused every action that would have needed approval.",
}

// renderContext lists the team packages both sides ran beside.
func (r Report) renderContext(b *strings.Builder) {
	if len(r.Context) == 0 {
		return
	}
	fmt.Fprintf(b, "\nBoth sides ran beside %d team package(s):\n\n", len(r.Context))
	for _, p := range r.Context {
		fmt.Fprintf(b, "- `%s` %s (%s)\n", p.ID, p.Version, pack.Kind(p.Kind).Describe())
	}
}

func timing(o Outcome) string {
	if o.Err != "" {
		return "failed"
	}
	return o.Answer.Duration.Round(100 * time.Millisecond).String()
}

func tokens(o Outcome) string {
	if o.Err != "" || (o.Answer.InputTokens == 0 && o.Answer.OutputTokens == 0) {
		return "-"
	}
	return fmt.Sprintf("%d/%d", o.Answer.InputTokens, o.Answer.OutputTokens)
}

// writeAnswer writes one answer in a fenced block, so its own Markdown
// cannot break the report's structure.
func writeAnswer(b *strings.Builder, title string, o Outcome, budget int) {
	fmt.Fprintf(b, "\n### %s\n\n", title)
	if o.Err != "" {
		fmt.Fprintf(b, "Failed: %s\n", truncate(o.Err, budget))
		return
	}
	text := truncate(o.Answer.Text, budget)
	fence := strings.Repeat("`", max(3, longestRun(text, '`')+1))
	fmt.Fprintf(b, "%stext\n%s\n%s\n", fence, strings.TrimRight(text, "\n"), fence)
}

// truncate shortens s to at most n bytes on a rune boundary, saying so.
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	const note = "\n[... shortened to fit the report]"
	cut := max(0, n-len(note))
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + note
}

func longestRun(s string, c byte) int {
	best, cur := 0, 0
	for i := range len(s) {
		if s[i] == c {
			cur++
			best = max(best, cur)
		} else {
			cur = 0
		}
	}
	return best
}
