package cli

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"github.com/petervlad/loadout/internal/api"
	"github.com/petervlad/loadout/internal/client"
	"github.com/petervlad/loadout/internal/pack"
)

// isDeletionID reports whether id names a request to delete a package. Their
// ids start with "del-", which no proposal id has, so the review commands
// route on it.
func isDeletionID(id string) bool { return strings.HasPrefix(id, api.DeletionIDPrefix) }

// notAProposal is the refusal for a review command that only means
// something for a proposal.
func notAProposal(verb, id string) error {
	return fmt.Errorf("%s is a request to delete a package, not a proposal, so there is nothing to %s; see it with: loadout review show %s", id, verb, id)
}

func newProposeDeleteCmd(d Deps) *cobra.Command {
	var remoteName, reason string
	cmd := &cobra.Command{
		Use:   "delete TEAM/NAME",
		Short: "Ask to delete a package; a maintainer or owner who is not you approves it",
		Long: `Ask to delete a whole package from the registry: every release, every
proposal with its reviews and prompts. Nothing is deleted until a maintainer
or owner of the package's namespace approves the request, and not you: a
second person decides. An owner may approve their own request with
--as-owner and a note, and the registry records that it was deleted without
review.

The reason is required and stays in the registry's record of deletions.
Machines that have the package installed keep their files; their next sync
says it was removed from the registry and how to uninstall it.

The folder-less form is a subcommand, so a package folder that is itself
named delete is proposed as ./delete.`,
		Example: `  loadout propose delete lime/lime-app-dev --reason "replaced by lime/planner"`,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := pack.ParseID(args[0])
			if err != nil {
				return err
			}
			if strings.TrimSpace(reason) == "" {
				return errors.New("say why with --reason TEXT: the record of a deletion keeps it")
			}
			c, r, err := d.connectFor(remoteName, id.Team(), "")
			if err != nil {
				return err
			}
			del, err := c.AskDelete(cmd.Context(), api.DeletionRequest{Package: id.String(), Reason: reason})
			if err != nil {
				return notOnRegistry(err, r.Name)
			}
			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "asked to delete %s on %s as %s\n", del.Package, r.Name, del.ID)
			fmt.Fprintf(out, "a maintainer or owner of %s who is not you approves it with: loadout review approve %s\n", id.Team(), del.ID)
			fmt.Fprintf(out, "you can take it back with: loadout review withdraw %s\n", del.ID)
			return nil
		},
	}
	cmd.Flags().StringVar(&remoteName, "remote", "", "registry to ask (default: the one bound to the package's team, else the default remote)")
	cmd.Flags().StringVar(&reason, "reason", "", "why the package should be deleted (required)")
	return cmd
}

// notOnRegistry says what an older registry's 404 on the deletion routes
// means, and passes other errors on.
func notOnRegistry(err error, remote string) error {
	if apiErr, ok := errors.AsType[*client.APIError](err); ok && apiErr.Status == http.StatusNotFound && apiErr.Code == "" {
		return fmt.Errorf("%s does not take requests to delete a package yet; ask whoever runs it to update loadout", remote)
	}
	return err
}

// deletionStatusText is a request's status in a listing.
func deletionStatusText(d api.Deletion) string {
	switch {
	case d.SelfApproved:
		return d.Status + " without review"
	case d.Status == api.StatusGone:
		return "gone: deleted another way"
	}
	return d.Status
}

// listedDeletions reads the requests of the team in view for `review list`,
// of package pkg when given. An older registry has no such route, which
// means none.
func listedDeletions(cmd *cobra.Command, c *client.Client, pkg string, all bool) ([]api.Deletion, error) {
	status := api.StatusOpen
	if all {
		status = api.StatusAny
	}
	team := ""
	if pkg != "" {
		id, err := pack.ParseID(pkg)
		if err != nil {
			return nil, err
		}
		team = id.Team()
	}
	ds, err := c.Deletions(cmd.Context(), team, status)
	if apiErr, ok := errors.AsType[*client.APIError](err); ok && apiErr.Status == http.StatusNotFound && apiErr.Code == "" {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if pkg != "" {
		ds = slices.DeleteFunc(ds, func(d api.Deletion) bool { return d.Package != pkg })
	}
	return ds, nil
}

func showDeletion(cmd *cobra.Command, d Deps, at *reviewTarget, id string) error {
	c, _, err := at.connect(d, "")
	if err != nil {
		return err
	}
	del, err := c.Deletion(cmd.Context(), id)
	if err != nil {
		return notOnRegistry(err, "the registry")
	}
	writeDeletion(cmd.OutOrStdout(), del)
	return nil
}

func writeDeletion(out io.Writer, d api.Deletion) {
	fmt.Fprintf(out, "Request %s: delete %s (%s)\n", d.ID, d.Package, deletionStatusText(d))
	fmt.Fprintf(out, "Asked by %s on %s\n", actor(d.Author), d.CreatedAt.Local().Format("2006-01-02 15:04"))
	fmt.Fprintf(out, "\nReason:\n%s\n", indent(d.Reason))
	if d.Status == api.StatusOpen {
		if d.Stale {
			fmt.Fprintf(out, "\n%s came out after this was asked, so it cannot be approved. Withdraw it and ask again.\n", d.Latest)
		} else {
			fmt.Fprintf(out, "\nApprove it with: loadout review approve %s (a second person; an owner may add --as-owner --note WHY)\n", d.ID)
		}
		return
	}
	if d.DecidedAt != nil && d.DecidedBy != nil {
		fmt.Fprintf(out, "\n%s by %s on %s", strings.ToUpper(d.Status[:1])+d.Status[1:], actor(*d.DecidedBy), d.DecidedAt.Local().Format("2006-01-02 15:04"))
		if d.DecisionNote != "" {
			fmt.Fprintf(out, ": %s", d.DecisionNote)
		}
		fmt.Fprintln(out)
	}
	if d.SelfApproved {
		fmt.Fprintln(out, "Deleted without review")
	}
	if d.Removed != nil {
		fmt.Fprintf(out, "Removed %s, %s, %s, %s\n", plural(len(d.Removed.Releases), "release", "releases"),
			plural(d.Removed.Proposals, "proposal", "proposals"), plural(d.Removed.Reviews, "review", "reviews"), plural(d.Removed.Prompts, "prompt", "prompts"))
	}
}

func approveDeletion(cmd *cobra.Command, d Deps, at *reviewTarget, id string, req api.DecisionRequest) error {
	if req.Over != "" {
		return errors.New("--over does not apply to a request to delete a package")
	}
	c, r, err := at.connect(d, "")
	if err != nil {
		return err
	}
	del, err := c.ApproveDelete(cmd.Context(), id, req)
	if apiErr, ok := errors.AsType[*client.APIError](err); ok && apiErr.Code == api.CodeBehind {
		// The registry's sentence says what came out and what to do.
		return errors.New(strings.TrimPrefix(apiErr.Message, api.ErrBehind.Error()+": "))
	}
	if err != nil {
		return err
	}
	what := "deleted " + del.Package + " on " + r.Name
	if del.SelfApproved {
		what = "deleted " + del.Package + " on " + r.Name + " on your own request, without review"
	}
	counts := ""
	if rm := del.Removed; rm != nil {
		counts = fmt.Sprintf(": %s, %s, %s, %s", plural(len(rm.Releases), "release", "releases"), plural(rm.Proposals, "proposal", "proposals"),
			plural(rm.Reviews, "review", "reviews"), plural(rm.Prompts, "prompt", "prompts"))
	}
	fmt.Fprintf(cmd.OutOrStdout(), "%s%s; machines that had it keep their files, and their next sync says it was removed\n", what, counts)
	return nil
}

func closeDeletion(cmd *cobra.Command, d Deps, at *reviewTarget, verb, id, note string) error {
	c, _, err := at.connect(d, "")
	if err != nil {
		return err
	}
	var del api.Deletion
	if verb == "reject" {
		del, err = c.RejectDelete(cmd.Context(), id, note)
	} else {
		del, err = c.WithdrawDelete(cmd.Context(), id, note)
	}
	if err != nil {
		return err
	}
	fmt.Fprintf(cmd.OutOrStdout(), "%s %s (delete %s)\n", del.Status, del.ID, del.Package)
	return nil
}
