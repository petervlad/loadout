package cli

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/petervlad/loadout/internal/api"
	"github.com/petervlad/loadout/internal/client"
	"github.com/petervlad/loadout/internal/remote"
)

func newLoginCmd(d Deps) *cobra.Command {
	var team, remoteArg, name, caFile string
	var yes bool
	cmd := &cobra.Command{
		Use:   "login",
		Short: "Sign in with your account; one sign-in covers every team you belong to",
		Long: `Sign in to a registry in your browser, like "gh auth login": loadout shows
a code, opens the registry's sign-in page, and waits while you sign in and
approve the code.

You get one token for your account on that registry. It acts in every team
you belong to, with your role in each at the moment of each request, so a
new team or a new role needs no new login. loadout binds each of your teams
and the projects above them to this registry. Signing in again replaces an
older token for the same registry.

Without --remote the default remote is used.`,
		Example: `  loadout login --remote https://registry.example.com
  loadout login`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			if caFile != "" {
				caFile = d.abs(caFile)
				if _, err := os.Stat(caFile); err != nil {
					return fmt.Errorf("--ca-file: %w", err)
				}
			}
			r, err := d.loginRemote(team, remoteArg, caFile)
			if err != nil {
				return err
			}
			c, err := d.newClient(r, "")
			if err != nil {
				return err
			}
			start, err := c.StartDevice(ctx, api.AccountNamespace)
			if err != nil {
				if apiErr, ok := errors.AsType[*client.APIError](err); ok && apiErr.Status == http.StatusNotFound {
					return fmt.Errorf("%s does not offer loadout login; ask its admin for an invite instead", r.URL)
				}
				return fmt.Errorf("start sign-in on %s: %w", r.URL, err)
			}

			out := cmd.OutOrStdout()
			page, complete := devicePages(r.URL, start)
			fmt.Fprintf(out, "Open %s and enter the code %s\n", page, start.UserCode)
			if d.Browser != nil && complete != "" {
				if err := d.Browser.Open(ctx, complete); err != nil {
					d.Log.Debug("could not open a browser", "err", err)
				} else {
					fmt.Fprintln(out, "(opened it in your browser)")
				}
			}
			fmt.Fprintln(out, "Waiting for you to approve...")

			tok, err := d.pollDevice(ctx, c, start)
			if err != nil {
				return err
			}
			if tok.Namespace != api.AccountNamespace {
				d.revokeSecret(cmd, r, tok.Token)
				return fmt.Errorf("the registry issued a token for %s only, not for your account; it was not saved (the registry may need an update)", tok.Namespace)
			}
			if name == "" && remoteArg != "" && !strings.Contains(remoteArg, "://") {
				name = remoteArg
			}
			if err := d.joinAccount(cmd, r, tok, name, caFile); err != nil {
				d.revokeSecret(cmd, r, tok.Token)
				return err
			}
			if !tok.ExpiresAt.IsZero() {
				fmt.Fprintf(out, "the token works until %s; run loadout login again to renew it\n", tok.ExpiresAt.Local().Format(time.DateOnly))
			}
			d.ensureBuiltins(cmd, true)
			d.ensureHooks(cmd)
			if team != "" {
				if cfg, err := d.Remotes.Load(); err == nil {
					if _, ok := cfg.Binding(team); !ok {
						d.Log.Warn("you are not a member of that team on this registry", "team", team)
					}
				}
			}
			return nil
		},
	}
	f := cmd.Flags()
	f.StringVar(&team, "team", "", "check that you belong to this team (the token covers all of them)")
	f.StringVar(&remoteArg, "remote", "", "registry: a remote name or a URL")
	f.StringVar(&name, "name", "", "name for a new remote (default: derived from the registry host)")
	f.StringVar(&caFile, "ca-file", "", "extra CA bundle (PEM) for TLS-inspecting proxies, for a new remote")
	f.BoolVarP(&yes, "yes", "y", false, "accepted for older scripts; nothing is asked any more")
	_ = f.MarkHidden("yes")
	return cmd
}

// joinAccount stores an account token for registry r and binds every team
// the account belongs to, with the projects above them as layers. One
// remote holds the registry: other remotes with the same URL are folded
// into it, their bindings moved and their tokens revoked.
func (d Deps) joinAccount(cmd *cobra.Command, r remote.Remote, tok api.DeviceTokenResponse, name, caFile string) error {
	c, err := d.newClient(r, tok.Token)
	if err != nil {
		return err
	}
	me, err := c.Me(cmd.Context())
	if err != nil {
		return fmt.Errorf("read your teams: %w", err)
	}
	cfg, err := d.Remotes.Load()
	if err != nil {
		return err
	}
	regURL := strings.TrimRight(r.URL, "/")
	same := cfg.ByURL(regURL)
	kept, isNew := remote.Remote{}, true
	for _, cand := range same {
		if name == "" || cand.Name == name {
			kept, isNew = cand, false
			break
		}
	}
	if isNew {
		if name == "" {
			name = cfg.NameFor(regURL)
		} else if other, ok := cfg.Get(name); ok {
			return fmt.Errorf("remote %s already exists with another URL (%s); choose another --name", name, other.URL)
		}
		kept = remote.Remote{Name: name, URL: regURL, CAFile: caFile}
		if err := kept.Validate(); err != nil {
			return err
		}
	}

	// Older remotes of this registry: revoke their tokens, drop them.
	wasDefault := false
	var folded []string
	for _, old := range same {
		if old.Name == kept.Name {
			continue
		}
		d.revoke(cmd, old)
		if cfg.Default == old.Name {
			wasDefault = true
		}
		if _, err := cfg.Remove(old.Name); err != nil {
			return err
		}
		folded = append(folded, old.Name)
	}
	// Bindings to this registry are rebuilt from what the account reads.
	onThisRegistry := func(b remote.Binding) bool {
		return b.Remote == kept.Name || slices.Contains(folded, b.Remote)
	}
	cfg.Bindings = slices.DeleteFunc(cfg.Bindings, onThisRegistry)
	if isNew {
		if err := cfg.Add(kept); err != nil {
			return err
		}
	}
	if wasDefault {
		cfg.Default = kept.Name
	}

	teams, layers := accountBindings(me.Namespaces)
	var bound, skipped []string
	for _, t := range teams {
		if b, ok := cfg.Binding(t.Name); ok && b.Remote != kept.Name {
			skipped = append(skipped, t.Name) // the same name on another registry keeps its binding
			continue
		}
		if err := cfg.Bind(t.Name, kept.Name, remoteRole(t.Role)); err != nil {
			return err
		}
		bound = append(bound, fmt.Sprintf("%s (%s)", t.Name, t.Role))
		for _, anc := range layers[t.Name] {
			if ok, err := cfg.BindAncestor(anc, kept.Name, t.Name); err != nil {
				return err
			} else if !ok {
				d.Log.Warn("parent namespace already comes from another registry; kept it there", "namespace", anc)
			}
		}
	}
	if err := d.Remotes.Save(cfg); err != nil {
		return err
	}
	for _, old := range folded {
		if err := d.Remotes.SetToken(old, ""); err != nil {
			return err
		}
	}
	if err := d.Remotes.SetToken(kept.Name, tok.Token); err != nil {
		return err
	}

	out := cmd.OutOrStdout()
	verb := "using remote"
	if isNew {
		verb = "added remote"
	}
	fmt.Fprintf(out, "signed in to %s as %s (%s %s)\n", regURL, me.Email, verb, kept.Name)
	if len(bound) == 0 {
		fmt.Fprintln(out, "you belong to no team there yet; ask a maintainer to add you, then run loadout login again")
	} else {
		fmt.Fprintf(out, "teams: %s\n", strings.Join(bound, ", "))
	}
	var above []string
	for _, b := range cfg.Bindings {
		if b.Remote == kept.Name && !b.Direct() {
			above = append(above, b.Namespace)
		}
	}
	if len(above) > 0 {
		fmt.Fprintf(out, "also reads %s from there\n", strings.Join(above, ", "))
	}
	for _, n := range skipped {
		d.Log.Warn("team is bound to another registry; kept it there (loadout team leave NAME, then log in again, to move it)", "team", n)
	}
	if len(folded) > 0 {
		fmt.Fprintf(out, "replaced older sign-ins of this registry: %s\n", strings.Join(folded, ", "))
	}
	if len(bound) > 0 {
		fmt.Fprintln(out, "try: loadout list, then loadout install TEAM/NAME")
	}
	if kept.Insecure() {
		d.Log.Warn("this registry uses plain HTTP: the token travels unencrypted; fine on a trusted LAN, use https otherwise", "remote", kept.Name)
	}
	return nil
}

// accountBindings picks what to bind from the namespaces an account can
// open: the lowest ones (those no other one of them sits below) as joined
// teams, and for each, its ancestors nearest first.
func accountBindings(nss []api.MeNamespace) ([]api.MeNamespace, map[string][]string) {
	byName := make(map[string]api.MeNamespace, len(nss))
	hasChild := map[string]bool{}
	for _, n := range nss {
		byName[n.Name] = n
	}
	for _, n := range nss {
		if n.Parent != "" {
			if _, ok := byName[n.Parent]; ok {
				hasChild[n.Parent] = true
			}
		}
	}
	var teams []api.MeNamespace
	layers := map[string][]string{}
	for _, n := range nss {
		if hasChild[n.Name] || n.Via != "" {
			continue
		}
		teams = append(teams, n)
		for p, seen := n.Parent, 0; p != "" && seen < 8; seen++ {
			layers[n.Name] = append(layers[n.Name], p)
			p = byName[p].Parent
		}
	}
	return teams, layers
}

// remoteRole is the binding role for a role name: publish for maintainers
// and above, read for members.
func remoteRole(role string) remote.Role {
	switch role {
	case "maintainer", "owner", "admin":
		return remote.RolePublish
	}
	return remote.RoleRead
}

// revokeSecret revokes a token that was never stored. A failure is a
// warning: the token is dropped on this machine either way.
func (d Deps) revokeSecret(cmd *cobra.Command, r remote.Remote, secret string) {
	c, err := d.newClient(r, secret)
	if err == nil {
		err = c.RevokeToken(cmd.Context())
	}
	if err != nil {
		d.Log.Warn("could not revoke the unwanted token on the registry; it expires on its own", "remote", r.Name, "err", err)
	}
}

// devicePages returns the page to show and the page to open in a browser.
// Both come from the registry, so they are only used when they are on the
// registry's own scheme and host; otherwise the registry's own device page
// is shown and nothing is opened.
func devicePages(registryURL string, start api.DeviceStartResponse) (show, open string) {
	base, err := url.Parse(registryURL)
	if err != nil {
		return registryURL, ""
	}
	fallback := strings.TrimRight(registryURL, "/") + "/auth/device"
	sameSite := func(raw string) bool {
		u, err := url.Parse(raw)
		return err == nil && u.Scheme == base.Scheme && strings.EqualFold(u.Host, base.Host) && u.User == nil
	}
	show, open = start.VerificationURI, start.VerificationURIComplete
	if !sameSite(show) {
		show = fallback
	}
	if !sameSite(open) {
		open = ""
	}
	return show, open
}

// loginRemote picks the registry to sign in to: --remote (a name or a URL),
// else the team's registry, else the default remote.
func (d Deps) loginRemote(team, remoteArg, caFile string) (remote.Remote, error) {
	if strings.Contains(remoteArg, "://") {
		r := remote.Remote{Name: "login", URL: strings.TrimRight(remoteArg, "/"), CAFile: caFile}
		if err := r.Validate(); err != nil {
			return remote.Remote{}, err
		}
		return r, nil
	}
	cfg, err := d.Remotes.Load()
	if err != nil {
		return remote.Remote{}, err
	}
	if remoteArg == "" && len(cfg.Remotes) == 0 {
		return remote.Remote{}, errors.New("which registry? pass --remote https://registry.example.com")
	}
	return cfg.ResolveFor(remoteArg, team, "")
}

// pollDevice polls until the sign-in is approved, denied or expired.
func (d Deps) pollDevice(ctx context.Context, c *client.Client, start api.DeviceStartResponse) (api.DeviceTokenResponse, error) {
	interval := time.Duration(start.Interval) * time.Second
	if interval <= 0 {
		interval = 5 * time.Second
	}
	deadline := time.Now().Add(time.Duration(start.ExpiresIn) * time.Second)
	for {
		if err := d.sleep(ctx, interval); err != nil {
			return api.DeviceTokenResponse{}, err
		}
		resp, err := c.PollDevice(ctx, start.DeviceCode)
		if err != nil {
			return api.DeviceTokenResponse{}, fmt.Errorf("wait for approval: %w", err)
		}
		switch resp.Status {
		case api.DeviceApproved:
			if resp.Token == "" || resp.Namespace == "" {
				return api.DeviceTokenResponse{}, errors.New("the registry approved the sign-in but sent no token")
			}
			return resp, nil
		case api.DeviceDenied:
			return api.DeviceTokenResponse{}, errors.New("the sign-in was denied in the browser")
		case api.DeviceExpired:
			return api.DeviceTokenResponse{}, errors.New("the code expired; run loadout login again")
		case api.DeviceSlowDown:
			interval += 5 * time.Second
		case api.DevicePending:
		default:
			return api.DeviceTokenResponse{}, fmt.Errorf("unexpected sign-in status %q", resp.Status)
		}
		if time.Now().After(deadline) {
			return api.DeviceTokenResponse{}, errors.New("the code expired; run loadout login again")
		}
	}
}

func (d Deps) sleep(ctx context.Context, dur time.Duration) error {
	if d.Sleep != nil {
		return d.Sleep(ctx, dur)
	}
	t := time.NewTimer(dur)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

func newLogoutCmd(d Deps) *cobra.Command {
	var team, remoteName string
	var all bool
	cmd := &cobra.Command{
		Use:   "logout",
		Short: "Revoke the token loadout login gave you and forget the registry",
		Long: `Revoke the registry's token and remove it from this machine, with the
remote and every team bound to it. Without --remote, the registry of your
teams is used when there is only one. With --team, only that team is
left on this machine and the token stays (it covers your other teams).
With --all, the registry also ends every browser session and every loadout
login token of your account, on every machine.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := d.Remotes.Load()
			if err != nil {
				return err
			}
			if team != "" {
				b, ok := cfg.Binding(team)
				if !ok || !b.Direct() {
					return fmt.Errorf("you have not joined %s (see: loadout team list)", team)
				}
				if !all && len(viewsOf(cfg, b.Remote)) > 1 {
					return d.leave(cmd, team) // the token stays for the other teams
				}
				// With --all, the team names the registry to sign out of.
				remoteName = b.Remote
			}
			if remoteName == "" {
				var names []string
				for _, b := range cfg.Bindings {
					if b.Direct() && !slices.Contains(names, b.Remote) {
						names = append(names, b.Remote)
					}
				}
				if len(names) != 1 {
					return fmt.Errorf("you are signed in to %d registries; pass --remote (see: loadout team list)", len(names))
				}
				remoteName = names[0]
			}
			r, ok := cfg.Get(remoteName)
			if !ok {
				return fmt.Errorf("remote %q does not exist", remoteName)
			}
			if all {
				if err := d.signOutEverywhere(cmd, r); err != nil {
					return err
				}
			} else {
				d.revoke(cmd, r)
			}
			for _, ns := range viewsOf(cfg, r.Name) {
				if err := d.leave(cmd, ns); err != nil {
					return err
				}
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&remoteName, "remote", "", "the registry to log out of")
	cmd.Flags().StringVar(&team, "team", "", "leave only this team on this machine")
	cmd.Flags().BoolVar(&all, "all", false, "also sign out every browser and every loadout login token of your account")
	return cmd
}

// signOutEverywhere asks the registry to end every session and login
// token of the user behind the remote's stored token.
func (d Deps) signOutEverywhere(cmd *cobra.Command, r remote.Remote) error {
	tok, err := d.Remotes.StoredToken(r.Name)
	if err != nil {
		return err
	}
	c, err := d.newClient(r, tok)
	if err != nil {
		return err
	}
	n, err := c.SignOutEverywhere(cmd.Context())
	if err != nil {
		return fmt.Errorf("sign out everywhere on %s: %w", r.URL, err)
	}
	fmt.Fprintf(cmd.OutOrStdout(), "signed out everywhere on %s (%d login tokens revoked)\n", r.URL, n)
	return nil
}

// revoke asks the registry to revoke the remote's stored token. A failure
// is a warning: leaving the team still removes the token from this machine.
func (d Deps) revoke(cmd *cobra.Command, r remote.Remote) {
	tok, err := d.Remotes.StoredToken(r.Name)
	if err != nil || tok == "" {
		return
	}
	c, err := d.newClient(r, tok)
	if err != nil {
		d.Log.Warn("could not revoke the token on the registry", "remote", r.Name, "err", err)
		return
	}
	err = c.RevokeToken(cmd.Context())
	apiErr, isAPI := errors.AsType[*client.APIError](err)
	switch {
	case err == nil:
		fmt.Fprintf(cmd.OutOrStdout(), "revoked the token on %s\n", r.URL)
	case isAPI && apiErr.Status == http.StatusUnauthorized:
		// Already revoked, or the membership is gone.
	case isAPI && apiErr.Status == http.StatusForbidden:
		d.Log.Warn("this token was not issued by loadout login, so it stays valid on the registry; its admin can revoke it", "remote", r.Name)
	default:
		d.Log.Warn("could not revoke the token on the registry; it is removed from this machine", "remote", r.Name, "err", err)
	}
}
