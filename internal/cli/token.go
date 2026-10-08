package cli

import (
	"errors"
	"fmt"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/petervlad/loadout/internal/invite"
	"github.com/petervlad/loadout/internal/registry"
)

// Token commands work directly on a registry's database, so they run on the
// machine that hosts the registry. With --remote, a team's maintainers
// manage its machine tokens over HTTP.
func newTokenCmd(d Deps) *cobra.Command {
	var dataDir, remoteName string
	cmd := &cobra.Command{
		Use:   "token",
		Short: "Manage registry access tokens (machine tokens for CI and shared machines)",
		Long: `On the registry host (a registry database in --data) the commands act as
the operator. Elsewhere, or with --remote, a maintainer of the team manages
its machine tokens on the default remote (or the one named), as the user
signed in there with loadout login.`,
	}
	f := cmd.PersistentFlags()
	f.StringVar(&dataDir, "data", d.defaultDataDir(), "registry data folder (on the registry host)")
	f.StringVar(&remoteName, "remote", "", "manage the team's machine tokens on this registry over HTTP")
	cmd.AddCommand(newTokenCreateCmd(d, &dataDir, &remoteName), newTokenListCmd(d, &dataDir, &remoteName), newTokenRevokeCmd(d, &dataDir, &remoteName))
	return cmd
}

func newTokenCreateCmd(d Deps, dataDir, remoteName *string) *cobra.Command {
	var team, label, publicURL string
	var read, publish, asInvite bool
	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create a token or a team invite; it is shown once",
		Long: `Create an access token for one team. With --invite, print an invite
instead: one string holding the registry URL, the team and the token, which a
teammate passes to "loadout team join". --url is the address teammates use to
reach this registry, which may differ from the one it listens on.`,
		Example: `  loadout token create --team payments --read --invite --url https://registry.example.com
  loadout token create --team payments --publish --label alice-laptop`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if read == publish {
				return errors.New("choose exactly one of --read or --publish")
			}
			scope, role := registry.ScopeRead, invite.RoleRead
			if publish {
				scope, role = registry.ScopePublish, invite.RolePublish
			}
			if asInvite {
				if publicURL == "" {
					return errors.New("--invite needs --url, the registry address teammates use (for example https://registry.example.com)")
				}
				// Check the invite before creating a token it would carry.
				probe := invite.Invite{URL: strings.TrimRight(publicURL, "/"), Namespace: team, Token: registry.TokenPrefix + "probe", Role: role}
				if err := probe.Validate(); err != nil {
					return fmt.Errorf("--url or --team: %w", err)
				}
			} else if publicURL != "" {
				return errors.New("--url is only used with --invite")
			}
			var secret string
			var tok registry.Token
			var err error
			if remote := d.manageRemote(*dataDir, *remoteName); remote != "" {
				err = d.withManager(cmd.Context(), *dataDir, remote, func(m manager) error {
					created, err := m.CreateMachineToken(cmd.Context(), team, string(scope), label)
					secret = created.Secret
					tok = registry.Token{ID: created.Token.ID, Team: created.Token.Namespace, Scope: registry.TokenScope(created.Token.Scope), Label: created.Token.Label}
					return err
				})
			} else {
				err = withRegistry(cmd.Context(), d, *dataDir, func(reg Registry) error {
					if _, err := reg.Service.Namespace(cmd.Context(), team); errors.Is(err, registry.ErrNotFound) {
						return fmt.Errorf("namespace %s does not exist; create it first: loadout namespace create %s --parent PROJECT", team, team)
					} else if err != nil {
						return err
					}
					var err error
					secret, tok, err = reg.Service.CreateToken(cmd.Context(), team, scope, label)
					return err
				})
			}
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if asInvite {
				code, err := invite.Invite{URL: publicURL, Namespace: tok.Team, Token: secret, Role: role}.Encode()
				if err != nil {
					return fmt.Errorf("token %s was created, but the invite could not be built (revoke it): %w", tok.ID, err)
				}
				fmt.Fprintf(out, "invite for team %s (%s, token %s):\n\n  %s\n\n", tok.Team, tok.Scope, tok.ID, code)
				fmt.Fprintln(out, "It is shown only now and contains a secret: send it privately. The teammate")
				fmt.Fprintln(out, "saves it to a file and runs:")
				fmt.Fprintln(out, "  loadout team join < invite.txt")
				return nil
			}
			fmt.Fprintf(out, "token %s (%s, namespace %s):\n\n  %s\n\n", tok.ID, tok.Scope, tok.Team, secret)
			fmt.Fprintln(out, "It is shown only now. Give it to the teammate, who runs:")
			fmt.Fprintln(out, "  loadout remote add NAME URL --token-stdin")
			return nil
		},
	}
	f := cmd.Flags()
	f.StringVar(&team, "team", "", "namespace the token belongs to; it must exist (required)")
	f.BoolVar(&read, "read", false, "token can list and install")
	f.BoolVar(&publish, "publish", false, "token can also publish")
	f.StringVar(&label, "label", "", "who or what the token is for")
	f.BoolVar(&asInvite, "invite", false, "print a team invite (for: loadout team join) instead of a bare token")
	f.StringVar(&publicURL, "url", "", "with --invite: the registry URL teammates use")
	_ = cmd.MarkFlagRequired("team") // only fails for an unknown flag name
	return cmd
}

func newTokenListCmd(d Deps, dataDir, remoteName *string) *cobra.Command {
	var team string
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List a team's tokens (with --remote: its machine tokens)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			var toks []registry.Token
			var err error
			if remote := d.manageRemote(*dataDir, *remoteName); remote != "" {
				err = d.withManager(cmd.Context(), *dataDir, remote, func(m manager) error {
					list, err := m.MachineTokens(cmd.Context(), team)
					for _, t := range list {
						toks = append(toks, registry.Token{ID: t.ID, Team: t.Namespace, Scope: registry.TokenScope(t.Scope), Label: t.Label, CreatedAt: t.CreatedAt})
					}
					return err
				})
			} else {
				err = withRegistry(cmd.Context(), d, *dataDir, func(reg Registry) error {
					var err error
					toks, err = reg.Service.Tokens(cmd.Context(), team)
					return err
				})
			}
			if err != nil {
				return err
			}
			w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "ID\tSCOPE\tLABEL\tCREATED")
			for _, t := range toks {
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", t.ID, t.Scope, t.Label, t.CreatedAt.Format("2006-01-02 15:04"))
			}
			return w.Flush()
		},
	}
	cmd.Flags().StringVar(&team, "team", "", "team (required)")
	_ = cmd.MarkFlagRequired("team") // only fails for an unknown flag name
	return cmd
}

func newTokenRevokeCmd(d Deps, dataDir, remoteName *string) *cobra.Command {
	var team string
	cmd := &cobra.Command{
		Use:   "revoke ID",
		Short: "Revoke a token immediately",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			var err error
			if remote := d.manageRemote(*dataDir, *remoteName); remote != "" {
				if team == "" {
					return errors.New("--team is required on a remote registry: the token's namespace")
				}
				err = d.withManager(cmd.Context(), *dataDir, remote, func(m manager) error {
					return m.RevokeMachineToken(cmd.Context(), team, args[0])
				})
			} else {
				err = withRegistry(cmd.Context(), d, *dataDir, func(reg Registry) error {
					return reg.Service.RevokeToken(cmd.Context(), args[0])
				})
			}
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "revoked %s\n", args[0])
			return nil
		},
	}
	cmd.Flags().StringVar(&team, "team", "", "with --remote: the namespace the token belongs to")
	return cmd
}
