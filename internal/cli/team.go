package cli

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"slices"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/petervlad/loadout/internal/api"
	"github.com/petervlad/loadout/internal/client"
	"github.com/petervlad/loadout/internal/invite"
	"github.com/petervlad/loadout/internal/remote"
)

// Team commands bind a team's namespace to the registry that holds it, so
// the registry is the team's choice and every command finds it from the
// package id.
func newTeamCmd(d Deps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "team",
		Short: "Join the teams whose packages you use; their registry is found for you",
	}
	cmd.AddCommand(newTeamJoinCmd(d), newTeamListCmd(d), newTeamLeaveCmd(d))
	return cmd
}

func newTeamJoinCmd(d Deps) *cobra.Command {
	var name, caFile string
	cmd := &cobra.Command{
		Use:   "join",
		Short: "Join a team with the invite a team lead gave you (read from standard input)",
		Long: `Read an invite (ldi_...) from standard input, check the registry and the
token, add the registry as a remote (or reuse one with the same URL), store
the token and bind the team's namespace to it. If the registry reports
parent namespaces (layers), they are bound to it too.

The invite contains a secret, so it is never accepted as an argument.`,
		Example: `  loadout team join < invite.txt
  pbpaste | loadout team join`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if caFile != "" {
				caFile = d.abs(caFile)
				if _, err := os.Stat(caFile); err != nil {
					return fmt.Errorf("--ca-file: %w", err)
				}
			}
			line, err := readSecret(cmd.InOrStdin(), "invite")
			if err != nil {
				return err
			}
			inv, err := invite.Decode(line)
			if err != nil {
				return err
			}
			d.Log.Debug("joining team", "invite", inv) // LogValue leaves the token out
			return d.join(cmd, inv, name, caFile)
		},
	}
	f := cmd.Flags()
	f.StringVar(&name, "name", "", "name for a new remote (default: derived from the registry host)")
	f.StringVar(&caFile, "ca-file", "", "extra CA bundle (PEM) for TLS-inspecting proxies, for a new remote")
	return cmd
}

// join checks the registry and the token of inv, adds the registry as a
// remote (or reuses one with the same URL), stores the token and binds the
// team's namespace and its ancestors to it. name and caFile apply to a new
// remote.
func (d Deps) join(cmd *cobra.Command, inv invite.Invite, name, caFile string) error {
	cfg, err := d.Remotes.Load()
	if err != nil {
		return err
	}
	r, isNew, err := d.remoteForInvite(cfg, inv, name, caFile)
	if err != nil {
		return err
	}
	c, err := d.newClient(r, inv.Token)
	if err != nil {
		return err
	}
	if err := checkRemote(cmd, c, r, true, ""); err != nil {
		return err
	}
	ancestors, err := ancestorLayers(cmd, c, inv.Namespace)
	if err != nil {
		return err
	}

	if isNew {
		if err := cfg.Add(r); err != nil {
			return err
		}
	}
	if _, ok := cfg.Binding(inv.Namespace); ok {
		// Rejoining, perhaps on another registry: drop the old binding
		// and the ancestors it brought in first.
		if _, err := cfg.Unbind(inv.Namespace); err != nil {
			return err
		}
	}
	if err := cfg.Bind(inv.Namespace, r.Name, remote.Role(inv.Role)); err != nil {
		return err
	}
	var alsoBound []string
	for _, ns := range ancestors {
		ok, err := cfg.BindAncestor(ns, r.Name, inv.Namespace)
		if err != nil {
			return err
		}
		if !ok {
			b, _ := cfg.Binding(ns)
			d.Log.Warn("parent namespace already comes from another registry; kept it there", "namespace", ns, "remote", b.Remote)
			continue
		}
		alsoBound = append(alsoBound, ns)
	}
	if err := d.Remotes.Save(cfg); err != nil {
		return err
	}
	if err := d.Remotes.SetToken(r.Name, inv.Token); err != nil {
		return err
	}

	out := cmd.OutOrStdout()
	verb := "using remote"
	if isNew {
		verb = "added remote"
	}
	fmt.Fprintf(out, "joined %s on %s (%s %s)\n", inv.Namespace, r.URL, verb, r.Name)
	if len(alsoBound) > 0 {
		fmt.Fprintf(out, "also reads %s from there\n", strings.Join(alsoBound, ", "))
	}
	fmt.Fprintf(out, "try: loadout list, then loadout install %s/NAME\n", inv.Namespace)
	if r.Insecure() {
		d.Log.Warn("this registry uses plain HTTP: the token travels unencrypted; fine on a trusted LAN, use https otherwise", "remote", r.Name)
	}
	return nil
}

// remoteForInvite picks the remote an invite goes to. A remote with the same
// URL is reused when it holds no token, the invite's token, or only this
// team's bindings; otherwise a new remote keeps each stored token with the
// one team it belongs to.
func (d Deps) remoteForInvite(cfg *remote.Config, inv invite.Invite, name, caFile string) (remote.Remote, bool, error) {
	if name == "" {
		for _, r := range cfg.ByURL(inv.URL) {
			tok, err := d.Remotes.StoredToken(r.Name)
			if err != nil {
				return remote.Remote{}, false, err
			}
			if tok == "" || tok == inv.Token || onlyTeam(cfg, r.Name, inv.Namespace) {
				return r, false, nil
			}
		}
		name = cfg.NameFor(inv.URL)
	} else if r, ok := cfg.Get(name); ok {
		if strings.TrimRight(r.URL, "/") != inv.URL {
			return remote.Remote{}, false, fmt.Errorf("remote %s already exists with another URL (%s); choose another --name", name, r.URL)
		}
		return r, false, nil
	}
	r := remote.Remote{Name: name, URL: inv.URL, CAFile: caFile}
	if err := r.Validate(); err != nil {
		return remote.Remote{}, false, err
	}
	return r, true, nil
}

// onlyTeam reports whether every binding to the named remote belongs to
// namespace (directly, or as one of its ancestors), and there is one.
func onlyTeam(cfg *remote.Config, remoteName, namespace string) bool {
	found := false
	for _, b := range cfg.Bindings {
		if b.Remote != remoteName {
			continue
		}
		if b.Namespace != namespace && !slices.Equal(b.Via, []string{namespace}) {
			return false
		}
		found = true
	}
	return found
}

// ancestorLayers returns the parent namespaces the token can read, nearest
// first. A registry without GET /v1/layers has none. It also checks that the
// token belongs to the invite's namespace, when the registry says so.
func ancestorLayers(cmd *cobra.Command, c *client.Client, namespace string) ([]string, error) {
	layers, err := c.Layers(cmd.Context())
	if apiErr, ok := errors.AsType[*client.APIError](err); ok && apiErr.Status == http.StatusNotFound {
		return nil, nil // an older registry: no layers
	}
	if err != nil {
		return nil, fmt.Errorf("read the team's layers: %w", err)
	}
	slices.SortStableFunc(layers, func(a, b api.Layer) int { return a.Depth - b.Depth })
	var out []string
	for _, l := range layers {
		if l.Depth == 0 {
			if l.Namespace != namespace {
				return nil, fmt.Errorf("the invite is for team %s, but its token belongs to %s; ask for a new invite", namespace, l.Namespace)
			}
			continue
		}
		if l.Namespace != namespace && !slices.Contains(out, l.Namespace) {
			out = append(out, l.Namespace)
		}
	}
	return out, nil
}

func newTeamListCmd(d Deps) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List the teams you joined and the registry of each",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := d.Remotes.Load()
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if len(cfg.Bindings) == 0 {
				fmt.Fprintln(out, "no teams; join one with: loadout team join < invite.txt")
				return nil
			}
			w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "NAMESPACE\tREMOTE\tURL\tROLE\tVIA")
			for _, b := range cfg.Bindings {
				u := "(missing remote)"
				if r, ok := cfg.Get(b.Remote); ok {
					u = r.URL
				}
				via := "-"
				if !b.Direct() {
					via = strings.Join(b.Via, ",")
				}
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", b.Namespace, b.Remote, u, dashIfEmpty(string(b.Role)), via)
			}
			return w.Flush()
		},
	}
}

func newTeamLeaveCmd(d Deps) *cobra.Command {
	return &cobra.Command{
		Use:   "leave NAMESPACE",
		Short: "Leave a team: unbind its namespace, and remove its registry if no other team uses it",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return d.leave(cmd, args[0])
		},
	}
}

// leave unbinds namespace and the ancestors only it brought in, removes
// each remote and token no other binding uses, and warns about packages
// installed from them.
func (d Deps) leave(cmd *cobra.Command, ns string) error {
	cfg, err := d.Remotes.Load()
	if err != nil {
		return err
	}
	unbound, err := cfg.Unbind(ns)
	if err != nil {
		return err
	}
	var removed []string
	for _, b := range unbound {
		if slices.Contains(removed, b.Remote) || cfg.Bound(b.Remote) {
			continue
		}
		if _, ok := cfg.Get(b.Remote); !ok {
			continue
		}
		if _, err := cfg.Remove(b.Remote); err != nil {
			return err
		}
		removed = append(removed, b.Remote)
	}
	if err := d.Remotes.Save(cfg); err != nil {
		return err
	}
	for _, name := range removed {
		if err := d.Remotes.SetToken(name, ""); err != nil {
			return err
		}
	}

	out := cmd.OutOrStdout()
	var namespaces []string
	for _, b := range unbound {
		namespaces = append(namespaces, b.Namespace)
	}
	fmt.Fprintf(out, "left %s (unbound %s)\n", ns, strings.Join(namespaces, ", "))
	for _, name := range removed {
		fmt.Fprintf(out, "removed remote %s and its token\n", name)
	}

	entries, err := d.Installer.Entries(cmd.Context())
	if err != nil {
		return err
	}
	for _, e := range entries {
		if e.Remote == "" {
			continue
		}
		_, remoteKept := cfg.Get(e.Remote)
		switch {
		case slices.Contains(removed, e.Remote) || (!remoteKept && slices.Contains(namespaces, e.ID.Team())):
			d.Log.Warn("installed from a team you left; sync can no longer update it (join the team again, or reinstall from another registry)", "id", e.ID, "scope", e.Scope.Kind)
		case slices.Contains(namespaces, e.ID.Team()):
			d.Log.Warn("installed from a team you left; sync now updates it from the remote it was installed from", "id", e.ID, "remote", e.Remote, "scope", e.Scope.Kind)
		}
	}
	return nil
}

// bindingGroup is one registry and the namespaces bound to it.
type bindingGroup struct {
	remote     remote.Remote
	namespaces []string
}

// bindingGroups groups the bindings by remote, in the order first bound.
// Bindings to a missing remote are skipped.
func bindingGroups(cfg *remote.Config) []bindingGroup {
	var groups []bindingGroup
	for _, b := range cfg.Bindings {
		r, ok := cfg.Get(b.Remote)
		if !ok {
			continue
		}
		i := slices.IndexFunc(groups, func(g bindingGroup) bool { return g.remote.Name == r.Name })
		if i < 0 {
			groups = append(groups, bindingGroup{remote: r})
			i = len(groups) - 1
		}
		groups[i].namespaces = append(groups[i].namespaces, b.Namespace)
	}
	return groups
}

func dashIfEmpty(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
