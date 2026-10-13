package cli

import (
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/spf13/cobra"
)

func newMemoryCmd(d Deps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "memory",
		Short: "Manage team memory",
	}
	cmd.AddCommand(newMemoryTokenCmd(d), newMemoryPurgeCmd(d), newMemoryImportCmd(d))
	return cmd
}

// A memory token lets your AI tools read and save your teams' memory and
// nothing else. These commands talk to a registry over HTTP as the person
// signed in there with loadout login.
func newMemoryTokenCmd(d Deps) *cobra.Command {
	var remoteName string
	cmd := &cobra.Command{
		Use:   "token",
		Short: "Manage your memory tokens",
		Long: `A memory token lets your AI tools read and save your teams' memory. It works
on the memory server only: it cannot install, publish or change anything
else. Each one lasts 90 days.

The commands act on the default remote (or --remote) as the user signed in
there with loadout login.`,
	}
	cmd.PersistentFlags().StringVar(&remoteName, "remote", "", "act on this registry (default: the default remote)")
	cmd.AddCommand(newMemoryTokenCreateCmd(d, &remoteName), newMemoryTokenListCmd(d, &remoteName), newMemoryTokenRevokeCmd(d, &remoteName))
	return cmd
}

func newMemoryTokenCreateCmd(d Deps, remoteName *string) *cobra.Command {
	var label string
	var show bool
	cmd := &cobra.Command{
		Use:   "create [--label TEXT] [--show]",
		Short: "Make a memory token and store it on this machine",
		Long: `Make a memory token and keep it in ~/.loadout/memory-credentials.yaml
(readable by you only), where loadout mcp finds it. The token is not
printed, so it stays out of your terminal history. With --show it is
printed once as well, for an MCP client you configure by hand.
LOADOUT_MEMORY_TOKEN overrides the stored token.`,
		Example: "  loadout memory token create\n  loadout memory token create --label work-laptop --remote office",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, r, err := d.connect(*remoteName)
			if err != nil {
				return err
			}
			if label == "" {
				label = DefaultTokenLabel()
			}
			had, err := d.Remotes.StoredMemoryToken(r.Name)
			if err != nil {
				return err
			}
			tok, err := c.CreateMemoryToken(cmd.Context(), label)
			if err != nil {
				return err
			}
			// The secret exists only in this answer: store it before anything
			// else can fail.
			if err := d.Remotes.SetMemoryTokenMade(r.Name, tok.Token, tok.ID, tok.ExpiresAt); err != nil {
				return fmt.Errorf("the registry made memory token %s, but it could not be stored: %w. Revoke it with loadout memory token revoke %s and try again", tok.ID, err, tok.ID)
			}
			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "Made memory token %s (%s) for %s. It expires on %s and is stored on this machine.\n", tok.ID, label, r.Name, day(tok.ExpiresAt.Local()))
			if had != "" {
				fmt.Fprintln(out, "It replaces the memory token stored before. That one still works until it expires or you revoke it: loadout memory token list.")
			}
			if show {
				fmt.Fprintf(out, "\n  %s\n\nIt is shown only now.\n", tok.Token)
			}
			return nil
		},
	}
	f := cmd.Flags()
	f.StringVar(&label, "label", "", "where the token is used (default: this machine's name)")
	f.BoolVar(&show, "show", false, "also print the token, once")
	return cmd
}

func newMemoryTokenListCmd(d Deps, remoteName *string) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List your memory tokens",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, r, err := d.connect(*remoteName)
			if err != nil {
				return err
			}
			toks, err := c.MemoryTokens(cmd.Context())
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if len(toks) == 0 {
				fmt.Fprintf(out, "You have no memory tokens on %s. Make one with: loadout memory token create\n", r.Name)
				return nil
			}
			w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "ID\tLABEL\tCREATED\tEXPIRES")
			for _, t := range toks {
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", t.ID, dashIfEmpty(t.Label), day(t.CreatedAt.Local()), day(t.ExpiresAt.Local()))
			}
			return w.Flush()
		},
	}
}

func newMemoryTokenRevokeCmd(d Deps, remoteName *string) *cobra.Command {
	return &cobra.Command{
		Use:   "revoke ID",
		Short: "Revoke one of your memory tokens at once",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, _, err := d.connect(*remoteName)
			if err != nil {
				return err
			}
			if err := c.RevokeMemoryToken(cmd.Context(), args[0]); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Revoked memory token %s. A tool using it can no longer reach team memory.\n", args[0])
			return nil
		},
	}
}

// DefaultTokenLabel names this machine for a memory token it makes.
func DefaultTokenLabel() string {
	if h, err := os.Hostname(); err == nil && h != "" {
		return h[:min(len(h), 80)] // the registry accepts labels up to 80 characters
	}
	return "loadout"
}
