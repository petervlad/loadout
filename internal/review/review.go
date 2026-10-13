// Package review compares a proposed version with another version of the
// same package by running both in the reviewer's own AI tool.
//
// Nothing here runs on a server. A reviewer starts `loadout review` on their
// own machine; this package renders both versions, each next to the same
// team packages, into two throwaway project folders, runs the reviewer's own
// AI CLI headless once per prompt per version under their own subscription,
// and writes a Markdown report. loadout holds no AI keys and calls no model
// itself.
package review

import (
	"context"
	"errors"
	"fmt"
	"iter"
	"log/slog"
	"slices"
	"strings"
	"time"

	"github.com/petervlad/loadout/internal/ident"
	"github.com/petervlad/loadout/internal/pack"
	"github.com/petervlad/loadout/internal/target"
	"github.com/petervlad/loadout/internal/tier"
)

// Limits on one comparison, so a typo cannot start hundreds of AI runs and
// a large team cannot fill the disk.
const (
	MaxRuns = 5
	// MaxContextPackages and MaxContextBytes bound the team packages
	// rendered beside each side; bytes are uncompressed and counted per side.
	MaxContextPackages = 100
	MaxContextBytes    = 256 << 20
	// versionTimeout bounds asking the AI tool for its version.
	versionTimeout = 30 * time.Second
)

// ErrUnknownTool is returned for a --tool no runner handles.
var ErrUnknownTool = errors.New("unknown tool")

// Mode is how much the AI tool may do on the reviewer's machine during a
// run. The zero value is invalid, so no caller gets full access by accident.
type Mode string

const (
	// ModeFull gives the tool the access it normally has: files, shell and
	// web, without asking.
	ModeFull Mode = "full"
	// ModeSafe refuses every tool call that would need approval.
	ModeSafe Mode = "safe"
)

// Valid reports whether m is a known mode.
func (m Mode) Valid() bool { return m == ModeFull || m == ModeSafe }

// Where a side came from, for Version.Source and Origin.Source. Empty reads
// as a release, as in reports made before sources were recorded.
const (
	SourceRelease   = "release"
	SourceInstalled = "installed"
	SourceProposal  = "proposal"
)

// Renderer writes a package in one AI tool's format. The target adapters
// implement it.
type Renderer = target.Renderer

// Runner runs one prompt headless in an AI tool the reviewer has installed.
type Runner interface {
	// Version returns the tool's version, which also proves it is installed.
	Version(ctx context.Context) (string, error)
	// Run answers req.Prompt in req.Dir, as the agent req.Agent if set.
	Run(ctx context.Context, req Request) (Answer, error)
}

// Caveat is implemented by runners whose tool cannot fully review in a
// mode. The knowledge stays next to the runner's flags.
type Caveat interface {
	// Caveat returns what the tool cannot do in m; "" when there is
	// nothing to say.
	Caveat(m Mode) string
}

// Request is one headless run.
type Request struct {
	// Dir is the project folder the agent was rendered into; the tool runs
	// with it as its working directory.
	Dir string
	// Agent is the agent to answer as; empty for a skill or knowledge
	// base, which the tool finds in Dir and uses when the prompt needs it.
	Agent  string
	Prompt string
	// Model is passed to the tool when set; empty keeps the reviewer's
	// default or the agent's own model.
	Model string
	// Mode is the access the tool gets; runners refuse the zero value.
	Mode Mode
	// Hide lists skill names the tool must not load for this run: the
	// reviewer's own install of the package under review, which would
	// otherwise shadow the side's copy. Runners without a way to hide a
	// skill ignore it.
	Hide []string
}

// Answer is what a run produced. Token counts are zero when the tool does
// not report them.
type Answer struct {
	Text string `json:"text"`
	// Duration is in nanoseconds in JSON.
	Duration     time.Duration `json:"duration_ns"`
	InputTokens  int           `json:"input_tokens,omitempty"`
	OutputTokens int           `json:"output_tokens,omitempty"`
}

// Tool pairs an AI tool's renderer with the runner that starts it.
type Tool struct {
	Name     string
	Renderer Renderer
	Runner   Runner
}

// Comparer runs comparisons.
type Comparer struct {
	tools   []Tool
	tempDir string
	now     func() time.Time
	log     *slog.Logger
	limits  contextLimits
}

// contextLimits bound the team packages rendered beside each side; tests
// lower them.
type contextLimits struct {
	packages int
	bytes    int64
}

// NewComparer returns a Comparer for the given tools. tempDir holds the
// throwaway project folders; empty means the system temp folder.
func NewComparer(log *slog.Logger, now func() time.Time, tempDir string, tools ...Tool) *Comparer {
	return &Comparer{
		tools: slices.Clone(tools), tempDir: tempDir, now: now, log: log,
		limits: contextLimits{packages: MaxContextPackages, bytes: MaxContextBytes},
	}
}

// ToolNames lists the tools a comparison can use.
func (c *Comparer) ToolNames() []string {
	out := make([]string, len(c.tools))
	for i, t := range c.tools {
		out[i] = t.Name
	}
	return out
}

// Caveat returns what the named tool cannot do in mode m, "" when nothing,
// so a caller can warn before a run starts.
func (c *Comparer) Caveat(tool string, m Mode) (string, error) {
	t, err := c.tool(tool)
	if err != nil {
		return "", err
	}
	if cv, ok := t.Runner.(Caveat); ok {
		return cv.Caveat(m), nil
	}
	return "", nil
}

// Origin says where a side came from.
type Origin struct {
	// Source is SourceRelease, SourceInstalled or SourceProposal; empty
	// leaves the report's plain "Current" and "Candidate" labels.
	Source string
	// ProposalID is set when Source is SourceProposal.
	ProposalID string
}

// Options describe one comparison.
type Options struct {
	Tool string
	// Mode is the access the tool gets; the zero value is refused.
	Mode Mode
	// Current is the version to compare with; nil when the package has no
	// release yet, and then only the candidate runs.
	Current   *pack.Pack
	Candidate *pack.Pack
	// CurrentFrom and CandidateFrom are shown in the report.
	CurrentFrom, CandidateFrom Origin
	// Context yields the team packages to render beside both sides. It is
	// walked once and each package is dropped after rendering, so memory
	// holds one at a time. Nil means none.
	Context iter.Seq2[*pack.Pack, error]
	// Notes are copied into the report, before any runner caveat.
	Notes   []string
	Prompts []string
	// Runs is how often each prompt runs per version.
	Runs    int
	Timeout time.Duration
	// Model is passed to the tool on every run and wins over the agent
	// file's model.
	Model string
	// Models is the reviewer's resolved model table, so both versions
	// render as the team would install them.
	Models tier.Table
	// ProposalID is shown in the report.
	ProposalID string
}

// CheckRunnable refuses a package that must not run in a review, such as a
// pipeline whose conductor would start a whole run in the review folder. A
// caller asks before it prints anything about a run.
func CheckRunnable(p *pack.Pack) error {
	if p.Kind().CanRunInReview() {
		return nil
	}
	return fmt.Errorf("%s is %s, and running it would start a whole run in the review folder. Read its changes instead: loadout review show ID", p.ID(), p.Kind().WithArticle())
}

func (o Options) check() error {
	switch {
	case !o.Mode.Valid():
		return fmt.Errorf("compare: mode %q: want %s or %s", o.Mode, ModeFull, ModeSafe)
	case o.Candidate == nil:
		return errors.New("compare: no candidate")
	case len(o.Prompts) == 0:
		return errors.New("compare: no prompts")
	case o.Runs < 1 || o.Runs > MaxRuns:
		return fmt.Errorf("compare: runs %d: want 1 to %d", o.Runs, MaxRuns)
	case o.Timeout <= 0:
		return fmt.Errorf("compare: timeout %s: want a positive duration", o.Timeout)
	}
	return CheckRunnable(o.Candidate)
}

// Compare renders both versions with the context beside them, runs every
// prompt against each and returns the report. A failed run is recorded in
// the report, not returned; only setup problems, such as a missing tool or
// too many team packages, are errors, and they come before any run.
func (c *Comparer) Compare(ctx context.Context, opts Options) (Report, error) {
	tool, err := c.tool(opts.Tool)
	if err != nil {
		return Report{}, err
	}
	if err := opts.check(); err != nil {
		return Report{}, err
	}

	vctx, cancel := context.WithTimeout(ctx, versionTimeout)
	version, err := tool.Runner.Version(vctx)
	cancel()
	if err != nil {
		return Report{}, fmt.Errorf("%s is not available on this machine: %w", tool.Name, err)
	}

	sides := []*side{{label: "candidate", pack: opts.Candidate}}
	if opts.Current != nil {
		sides = []*side{{label: "current", pack: opts.Current}, sides[0]}
	}
	for _, s := range sides {
		if err := c.prepare(tool, s, opts.Models); err != nil {
			return Report{}, errors.Join(err, cleanup(sides))
		}
	}
	neighbours, skipped, err := c.renderContext(ctx, tool, sides, opts.Context, opts.Models)
	if err != nil {
		return Report{}, errors.Join(err, cleanup(sides))
	}

	rep := c.newReport(tool, version, opts)
	rep.Context = neighbours
	rep.Notes = append(rep.Notes, skipped...)
	req := Request{Model: opts.Model, Mode: opts.Mode, Hide: hidden(sides)}
	for _, prompt := range opts.Prompts {
		pr := PromptResult{Prompt: prompt}
		req.Prompt = prompt
		for range opts.Runs {
			var pair RunPair
			for _, s := range sides {
				if ctx.Err() != nil {
					return Report{}, errors.Join(ctx.Err(), cleanup(sides))
				}
				out := c.runOne(ctx, tool, s, req, opts.Timeout)
				if s.label == "current" {
					pair.Current = &out
				} else {
					pair.Candidate = out
				}
			}
			pr.Runs = append(pr.Runs, pair)
		}
		rep.Prompts = append(rep.Prompts, pr)
	}
	return rep, cleanup(sides)
}

func (c *Comparer) newReport(tool Tool, version string, opts Options) Report {
	rep := Report{
		Tool:        tool.Name,
		ToolVersion: version,
		Model:       opts.Model,
		Mode:        opts.Mode,
		ProposalID:  opts.ProposalID,
		Package:     opts.Candidate.ID().String(),
		Candidate:   describe(opts.Candidate, opts.CandidateFrom),
		Runs:        opts.Runs,
		Notes:       slices.Clone(opts.Notes),
		GeneratedAt: c.now().UTC(),
	}
	if opts.Current != nil {
		cur := describe(opts.Current, opts.CurrentFrom)
		rep.Current = &cur
	}
	if cv, ok := tool.Runner.(Caveat); ok {
		if note := cv.Caveat(opts.Mode); note != "" {
			rep.Notes = append(rep.Notes, note)
		}
	}
	return rep
}

func (c *Comparer) tool(name string) (Tool, error) {
	for _, t := range c.tools {
		if t.Name == name {
			return t, nil
		}
	}
	return Tool{}, fmt.Errorf("%w %q: want one of %s", ErrUnknownTool, name, strings.Join(c.ToolNames(), ", "))
}

// side is one version under comparison, rendered into its own folder under
// its own name, so the two can never load each other.
type side struct {
	label string
	pack  *pack.Pack
	ws    *workspace
	// agent is the name the version runs as: the agent passed with
	// --agent, or empty for a skill or knowledge base.
	agent string
}

// prepare renders s into a fresh workspace, before any context package, so
// no context package can take its place.
func (c *Comparer) prepare(tool Tool, s *side, models tier.Table) error {
	ws, err := newWorkspace(c.tempDir, s.label)
	if err != nil {
		return err
	}
	s.ws = ws
	name := agentName(s.label, s.pack.ID().Name())
	if s.pack.Kind() == pack.KindAgent {
		s.agent = name
	}
	u, err := ws.render(tool.Renderer, s.pack, name, models)
	if err != nil {
		return fmt.Errorf("compare: render %s %s: %w", s.label, s.pack.ID(), err)
	}
	if err := ws.write(u); err != nil {
		return fmt.Errorf("compare: %w", err)
	}
	c.log.Debug("rendered for review", "side", s.label, "version", s.pack.Version(), "kind", s.pack.Kind(), "name", name, "at", ws.dir+"/"+u.Root)
	return nil
}

// renderContext walks seq once and renders each package into every side's
// workspace, so both sides get exactly the same neighbours. A package whose
// files would land on a path already taken in any side is skipped on all of
// them and named in the returned notes.
func (c *Comparer) renderContext(ctx context.Context, tool Tool, sides []*side, seq iter.Seq2[*pack.Pack, error], models tier.Table) ([]ContextPackage, []string, error) {
	if seq == nil {
		return nil, nil, nil
	}
	var (
		out   []ContextPackage
		notes []string
	)
	for p, err := range seq {
		if err != nil {
			return nil, nil, fmt.Errorf("compare: team packages: %w", err)
		}
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
		if len(out) == c.limits.packages {
			return nil, nil, fmt.Errorf("the team has more than %d packages to review beside; run with --no-context to review without them", c.limits.packages)
		}
		ok, err := c.renderEverywhere(tool, sides, p, models)
		if err != nil {
			return nil, nil, err
		}
		if !ok {
			notes = append(notes, fmt.Sprintf("%s was left out: it would take the place of another package in the review folder.", p.ID()))
			continue
		}
		out = append(out, ContextPackage{ID: p.ID().String(), Version: p.Version().String(), Kind: string(p.Kind())})
	}
	c.log.Debug("rendered team packages for review", "count", len(out), "skipped", len(notes))
	return out, notes, nil
}

// renderEverywhere renders p into every side, or into none when its unit
// clashes with one already there.
func (c *Comparer) renderEverywhere(tool Tool, sides []*side, p *pack.Pack, models tier.Table) (bool, error) {
	units := make([]target.Unit, len(sides))
	for i, s := range sides {
		u, err := s.ws.render(tool.Renderer, p, "", models)
		if err != nil {
			return false, fmt.Errorf("compare: render team package %s: %w", p.ID(), err)
		}
		if s.ws.taken(u.Root) {
			return false, nil
		}
		units[i] = u
	}
	for i, s := range sides {
		if s.ws.contextBytes+unitSize(units[i]) > c.limits.bytes {
			return false, fmt.Errorf("the team's packages come to more than %s; run with --no-context to review without them", sizeText(c.limits.bytes))
		}
		if err := s.ws.write(units[i]); err != nil {
			return false, fmt.Errorf("compare: %w", err)
		}
		s.ws.contextBytes += unitSize(units[i])
	}
	return true, nil
}

// sizeText formats n bytes for a person: whole MB when it is at least one.
func sizeText(n int64) string {
	if n >= 1<<20 {
		return fmt.Sprintf("%d MB", n>>20)
	}
	return fmt.Sprintf("%d bytes", n)
}

// hidden lists the skill names of the versions under review, so the tool
// does not load the reviewer's own install of that skill instead of the
// side's copy. Agents run by their side's name and need nothing.
func hidden(sides []*side) []string {
	var names []string
	for _, s := range sides {
		if sk, ok := s.pack.Skill(); ok && !slices.Contains(names, sk.Name) {
			names = append(names, sk.Name)
		}
	}
	return names
}

// agentName returns label-name, shortened to a valid agent name.
func agentName(label, name string) string {
	const maxLen = 64
	n := label + "-" + name
	if len(n) > maxLen {
		n = strings.TrimRight(n[:maxLen], "-")
	}
	if ident.Check("agent", n) != nil {
		return label
	}
	return n
}

func (c *Comparer) runOne(ctx context.Context, tool Tool, s *side, req Request, timeout time.Duration) Outcome {
	rctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	req.Dir, req.Agent = s.ws.dir, s.agent
	start := c.now()
	ans, err := tool.Runner.Run(rctx, req)
	if err != nil {
		if errors.Is(rctx.Err(), context.DeadlineExceeded) {
			err = fmt.Errorf("timed out after %s: %w", timeout, err)
		}
		c.log.Warn("review run failed", "side", s.label, "tool", tool.Name, "err", err)
		return Outcome{Err: err.Error()}
	}
	if ans.Duration == 0 {
		ans.Duration = c.now().Sub(start)
	}
	return Outcome{Answer: ans}
}

// cleanup removes the throwaway folders.
func cleanup(sides []*side) error {
	var errs []error
	for _, s := range sides {
		if s.ws != nil {
			errs = append(errs, s.ws.remove())
		}
	}
	return errors.Join(errs...)
}

func describe(p *pack.Pack, from Origin) Version {
	return Version{Version: p.Version().String(), Digest: string(p.Digest()), Source: from.Source, ProposalID: from.ProposalID}
}
