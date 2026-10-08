package cli

import (
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/petervlad/loadout/internal/api"
	"github.com/petervlad/loadout/internal/client"
	"github.com/petervlad/loadout/internal/pack"
	"github.com/petervlad/loadout/internal/remote"
	"github.com/petervlad/loadout/internal/reviewplan"
)

// maxLocalReport bounds the report compare writes to disk.
const maxLocalReport = 8 << 20

// reviewTarget picks the registry the review commands talk to.
type reviewTarget struct{ remote, team string }

// connect returns a client for --remote, else for the registry bound to
// --team, else for the one bound to ns, else the default.
func (t *reviewTarget) connect(d Deps, ns string) (*client.Client, remote.Remote, error) {
	if t.team != "" {
		ns = t.team
	}
	return d.connectFor(t.remote, ns, "")
}

func newReviewCmd(d Deps) *cobra.Command {
	var at reviewTarget
	var f runFlags
	var req reviewplan.Request
	var noUpload bool
	cmd := &cobra.Command{
		Use:   "review [TEAM/NAME]",
		Short: "Review proposed versions: run one in your AI tool, list, show, approve, reject",
		Long: `A proposal is a candidate version waiting for a teammate's approval. Approving
it publishes it as a normal release that everyone gets through sync.

loadout review TEAM/NAME runs two versions of a package side by side in your
own AI tool (Claude Code or Copilot CLI), on your machine and your
subscription, with the team's other packages beside each. By default it
compares the version you have installed with the newest open proposal, and
attaches the report to that proposal. loadout itself never runs an agent or
calls a model.

The proposal is a teammate's package, and by default your tool runs it with
the access it normally has: it can read and change files, run commands and
reach the web, and answers may quote your files. Add --safe to refuse
anything that would need your approval.

--from and --to take a version, latest (the latest release) or a proposal
id. Without --from: your installed version, else the latest release. Without
--to: the newest open proposal, else the latest release.`,
		Example: `  loadout review payments/planner
  loadout review payments/planner --to 3f2a9c01b7de --safe
  loadout review payments/planner --from 1.2.0 --to latest --prompt "Plan PROJ-99"
  loadout review payments/planner --tool copilot --no-upload --no-context`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				return cmd.Help()
			}
			return runReviewPackage(cmd, d, &at, args[0], req, &f, noUpload)
		},
	}
	cmd.PersistentFlags().StringVar(&at.remote, "remote", "", "registry (default: the one bound to --team, else the default remote)")
	cmd.PersistentFlags().StringVar(&at.team, "team", "", "review in the registry of this team or layer")
	f.register(cmd)
	cmd.Flags().StringVar(&req.From, "from", "", "version to compare with: a version, latest or a proposal id (default: yours installed, else the latest release)")
	cmd.Flags().StringVar(&req.To, "to", "", "version under review: a version, latest or a proposal id (default: the newest open proposal, else the latest release)")
	cmd.Flags().BoolVar(&noUpload, "no-upload", false, "keep the report on this machine instead of attaching it to the proposal")
	cmd.AddCommand(
		newReviewListCmd(d, &at),
		newReviewShowCmd(d, &at),
		newReviewReportCmd(d, &at),
		newReviewCompareCmd(d, &at),
		newReviewApproveCmd(d, &at),
		newReviewCloseCmd(d, &at, "reject", "Reject a proposal (needs a publish token; not your own)"),
		newReviewCloseCmd(d, &at, "withdraw", "Withdraw your own proposal"),
	)
	return cmd
}

func newReviewListCmd(d Deps, at *reviewTarget) *cobra.Command {
	var pkg string
	var all bool
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List open proposals of your team",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			var ns string
			if pkg != "" {
				id, err := pack.ParseID(pkg)
				if err != nil {
					return err
				}
				ns = id.Team()
			}
			c, r, err := at.connect(d, ns)
			if err != nil {
				return err
			}
			status := api.StatusOpen
			if all {
				status = api.StatusAny
			}
			props, err := c.Proposals(cmd.Context(), pkg, status)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if len(props) == 0 {
				if all {
					fmt.Fprintf(out, "no proposals on %s\n", r.Name)
				} else {
					fmt.Fprintf(out, "no open proposals on %s; see closed ones with --all\n", r.Name)
				}
				return nil
			}
			w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "ID\tPACKAGE\tVERSION\tSTATUS\tAUTHOR\tPROPOSED\tPROMPTS\tREVIEWS")
			for _, p := range props {
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%d\t%d\n", p.ID, p.Package, p.Version, statusText(p), actor(p.Author), p.CreatedAt.Local().Format("2006-01-02 15:04"), len(p.Prompts), len(p.Reviews))
			}
			return w.Flush()
		},
	}
	cmd.Flags().StringVar(&pkg, "package", "", "only proposals of TEAM/NAME")
	cmd.Flags().BoolVar(&all, "all", false, "include approved, rejected and withdrawn proposals")
	return cmd
}

// statusText is a proposal's status in a listing, marking one its author
// approved as owner and an open one a release came out after (review show
// says both in full).
func statusText(p api.Proposal) string {
	switch {
	case p.SelfApproved:
		return p.Status + " without review"
	case p.ReplacedBy != "":
		return p.Status + ", replaced by " + p.ReplacedBy
	case p.Blocked():
		return p.Status + ", blocked by " + p.Latest
	case p.Behind():
		return p.Status + ", behind " + p.Latest
	}
	return p.Status
}

func actor(a api.Actor) string {
	if a.Label == "" {
		return "token " + a.TokenID
	}
	return a.Label + " (" + a.TokenID + ")"
}

// releasesOf lists a package's releases, lowest first; none is not an
// error.
func releasesOf(cmd *cobra.Command, c *client.Client, id pack.ID) ([]api.Release, error) {
	rels, err := c.Releases(cmd.Context(), id)
	if errors.Is(err, api.ErrNotFound) {
		return nil, nil
	}
	return rels, err
}

func newReviewShowCmd(d Deps, at *reviewTarget) *cobra.Command {
	return &cobra.Command{
		Use:   "show ID",
		Short: "Show a proposal: note, prompts, reviews and what its author changed",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, _, err := at.connect(d, "")
			if err != nil {
				return err
			}
			prop, err := c.Proposal(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			candidate, err := c.FetchProposal(cmd.Context(), prop)
			if err != nil {
				return err
			}
			rels, err := releasesOf(cmd, c, candidate.ID())
			if err != nil {
				return err
			}
			st := newStanding(prop, rels)
			var from *pack.Pack
			if base, ok := st.diffBase(); ok {
				if from, err = c.Fetch(cmd.Context(), candidate.ID(), base.Version); err != nil {
					return err
				}
			}
			out := cmd.OutOrStdout()
			writeProposal(out, prop)
			st.write(out)
			fmt.Fprintf(out, "\n%s:\n\n", st.diffTitle())
			if err := writeDiff(out, prop.Package+"@"+st.diffLabel(), prop.Package+"@"+prop.Version, from, candidate); err != nil {
				return err
			}
			st.writeNext(out)
			if prop.Status == api.StatusOpen {
				fmt.Fprintf(out, "\nTry both versions in your own AI tool: loadout review %s --to %s\n", prop.Package, prop.ID)
			}
			return nil
		},
	}
}

func writeProposal(out io.Writer, p api.Proposal) {
	fmt.Fprintf(out, "Proposal %s: %s@%s (%s)\n", p.ID, p.Package, p.Version, p.Status)
	fmt.Fprintf(out, "Proposed by %s on %s\n", actor(p.Author), p.CreatedAt.Local().Format("2006-01-02 15:04"))
	if p.Replaces != "" {
		fmt.Fprintf(out, "Replaces proposal %s; its prompts came along, its reviews compared the older content\n", p.Replaces)
	}
	if p.DecidedAt != nil && p.DecidedBy != nil {
		fmt.Fprintf(out, "%s by %s on %s", strings.ToUpper(p.Status[:1])+p.Status[1:], actor(*p.DecidedBy), p.DecidedAt.Local().Format("2006-01-02 15:04"))
		if p.DecisionNote != "" {
			fmt.Fprintf(out, ": %s", p.DecisionNote)
		}
		fmt.Fprintln(out)
		if p.SelfApproved {
			fmt.Fprintln(out, "Approved by its author as owner, without review")
		}
		if p.ReplacedBy != "" {
			fmt.Fprintf(out, "Replaced by proposal %s: loadout review show %s\n", p.ReplacedBy, p.ReplacedBy)
		}
		if p.ApprovedOver != "" {
			fmt.Fprintf(out, "Approved over %s: released without %s's changes\n", p.ApprovedOver, p.ApprovedOver)
		}
	}
	if p.Note != "" {
		fmt.Fprintf(out, "\nNote:\n%s\n", indent(p.Note))
	}
	fmt.Fprintln(out, "\nPrompts:")
	for i, pr := range p.Prompts {
		fmt.Fprintf(out, "%3d. %s\n", i+1, strings.ReplaceAll(pr, "\n", "\n     "))
	}
	if len(p.Reviews) > 0 {
		fmt.Fprintln(out, "\nReviews:")
		for _, r := range p.Reviews {
			fmt.Fprintf(out, "%3d. %s on %s, %d bytes (loadout review report %s %d)\n", r.Seq, actor(r.Author), r.CreatedAt.Local().Format("2006-01-02 15:04"), r.Size, p.ID, r.Seq)
		}
	}
}

func indent(s string) string {
	return "  " + strings.ReplaceAll(s, "\n", "\n  ")
}

func newReviewReportCmd(d Deps, at *reviewTarget) *cobra.Command {
	return &cobra.Command{
		Use:   "report ID [N]",
		Short: "Print a review attached to a proposal (default: the latest)",
		Args:  cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, _, err := at.connect(d, "")
			if err != nil {
				return err
			}
			seq := 0
			if len(args) == 2 {
				if seq, err = strconv.Atoi(args[1]); err != nil || seq < 1 {
					return fmt.Errorf("review number %q: want a positive number", args[1])
				}
			} else {
				prop, err := c.Proposal(cmd.Context(), args[0])
				if err != nil {
					return err
				}
				if len(prop.Reviews) == 0 {
					return fmt.Errorf("proposal %s has no reviews yet; run one and attach it with: loadout review %s --to %s", prop.ID, prop.Package, prop.ID)
				}
				seq = prop.Reviews[len(prop.Reviews)-1].Seq
			}
			text, err := c.ReviewText(cmd.Context(), args[0], seq)
			if err != nil {
				return err
			}
			_, err = cmd.OutOrStdout().Write(text)
			return err
		},
	}
}

func newReviewCompareCmd(d Deps, at *reviewTarget) *cobra.Command {
	var f runFlags
	var upload bool
	cmd := &cobra.Command{
		Use:   "compare ID",
		Short: "Run the latest release and a proposal side by side in your own AI tool",
		Long: `Compare a proposal with the latest release of its package, the way
loadout review TEAM/NAME --from latest --to ID does, but keep the report on
this machine unless --upload is given. It runs on your machine, with your
tool and your subscription; loadout never calls a model itself.

The proposal is a teammate's package, and by default your tool runs it with
the access it normally has: it can read and change files, run commands and
reach the web. Read it first with loadout review show ID, or add --safe to
refuse anything that would need your approval.`,
		Example: `  loadout review compare 3f2a9c01b7de
  loadout review compare 3f2a9c01b7de --tool copilot --runs 2 --upload
  loadout review compare 3f2a9c01b7de --prompt "Plan PROJ-99" --model haiku --safe`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, r, err := at.connect(d, "")
			if err != nil {
				return err
			}
			prop, err := c.Proposal(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			if upload && prop.Status != api.StatusOpen {
				return fmt.Errorf("proposal %s is %s; reports can only be attached to open proposals", prop.ID, prop.Status)
			}
			id, err := pack.ParseID(prop.Package)
			if err != nil {
				return fmt.Errorf("proposal %s: %w", prop.ID, err)
			}
			if c, err = d.inView(c, r, id.Team()); err != nil {
				return err
			}
			run, err := d.planReview(cmd.Context(), c, reviewplan.Request{Package: id, From: "latest", To: prop.ID, NoContext: f.noContext})
			if err != nil {
				return err
			}
			run.upload = upload
			return d.runReview(cmd, run, r, &f)
		},
	}
	f.register(cmd)
	cmd.Flags().BoolVar(&upload, "upload", false, "attach the report to the proposal")
	return cmd
}

func newReviewApproveCmd(d Deps, at *reviewTarget) *cobra.Command {
	var req api.DecisionRequest
	cmd := &cobra.Command{
		Use:   "approve ID",
		Short: "Approve a proposal: it becomes a release everyone gets through sync",
		Long: `Approve a teammate's proposal. It needs a publish token, and not the
author's: nobody approves their own change. The version must still be higher
than the latest release; if someone published in between, the author
proposes again.

An owner of the namespace (or of a project above it) may approve their own
proposal with --as-owner and a --note saying why. It is released without a
second person's review, and the proposal and the release say so.

A proposal made from an older release than the latest is behind: approving
it releases the author's files without the newer release's changes. The
registry refuses that unless you name the latest release with --over, which
is recorded; otherwise the author brings the proposal up to date.`,
		Example: `  loadout review approve 3f9a1c2b7d10 --note "looks good"
  loadout review approve 3f9a1c2b7d10 --as-owner --note "hotfix, nobody else is around"
  loadout review approve 3f9a1c2b7d10 --over 2.1.0 --note "redoes the 2.1.0 hotfix"`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, r, err := at.connect(d, "")
			if err != nil {
				return err
			}
			res, err := c.Approve(cmd.Context(), args[0], req)
			if apiErr, ok := errors.AsType[*client.APIError](err); ok && apiErr.Code == api.CodeVersionConflict {
				if strings.Contains(apiErr.Message, "loadout rebase") {
					return err
				}
				// An older registry gives no hint.
				return fmt.Errorf("%w\nthe author brings it up to date with: loadout rebase --proposal %s, then proposes again", err, args[0])
			}
			if apiErr, ok := errors.AsType[*client.APIError](err); ok && apiErr.Code == api.CodeBehind {
				// The registry's sentence names both versions and both ways out.
				return errors.New(strings.TrimPrefix(apiErr.Message, api.ErrBehind.Error()+": "))
			}
			if err != nil {
				return err
			}
			rel, prop := res.Release, res.Proposal
			what := "approved " + prop.ID
			if prop.SelfApproved {
				what = "approved your own proposal " + prop.ID + " as owner, without review"
			}
			released := fmt.Sprintf("%s@%s is released on %s", rel.ID, rel.Version, r.Name)
			if prop.ApprovedOver != "" {
				what += " over " + prop.ApprovedOver
				released += " without " + prop.ApprovedOver + "'s changes"
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%s: %s; teammates get it with: loadout sync\n", what, released)
			return nil
		},
	}
	cmd.Flags().StringVar(&req.Note, "note", "", "a note for the author (required with --as-owner)")
	cmd.Flags().BoolVar(&req.AsOwner, "as-owner", false, "approve your own proposal as an owner of its namespace, without review")
	cmd.Flags().StringVar(&req.Over, "over", "", "approve a proposal that is behind over the latest release, named exactly, dropping its changes")
	return cmd
}

// newReviewCloseCmd builds reject and withdraw, which differ only in who may
// run them.
func newReviewCloseCmd(d Deps, at *reviewTarget, verb, short string) *cobra.Command {
	var note string
	cmd := &cobra.Command{
		Use:   verb + " ID",
		Short: short,
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, _, err := at.connect(d, "")
			if err != nil {
				return err
			}
			var p api.Proposal
			if verb == "reject" {
				p, err = c.Reject(cmd.Context(), args[0], note)
			} else {
				p, err = c.Withdraw(cmd.Context(), args[0], note)
			}
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%s %s (%s@%s)\n", p.Status, p.ID, p.Package, p.Version)
			return nil
		},
	}
	cmd.Flags().StringVar(&note, "note", "", "why, for the record")
	return cmd
}
