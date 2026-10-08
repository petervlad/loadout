package cli

import (
	"fmt"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/petervlad/loadout/internal/account"
	"github.com/petervlad/loadout/internal/api"
)

// Member commands run on the registry host (--data, as the operator) or
// against a registry over HTTP (--remote, as the user signed in there with
// loadout login, whose role decides what they may change).
func newMemberCmd(d Deps) *cobra.Command {
	var dataDir, remoteName string
	cmd := &cobra.Command{
		Use:   "member",
		Short: "Manage who belongs to a namespace, and their role",
		Long: `Roles: member (install, sync, propose), maintainer (also publish, approve,
manage members and machine tokens of the team), owner (on a project: create
teams below it, make their maintainers; a maintainer of every team below).
Maintainers add members; owners of the project above add maintainers;
admins add owners.

A person needs no account to be added: they sign up with the same address
(or Google), then run loadout login. Removing a member revokes the tokens
they got with loadout login.

On the registry host (a registry database in --data) the commands act as
the operator. Elsewhere, or with --remote, they act on the default remote
(or the one named) as the user signed in there with loadout login.`,
	}
	f := cmd.PersistentFlags()
	f.StringVar(&dataDir, "data", d.defaultDataDir(), "registry data folder (on the registry host)")
	f.StringVar(&remoteName, "remote", "", "manage this registry over HTTP, as the user you signed in as with loadout login")
	cmd.AddCommand(
		newMemberAddCmd(d, &dataDir, &remoteName, false),
		newMemberAddCmd(d, &dataDir, &remoteName, true),
		newMemberInviteCmd(d, &dataDir, &remoteName),
		newMemberListCmd(d, &dataDir, &remoteName),
		newMemberRemoveCmd(d, &dataDir, &remoteName),
	)
	return cmd
}

func newMemberAddCmd(d Deps, dataDir, remoteName *string, set bool) *cobra.Command {
	var namespace, email, role string
	use, short := "add", "Add a member, or change their role"
	if set {
		use, short = "set", "Change the role of an existing member"
	}
	cmd := &cobra.Command{
		Use:   use,
		Short: short,
		Example: `  loadout member add --namespace payments --email alice@example.com --role maintainer
  loadout member add --namespace acme --email olga@example.com --role owner
  loadout member set --namespace payments --email alice@example.com --role member --remote office`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if !account.Role(role).Valid() {
				return fmt.Errorf("--role %q: want member, maintainer or owner", role)
			}
			var resp api.MemberResponse
			err := d.withManager(cmd.Context(), *dataDir, *remoteName, func(m manager) error {
				var err error
				if set {
					resp, err = m.SetMember(cmd.Context(), namespace, email, role)
				} else {
					resp, err = m.AddMember(cmd.Context(), namespace, email, role)
				}
				return err
			})
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			article := "a"
			if strings.ContainsAny(resp.Member.Role[:1], "aeiou") {
				article = "an"
			}
			fmt.Fprintf(out, "%s is %s %s of %s\n", resp.Member.Email, article, resp.Member.Role, namespace)
			if resp.Created {
				fmt.Fprintln(out, "They have no account yet: they sign up with this address (or Google), then run loadout login.")
			}
			return nil
		},
	}
	fl := cmd.Flags()
	fl.StringVar(&namespace, "namespace", "", "namespace (required)")
	fl.StringVar(&email, "email", "", "the member's email address (required)")
	defaultRole := string(account.RoleMember)
	if set {
		defaultRole = ""
	}
	fl.StringVar(&role, "role", defaultRole, "member, maintainer or owner (owner only on a project)")
	_ = cmd.MarkFlagRequired("namespace") // only fails for an unknown flag name
	_ = cmd.MarkFlagRequired("email")     // only fails for an unknown flag name
	if set {
		_ = cmd.MarkFlagRequired("role") // only fails for an unknown flag name
	}
	return cmd
}

func newMemberInviteCmd(d Deps, dataDir, remoteName *string) *cobra.Command {
	var project, email, role string
	var teamFlags []string
	cmd := &cobra.Command{
		Use:   "invite",
		Short: "Add a person to a project and some of its teams, and get a sign-in link for them",
		Long: `Gives the person a role in the project and in each team named with --team, in
one step: if any role is not yours to give, nothing changes. A role they
already have is raised, never lowered.

It prints a sign-in link with their address filled in. The link holds no
secret; it only helps them sign up with the right address. When the
registry can send mail, it also mails them the invitation.`,
		Example: `  loadout member invite --namespace acme --email alice@example.com
  loadout member invite --namespace acme --email alice@example.com --team payments=maintainer --team mobile=member`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			req := api.InviteRequest{Email: email, Role: role, Teams: map[string]string{}}
			for _, t := range teamFlags {
				name, r, ok := strings.Cut(t, "=")
				if !ok {
					r = string(account.RoleMember)
				}
				if name == "" || (r != string(account.RoleMember) && r != string(account.RoleMaintainer)) {
					return fmt.Errorf("--team %q: want TEAM or TEAM=member or TEAM=maintainer", t)
				}
				if _, dup := req.Teams[name]; dup {
					return fmt.Errorf("--team %s is given twice", name)
				}
				req.Teams[name] = r
			}
			var resp api.InviteResponse
			err := d.withManager(cmd.Context(), *dataDir, *remoteName, func(m manager) error {
				var err error
				resp, err = m.Invite(cmd.Context(), project, req)
				return err
			})
			if err != nil {
				return err
			}
			printInvitation(cmd, resp)
			return nil
		},
	}
	fl := cmd.Flags()
	fl.StringVar(&project, "namespace", "", "the project (required)")
	fl.StringVar(&email, "email", "", "the person's email address (required)")
	fl.StringVar(&role, "role", string(account.RoleMember), "their role in the project: member, maintainer or owner")
	fl.StringArrayVar(&teamFlags, "team", nil, "a team of the project, as TEAM=member or TEAM=maintainer (repeatable)")
	_ = cmd.MarkFlagRequired("namespace") // only fails for an unknown flag name
	_ = cmd.MarkFlagRequired("email")     // only fails for an unknown flag name
	return cmd
}

// printInvitation says what an invitation did in each namespace, and how
// the person signs in.
func printInvitation(cmd *cobra.Command, resp api.InviteResponse) {
	out := cmd.OutOrStdout()
	w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "NAMESPACE\tROLE\tRESULT")
	changed := false
	for _, g := range resp.Grants {
		result := "added"
		switch g.Result {
		case string(account.GrantRaised):
			result = "raised from " + g.Was
			changed = true
		case string(account.GrantKept):
			result = "unchanged, already " + g.Role
		default:
			changed = true
		}
		fmt.Fprintf(w, "%s\t%s\t%s\n", g.Namespace, g.Asked, result)
	}
	_ = w.Flush()
	fmt.Fprintln(out)
	switch {
	case resp.Mailed:
		fmt.Fprintf(out, "Mailed %s an invitation with this sign-in link:\n", resp.Email)
	case !changed:
		fmt.Fprintln(out, "Nothing changed, so nothing was mailed. Their sign-in link:")
	default:
		fmt.Fprintln(out, "This registry sends no mail, so send them this sign-in link yourself:")
	}
	if resp.Link == "" {
		fmt.Fprintln(out, "  (none: the registry has no public URL; they sign up on its web page with this address)")
		return
	}
	fmt.Fprintln(out, "  "+resp.Link)
	fmt.Fprintln(out, "They sign in or create an account with that address, then run loadout login.")
}

func newMemberListCmd(d Deps, dataDir, remoteName *string) *cobra.Command {
	var namespace string
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List a namespace's members and their roles",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			var ms []api.Member
			err := d.withManager(cmd.Context(), *dataDir, *remoteName, func(m manager) error {
				var err error
				ms, err = m.Members(cmd.Context(), namespace)
				return err
			})
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if len(ms) == 0 {
				fmt.Fprintf(out, "%s has no members; add one with: loadout member add --namespace %s --email EMAIL\n", namespace, namespace)
				return nil
			}
			w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "EMAIL\tROLE\tADDED")
			for _, m := range ms {
				fmt.Fprintf(w, "%s\t%s\t%s\n", m.Email, m.Role, m.CreatedAt.Local().Format("2006-01-02 15:04"))
			}
			return w.Flush()
		},
	}
	cmd.Flags().StringVar(&namespace, "namespace", "", "namespace (required)")
	_ = cmd.MarkFlagRequired("namespace") // only fails for an unknown flag name
	return cmd
}

func newMemberRemoveCmd(d Deps, dataDir, remoteName *string) *cobra.Command {
	var namespace, email string
	cmd := &cobra.Command{
		Use:   "remove",
		Short: "Remove a member and revoke the tokens they got with loadout login",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			var n int
			err := d.withManager(cmd.Context(), *dataDir, *remoteName, func(m manager) error {
				var err error
				n, err = m.RemoveMember(cmd.Context(), namespace, email)
				return err
			})
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "removed %s from %s; revoked %d token(s)\n", email, namespace, n)
			return nil
		},
	}
	f := cmd.Flags()
	f.StringVar(&namespace, "namespace", "", "namespace (required)")
	f.StringVar(&email, "email", "", "the member's email address (required)")
	_ = cmd.MarkFlagRequired("namespace") // only fails for an unknown flag name
	_ = cmd.MarkFlagRequired("email")     // only fails for an unknown flag name
	return cmd
}

func newUserCmd(d Deps) *cobra.Command {
	var dataDir string
	cmd := &cobra.Command{
		Use:   "user",
		Short: "Manage user accounts (run on the registry host)",
	}
	cmd.PersistentFlags().StringVar(&dataDir, "data", d.defaultDataDir(), "registry data folder")

	var email string
	del := &cobra.Command{
		Use:   "delete",
		Short: "Delete an account with its memberships, sessions and login tokens",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			var n int
			err := withRegistry(cmd.Context(), d, dataDir, func(reg Registry) error {
				var err error
				n, err = reg.Accounts.DeleteUser(cmd.Context(), account.Operator(), email)
				return err
			})
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "deleted %s; revoked %d token(s)\n", email, n)
			return nil
		},
	}
	del.Flags().StringVar(&email, "email", "", "the user's email address (required)")
	_ = del.MarkFlagRequired("email") // only fails for an unknown flag name
	cmd.AddCommand(del)
	cmd.AddCommand(newUserPasswordCmd(d, &dataDir))
	return cmd
}
