package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/petervlad/loadout/internal/api"
	"github.com/petervlad/loadout/internal/atomicfile"
	"github.com/petervlad/loadout/internal/client"
	"github.com/petervlad/loadout/internal/pack"
	"github.com/petervlad/loadout/internal/registry"
	"github.com/petervlad/loadout/internal/remote"
	"github.com/petervlad/loadout/internal/review"
	"github.com/petervlad/loadout/internal/reviewplan"
)

// Planner picks the two versions and the team packages a review runs;
// *reviewplan.Planner is one.
type Planner interface {
	Resolve(ctx context.Context, req reviewplan.Request) (reviewplan.Plan, error)
}

// runFlags are the flags of every command that runs a comparison.
type runFlags struct {
	tool, outPath, model string
	prompts              []string
	runs                 int
	timeout              time.Duration
	safe, noContext      bool
}

func (f *runFlags) register(cmd *cobra.Command) {
	fs := cmd.Flags()
	fs.StringVar(&f.tool, "tool", "claude", "AI tool to run: claude or copilot")
	fs.StringArrayVar(&f.prompts, "prompt", nil, "prompt to run instead of the proposal's (repeatable)")
	fs.IntVar(&f.runs, "runs", 1, fmt.Sprintf("runs per prompt and version (1 to %d)", review.MaxRuns))
	fs.DurationVar(&f.timeout, "timeout", 5*time.Minute, "time limit for each run")
	fs.StringVar(&f.model, "model", "", "model to ask the tool for (default: the tool's or the agent's own)")
	fs.StringVar(&f.outPath, "out", "", "report file (default: review-ID.md in the current folder)")
	fs.BoolVar(&f.safe, "safe", false, "refuse every tool call that would need your approval (default: your tool's full access)")
	fs.BoolVar(&f.noContext, "no-context", false, "run without the team's other packages beside each version")
}

func (f *runFlags) mode() review.Mode {
	if f.safe {
		return review.ModeSafe
	}
	return review.ModeFull
}

// runReviewPackage is `loadout review PKG`.
func runReviewPackage(cmd *cobra.Command, d Deps, at *reviewTarget, arg string, req reviewplan.Request, f *runFlags, noUpload bool) error {
	if !strings.Contains(arg, "/") {
		return fmt.Errorf("unknown command or package %q: name a package as TEAM/NAME, for example loadout review payments/planner, or see loadout review --help", arg)
	}
	id, err := pack.ParseID(arg)
	if err != nil {
		return err
	}
	c, r, err := at.connect(d, id.Team())
	if err != nil {
		return err
	}
	if c, err = d.inView(c, r, id.Team()); err != nil {
		return err
	}
	req.Package, req.NoContext = id, f.noContext
	run, err := d.planReview(cmd.Context(), c, req)
	if err != nil {
		return err
	}
	run.upload = !noUpload && run.plan.Proposal != nil && run.plan.Proposal.Status == api.StatusOpen
	run.noUploadHint = true
	if !noUpload && !run.upload {
		run.notes = append(run.notes, "the report stays on this machine: "+whyLocal(run.plan)+".")
	}
	return d.runReview(cmd, run, r, f)
}

// whyLocal says why a report cannot be attached to what was reviewed.
func whyLocal(p reviewplan.Plan) string {
	if p.Proposal != nil {
		return fmt.Sprintf("reports attach only to open proposals, and proposal %s is %s", p.Proposal.ID, p.Proposal.Status)
	}
	return fmt.Sprintf("%s %s is a release, and reports attach only to open proposals", p.To.Pack.ID(), p.To.Version())
}

// inView returns c working in the view of team on r, so listings show that
// team's packages and the layers above it.
func (d Deps) inView(c *client.Client, r remote.Remote, team string) (*client.Client, error) {
	cfg, err := d.Remotes.Load()
	if err != nil {
		return nil, err
	}
	return c.In(viewOf(cfg, r.Name, team)), nil
}

// reviewRun is a comparison about to start.
type reviewRun struct {
	c    *client.Client
	plan reviewplan.Plan
	// upload attaches the report to plan.Proposal.
	upload bool
	// noUploadHint mentions --no-upload in the upload line, for commands
	// that upload by default.
	noUploadHint bool
	// notes are printed with the plan's notes but not put in the report.
	notes []string
}

func (d Deps) planReview(ctx context.Context, c *client.Client, req reviewplan.Request) (*reviewRun, error) {
	if d.Comparer == nil || d.NewPlanner == nil {
		return nil, errors.New("review runs are not available in this build")
	}
	plan, err := d.NewPlanner(c).Resolve(ctx, req)
	if err != nil {
		return nil, err
	}
	return &reviewRun{c: c, plan: plan}, nil
}

// runReview prints what is about to run, runs it, writes the report and
// uploads it when asked.
func (d Deps) runReview(cmd *cobra.Command, run *reviewRun, r remote.Remote, f *runFlags) error {
	plan := run.plan
	prompts, err := promptsFor(plan, f.prompts)
	if err != nil {
		return err
	}
	mode := f.mode()
	caveat, err := d.Comparer.Caveat(f.tool, mode)
	if err != nil {
		return err
	}
	outPath := f.outPath
	if outPath == "" {
		outPath = defaultReportName(plan)
	}
	outPath = d.abs(outPath)

	out, errOut := cmd.OutOrStdout(), cmd.ErrOrStderr()
	writePlan(out, plan, f.noContext, slices.Concat(plan.Notes, run.notes))
	if mode == review.ModeFull {
		fmt.Fprintf(errOut, "warning: running %s with full access to this machine (files, shell, web); add --safe to refuse anything that needs approval.\n", runSubject(plan))
	}
	if caveat != "" {
		fmt.Fprintln(errOut, "notice:", caveat)
	}
	pkg := plan.To.Pack.ID()
	if run.upload {
		fmt.Fprintf(out, "the report will be attached to proposal %s, where everyone who reads %s on %s can see it", plan.Proposal.ID, pkg.Team(), r.Name)
		if run.noUploadHint {
			fmt.Fprint(out, "; add --no-upload to keep it on this machine")
		}
		fmt.Fprintln(out)
	}
	sides := 2
	if plan.From.Pack == nil {
		sides = 1
	}
	fmt.Fprintf(out, "running %d prompt(s) x %d run(s) x %d version(s) = %d %s run(s) on your own subscription, %s\n",
		len(prompts), f.runs, sides, len(prompts)*f.runs*sides, f.tool, modeText(mode))

	opts := review.Options{
		Tool:          f.tool,
		Mode:          mode,
		Current:       plan.From.Pack,
		Candidate:     plan.To.Pack,
		CurrentFrom:   origin(plan.From),
		CandidateFrom: origin(plan.To),
		Context:       plan.Context,
		Notes:         plan.Notes,
		Prompts:       prompts,
		Runs:          f.runs,
		Timeout:       f.timeout,
		Model:         f.model,
	}
	if plan.Proposal != nil {
		opts.ProposalID = plan.Proposal.ID
	}
	// Both versions run with the models the reviewer's team installs;
	// --model still wins, the tool reads it over the agent file.
	if opts.Models, err = fetchTiers(cmd.Context(), run.c); err != nil {
		return err
	}
	rep, err := d.Comparer.Compare(cmd.Context(), opts)
	if err != nil {
		return err
	}
	if err := atomicfile.Write(outPath, rep.Markdown(maxLocalReport), 0o644); err != nil {
		return fmt.Errorf("write report: %w", err)
	}
	fmt.Fprintf(out, "compared %s: %s with %s using %s %s, %s, %d team package(s) beside: %d failed run(s)\nreport: %s\n",
		pkg, sideText(plan.To), sideText(plan.From), rep.Tool, rep.ToolVersion, modeText(mode), len(rep.Context), rep.Failures(), outPath)
	if n := rep.Failures(); mode == review.ModeFull && n > 0 && n == len(prompts)*f.runs*sides {
		fmt.Fprintln(errOut, "every run failed; if your tool refuses full access (managed settings can turn it off), run again with --safe.")
	}
	if !run.upload {
		return nil
	}
	seq, err := uploadReport(cmd.Context(), run.c, plan.Proposal.ID, rep)
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "attached as review %d of proposal %s; everyone who reads %s on %s can see it\n", seq, plan.Proposal.ID, pkg.Team(), r.Name)
	return nil
}

// promptsFor returns the prompts given on the command line, else the
// proposal's current list.
func promptsFor(plan reviewplan.Plan, given []string) ([]string, error) {
	if len(given) > 0 {
		return given, checkPrompts(given)
	}
	if plan.Proposal == nil || len(plan.Proposal.Prompts) == 0 {
		return nil, fmt.Errorf("%s %s is a release and has no prompts; give them with --prompt TEXT (repeatable)", plan.To.Pack.ID(), plan.To.Version())
	}
	return plan.Proposal.Prompts, nil
}

func defaultReportName(plan reviewplan.Plan) string {
	if plan.Proposal != nil {
		return "review-" + plan.Proposal.ID + ".md"
	}
	return "review-" + plan.To.Pack.ID().Name() + "-" + plan.To.Version() + ".md"
}

// writePlan prints both sides, the team packages beside them and the
// planner's notes.
func writePlan(out io.Writer, plan reviewplan.Plan, noContext bool, notes []string) {
	fmt.Fprintf(out, "reviewing %s\n", plan.To.Pack.ID())
	to := sideText(plan.To)
	if plan.Proposal != nil {
		to += " by " + authorName(plan.Proposal.Author)
	}
	fmt.Fprintf(out, "  to:     %s\n  from:   %s\n", to, sideText(plan.From))
	switch {
	case noContext:
		fmt.Fprintln(out, "  beside: no team packages (--no-context)")
	case len(plan.ContextReleases) == 0:
		fmt.Fprintln(out, "  beside: no other team packages")
	default:
		names := make([]string, len(plan.ContextReleases))
		for i, rel := range plan.ContextReleases {
			names[i] = rel.ID + " " + rel.Version
		}
		fmt.Fprintf(out, "  beside: %d team package(s): %s\n", len(names), strings.Join(names, ", "))
	}
	for _, n := range notes {
		fmt.Fprintln(out, "note:", n)
	}
}

// sideText names a side for the terminal, such as "installed 1.2.0".
func sideText(s reviewplan.Side) string {
	switch {
	case s.Pack == nil:
		return "nothing (no release yet)"
	case s.Source == reviewplan.SourceProposal:
		return fmt.Sprintf("proposal %s (%s)", s.ProposalID, s.Version())
	}
	return string(s.Source) + " " + s.Version()
}

// runSubject names what a full-access run executes: the proposal and its
// author, or the package when both sides are releases.
func runSubject(plan reviewplan.Plan) string {
	if p := plan.Proposal; p != nil {
		return fmt.Sprintf("proposal %s by %s", p.ID, authorName(p.Author))
	}
	return plan.To.Pack.ID().String()
}

// authorName names an actor by its token label without the way the token
// was made: "alice@example.com via loadout login" is alice@example.com.
func authorName(a api.Actor) string {
	if a.Label == "" {
		return "token " + a.TokenID
	}
	name, _, _ := strings.Cut(a.Label, " via ")
	return name
}

func modeText(m review.Mode) string {
	if m == review.ModeSafe {
		return "safe mode"
	}
	return "full access"
}

func origin(s reviewplan.Side) review.Origin {
	return review.Origin{Source: string(s.Source), ProposalID: s.ProposalID}
}

// uploadReport attaches rep to the proposal and returns its number.
func uploadReport(ctx context.Context, c *client.Client, proposalID string, rep review.Report) (int, error) {
	text := rep.Markdown(registry.MaxReviewSize)
	if len(text) > registry.MaxReviewSize {
		return 0, fmt.Errorf("the report is %d bytes even with shortened answers, over the %d byte limit; try fewer prompts or runs", len(text), registry.MaxReviewSize)
	}
	data, err := rep.JSON(registry.MaxReviewSize)
	if err != nil {
		return 0, err
	}
	if len(data) > registry.MaxReviewSize {
		data = nil // the Markdown still carries every answer
	}
	r, err := c.AddReview(ctx, proposalID, text, data)
	if err != nil {
		return 0, err
	}
	return r.Seq, nil
}
