package cli

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/petervlad/loadout/internal/client"
	"github.com/petervlad/loadout/internal/remote"
)

func newRemoteCmd(d Deps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "remote",
		Short: "Manage the registries you publish to and install from",
	}
	cmd.AddCommand(newRemoteAddCmd(d), newRemoteListCmd(d), newRemoteRemoveCmd(d))
	return cmd
}

func newRemoteAddCmd(d Deps) *cobra.Command {
	var tokenStdin, noCheck bool
	var caFile string
	cmd := &cobra.Command{
		Use:   "add NAME URL",
		Short: "Add a registry and check that its token works",
		Example: `  echo "$TOKEN" | loadout remote add office http://10.0.4.12:7420 --token-stdin
  loadout remote add cloud https://registry.example.com --token-stdin < token.txt`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			if caFile != "" {
				caFile = d.abs(caFile) // cron and other folders must find it too
				if _, err := os.Stat(caFile); err != nil {
					return fmt.Errorf("--ca-file: %w", err)
				}
			}
			r := remote.Remote{Name: args[0], URL: strings.TrimRight(args[1], "/"), CAFile: caFile}
			cfg, err := d.Remotes.Load()
			if err != nil {
				return err
			}
			if err := cfg.Add(r); err != nil {
				return err
			}
			token := ""
			if tokenStdin {
				token, err = readSecret(cmd.InOrStdin(), "token")
				if err != nil {
					return err
				}
			}

			out := cmd.OutOrStdout()
			if !noCheck {
				c, err := d.newClient(r, token)
				if err != nil {
					return err
				}
				if err := checkRemote(cmd, c, r, token != "", " (use --no-check to add it anyway)"); err != nil {
					return err
				}
			}

			if err := d.Remotes.Save(cfg); err != nil {
				return err
			}
			if err := d.Remotes.SetToken(r.Name, token); err != nil {
				return err
			}
			fmt.Fprintf(out, "added remote %s", r.Name)
			if cfg.Default == r.Name {
				fmt.Fprint(out, " (default)")
			}
			fmt.Fprintln(out)
			if r.Insecure() {
				d.Log.Warn("this remote uses plain HTTP: the token travels unencrypted; fine on a trusted LAN, use https otherwise", "remote", r.Name)
			}
			return nil
		},
	}
	f := cmd.Flags()
	f.BoolVar(&tokenStdin, "token-stdin", false, "read the access token from standard input")
	f.BoolVar(&noCheck, "no-check", false, "do not contact the registry before saving")
	f.StringVar(&caFile, "ca-file", "", "extra CA bundle (PEM) for TLS-inspecting proxies")
	return cmd
}

// checkRemote checks that the registry behind c answers and, when
// withToken, that it accepts the token. It prints what it reached. reachHint
// is appended to the error for an unreachable registry.
func checkRemote(cmd *cobra.Command, c *client.Client, r remote.Remote, withToken bool, reachHint string) error {
	h, err := c.Health(cmd.Context())
	if err != nil {
		return fmt.Errorf("cannot reach %s%s: %w", r.URL, reachHint, err)
	}
	if withToken {
		if _, err := c.Packages(cmd.Context()); err != nil {
			if apiErr, ok := errors.AsType[*client.APIError](err); ok && (apiErr.Status == http.StatusUnauthorized || apiErr.Status == http.StatusForbidden) {
				return fmt.Errorf("the token was rejected by %s: %w", r.URL, err)
			}
			return fmt.Errorf("could not check the token with %s: %w", r.URL, err)
		}
	}
	out := cmd.OutOrStdout()
	if h.Version != "" {
		fmt.Fprintf(out, "reached %s (registry %s)\n", r.URL, h.Version)
	} else {
		fmt.Fprintf(out, "reached %s\n", r.URL)
	}
	return nil
}

// readSecret reads one line holding a secret (a token or an invite) from
// standard input, so it never appears in shell history or the process list.
func readSecret(r io.Reader, what string) (string, error) {
	line, err := bufio.NewReader(io.LimitReader(r, 64<<10)).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", fmt.Errorf("read %s: %w", what, err)
	}
	secret := strings.TrimSpace(line)
	if secret == "" {
		return "", fmt.Errorf("no %s on standard input", what)
	}
	return secret, nil
}

func newRemoteListCmd(d Deps) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List registries",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := d.Remotes.Load()
			if err != nil {
				return err
			}
			if len(cfg.Remotes) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "no remotes; add one with: loadout remote add NAME URL --token-stdin")
				return nil
			}
			w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "NAME\tURL\tDEFAULT\tTOKEN")
			for _, r := range cfg.Remotes {
				tok, err := d.Remotes.Token(r.Name)
				if err != nil {
					return err
				}
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", r.Name, r.URL, yesNo(r.Name == cfg.Default), yesNo(tok != ""))
			}
			return w.Flush()
		},
	}
}

func newRemoteRemoveCmd(d Deps) *cobra.Command {
	return &cobra.Command{
		Use:   "remove NAME",
		Short: "Remove a registry and its stored token",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := d.Remotes.Load()
			if err != nil {
				return err
			}
			unbound, err := cfg.Remove(args[0])
			if err != nil {
				return err
			}
			if err := d.Remotes.Save(cfg); err != nil {
				return err
			}
			if err := d.Remotes.SetToken(args[0], ""); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "removed remote %s\n", args[0])
			for _, b := range unbound {
				fmt.Fprintf(cmd.OutOrStdout(), "namespace %s no longer has a team registry\n", b.Namespace)
			}
			entries, err := d.Installer.Entries(cmd.Context())
			if err != nil {
				return err
			}
			for _, e := range entries {
				if e.Remote == args[0] {
					d.Log.Warn("installed from the removed remote; sync can no longer update it (add the remote again, or reinstall from another one)", "id", e.ID, "scope", e.Scope.Kind)
				}
			}
			return nil
		},
	}
}

func yesNo(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}
