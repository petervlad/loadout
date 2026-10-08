package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/petervlad/loadout/internal/account"
	"github.com/petervlad/loadout/internal/api"
	"github.com/petervlad/loadout/internal/client"
	"github.com/petervlad/loadout/internal/pack"
	"github.com/petervlad/loadout/internal/registry"
)

// manager manages namespaces, members and machine tokens, either on the
// registry host as the operator or over HTTP as the signed-in user.
type manager interface {
	CreateNamespace(ctx context.Context, name, parent, kind string) (api.Namespace, error)
	DeleteNamespace(ctx context.Context, name string) error
	SetRequireReview(ctx context.Context, name string, on bool) (api.Namespace, error)
	SetWiki(ctx context.Context, name string, on bool) (api.Namespace, error)
	SetCourse(ctx context.Context, name, course string) (api.Namespace, error)
	NamespaceTiers(ctx context.Context, namespace string) (api.TiersResponse, error)
	SetTiers(ctx context.Context, namespace string, cells []api.TierCell) (api.TiersResponse, error)
	Members(ctx context.Context, namespace string) ([]api.Member, error)
	AddMember(ctx context.Context, namespace, email, role string) (api.MemberResponse, error)
	SetMember(ctx context.Context, namespace, email, role string) (api.MemberResponse, error)
	RemoveMember(ctx context.Context, namespace, email string) (int, error)
	Invite(ctx context.Context, project string, req api.InviteRequest) (api.InviteResponse, error)
	MachineTokens(ctx context.Context, namespace string) ([]api.Token, error)
	CreateMachineToken(ctx context.Context, namespace, scope, label string) (api.TokenCreated, error)
	RevokeMachineToken(ctx context.Context, namespace, id string) error
}

var _ manager = (*client.Client)(nil)

// withManager runs fn against the registry named by remote (over HTTP, with
// its stored login token), or, without remote, against the database in
// dataDir as the operator.
func (d Deps) withManager(ctx context.Context, dataDir, remote string, fn func(manager) error) error {
	if remote = d.manageRemote(dataDir, remote); remote != "" {
		c, _, err := d.connect(remote)
		if err != nil {
			return err
		}
		return fn(c)
	}
	return withRegistry(ctx, d, dataDir, func(reg Registry) error {
		return fn(hostManager{a: reg.Accounts})
	})
}

// manageRemote picks where a management command acts: the --remote it was
// given; else, on a machine that runs no registry (no --data and no
// registry database in the default folder), the default remote, so a
// laptop needs no --remote after loadout login. It returns "" for the
// registry host.
func (d Deps) manageRemote(dataDir, remote string) string {
	if remote != "" || dataDir != d.defaultDataDir() {
		return remote
	}
	if _, err := os.Stat(filepath.Join(dataDir, "registry.db")); err == nil {
		return ""
	}
	cfg, err := d.Remotes.Load()
	if err != nil || len(cfg.Remotes) == 0 {
		return ""
	}
	r, err := cfg.ResolveFor("", "", "")
	if err != nil {
		return ""
	}
	return r.Name
}

// siteOf is the address of the registry a management command acts on, or
// "" on the registry host.
func (d Deps) siteOf(dataDir, remote string) string {
	if remote = d.manageRemote(dataDir, remote); remote == "" {
		return ""
	}
	cfg, err := d.Remotes.Load()
	if err != nil {
		return ""
	}
	r, err := cfg.ResolveFor(remote, "", "")
	if err != nil {
		return ""
	}
	return r.URL
}

// hostManager is the operator on the registry host: every rule allows it.
type hostManager struct{ a *account.Service }

func (h hostManager) CreateNamespace(ctx context.Context, name, parent, kind string) (api.Namespace, error) {
	ns, err := h.a.CreateNamespace(ctx, account.Operator(), name, parent, registry.NamespaceKind(kind))
	return apiNamespace(ns), err
}

func (h hostManager) DeleteNamespace(ctx context.Context, name string) error {
	return h.a.DeleteNamespace(ctx, account.Operator(), name)
}

func (h hostManager) SetRequireReview(ctx context.Context, name string, on bool) (api.Namespace, error) {
	ns, err := h.a.SetRequireReview(ctx, account.Operator(), name, on)
	return apiNamespace(ns), err
}

func (h hostManager) SetWiki(ctx context.Context, name string, on bool) (api.Namespace, error) {
	ns, err := h.a.SetWiki(ctx, account.Operator(), name, on)
	return apiNamespace(ns), err
}

func (h hostManager) SetCourse(ctx context.Context, name, course string) (api.Namespace, error) {
	var id pack.ID
	if course != "" {
		var err error
		if id, err = pack.ParseID(course); err != nil {
			return api.Namespace{}, fmt.Errorf("--course %q: name a knowledge base as team/name, or none", course)
		}
	}
	ns, err := h.a.SetCourse(ctx, account.Operator(), name, id)
	if err != nil {
		return api.Namespace{}, err
	}
	out := apiNamespace(ns)
	if id.IsZero() {
		above, ok, err := h.a.CourseAbove(ctx, account.Operator(), name)
		if err != nil {
			return api.Namespace{}, err
		}
		if ok {
			out.CourseAbove, out.CourseFrom = above.Package.String(), above.From
		}
	}
	return out, nil
}

func (h hostManager) NamespaceTiers(ctx context.Context, namespace string) (api.TiersResponse, error) {
	st, err := h.a.ModelTiers(ctx, account.Operator(), namespace)
	return apiTiers(st), err
}

func (h hostManager) SetTiers(ctx context.Context, namespace string, cells []api.TierCell) (api.TiersResponse, error) {
	t, err := tableOf(cells, "")
	if err != nil {
		return api.TiersResponse{}, err
	}
	st, err := h.a.SetModelTiers(ctx, account.Operator(), namespace, t)
	return apiTiers(st), err
}

func (h hostManager) Members(ctx context.Context, namespace string) ([]api.Member, error) {
	ms, err := h.a.Members(ctx, account.Operator(), namespace)
	out := make([]api.Member, 0, len(ms))
	for _, m := range ms {
		out = append(out, apiMember(m))
	}
	return out, err
}

func (h hostManager) AddMember(ctx context.Context, namespace, email, role string) (api.MemberResponse, error) {
	m, created, err := h.a.AddMember(ctx, account.Operator(), namespace, email, account.Role(role))
	return api.MemberResponse{Member: apiMember(m), Created: created}, err
}

func (h hostManager) SetMember(ctx context.Context, namespace, email, role string) (api.MemberResponse, error) {
	m, err := h.a.SetMember(ctx, account.Operator(), namespace, email, account.Role(role))
	return api.MemberResponse{Member: apiMember(m)}, err
}

func (h hostManager) RemoveMember(ctx context.Context, namespace, email string) (int, error) {
	return h.a.RemoveMember(ctx, account.Operator(), namespace, email)
}

func (h hostManager) Invite(ctx context.Context, project string, req api.InviteRequest) (api.InviteResponse, error) {
	teams := make(map[string]account.Role, len(req.Teams))
	for name, role := range req.Teams {
		teams[name] = account.Role(role)
	}
	inv, err := h.a.InviteToProject(ctx, account.Operator(), project, req.Email, account.Role(req.Role), teams)
	return apiInvitation(inv), err
}

func (h hostManager) MachineTokens(ctx context.Context, namespace string) ([]api.Token, error) {
	toks, err := h.a.MachineTokens(ctx, account.Operator(), namespace)
	out := make([]api.Token, 0, len(toks))
	for _, t := range toks {
		out = append(out, apiToken(t))
	}
	return out, err
}

func (h hostManager) CreateMachineToken(ctx context.Context, namespace, scope, label string) (api.TokenCreated, error) {
	secret, tok, err := h.a.CreateMachineToken(ctx, account.Operator(), namespace, registry.TokenScope(scope), label)
	return api.TokenCreated{Token: apiToken(tok), Secret: secret}, err
}

func (h hostManager) RevokeMachineToken(ctx context.Context, namespace, id string) error {
	return h.a.RevokeMachineToken(ctx, account.Operator(), namespace, id)
}

func apiNamespace(ns registry.Namespace) api.Namespace {
	out := api.Namespace{Name: ns.Name, Parent: ns.Parent, Kind: api.LayerKind(ns.Kind), RequireReview: ns.RequireReview, Wiki: ns.Wiki, CreatedAt: ns.CreatedAt}
	if !ns.Course.IsZero() {
		out.Course = ns.Course.String()
	}
	return out
}

func apiMember(m account.Membership) api.Member {
	return api.Member{Email: m.Email, Role: string(m.Role), CreatedAt: m.CreatedAt}
}

func apiInvitation(inv account.Invitation) api.InviteResponse {
	out := api.InviteResponse{Email: inv.Email, Project: inv.Project, Link: inv.Link, Mailed: inv.Mailed}
	for _, g := range inv.Grants {
		out.Grants = append(out.Grants, api.Grant{Namespace: g.Namespace, Asked: string(g.Asked), Role: string(g.Role), Was: string(g.Was), Result: string(g.Result)})
	}
	return out
}

func apiToken(t registry.Token) api.Token {
	return api.Token{ID: t.ID, Namespace: t.Team, Scope: string(t.Scope), Label: t.Label, CreatedAt: t.CreatedAt}
}

// Admin commands run on the registry host only: the operator decides who
// the admins are, so the first admin needs no admin.
func newAdminCmd(d Deps) *cobra.Command {
	var dataDir string
	cmd := &cobra.Command{
		Use:   "admin",
		Short: "Manage registry admins (run on the registry host)",
		Long: `Admins may do everything on the registry: create and delete namespaces,
make project owners, and act as a maintainer everywhere. The person who runs
the registry host grants the first one.`,
	}
	cmd.PersistentFlags().StringVar(&dataDir, "data", d.defaultDataDir(), "registry data folder")
	set := func(on bool) *cobra.Command {
		var email string
		use, short, verb := "grant", "Make a user an admin (creating the account if needed)", "is now an admin"
		if !on {
			use, short, verb = "revoke", "Take the admin flag away", "is no longer an admin"
		}
		c := &cobra.Command{
			Use:   use,
			Short: short,
			Args:  cobra.NoArgs,
			RunE: func(cmd *cobra.Command, _ []string) error {
				var u account.User
				err := withRegistry(cmd.Context(), d, dataDir, func(reg Registry) error {
					var err error
					u, err = reg.Accounts.SetAdmin(cmd.Context(), account.Operator(), email, on)
					return err
				})
				if err != nil {
					return err
				}
				fmt.Fprintf(cmd.OutOrStdout(), "%s %s\n", u.Email, verb)
				return nil
			},
		}
		c.Flags().StringVar(&email, "email", "", "the user's email address (required)")
		_ = c.MarkFlagRequired("email") // only fails for an unknown flag name
		return c
	}
	list := &cobra.Command{
		Use:   "list",
		Short: "List the admins",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			var us []account.User
			err := withRegistry(cmd.Context(), d, dataDir, func(reg Registry) error {
				var err error
				us, err = reg.Accounts.Admins(cmd.Context(), account.Operator())
				return err
			})
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if len(us) == 0 {
				fmt.Fprintln(out, "no admins; grant one with: loadout admin grant --email EMAIL")
				return nil
			}
			w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "EMAIL\tVERIFIED")
			for _, u := range us {
				fmt.Fprintf(w, "%s\t%s\n", u.Email, yesNo(u.Verified()))
			}
			return w.Flush()
		},
	}
	cmd.AddCommand(set(true), set(false), list)
	return cmd
}
