package cli

import (
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/petervlad/loadout/internal/api"
	"github.com/petervlad/loadout/internal/registry"
)

// Namespace commands work directly on a registry's database on the machine
// that hosts it, like token commands. create, set and delete also work over
// HTTP with --remote, for owners (teams in their project) and admins.
func newNamespaceCmd(d Deps) *cobra.Command {
	var dataDir, remoteName string
	cmd := &cobra.Command{
		Use:   "namespace",
		Short: "Manage registry namespaces and their layers",
		Long: `A namespace is the first part of a package id. A namespace can have a
parent: tokens of a team read their own namespace and every ancestor, and
publish only to their own. A project above its teams is their shared L0.

On the registry host (a registry database in --data) the commands act as
the operator. Elsewhere, or with --remote, they act on the default remote
(or the one named) as the user signed in there with loadout login: owners
create teams in their project.`,
	}
	f := cmd.PersistentFlags()
	f.StringVar(&dataDir, "data", d.defaultDataDir(), "registry data folder (on the registry host)")
	f.StringVar(&remoteName, "remote", "", "act on this registry over HTTP, as the user you signed in as with loadout login")
	cmd.AddCommand(
		newNamespaceCreateCmd(d, &dataDir, &remoteName),
		newNamespaceListCmd(d, &dataDir, &remoteName),
		newNamespaceSetCmd(d, &dataDir, &remoteName),
		newNamespaceTiersCmd(d, &dataDir, &remoteName),
		newNamespaceDeleteCmd(d, &dataDir, &remoteName),
	)
	return cmd
}

func newNamespaceCreateCmd(d Deps, dataDir, remoteName *string) *cobra.Command {
	var parent, kind string
	cmd := &cobra.Command{
		Use:   "create NAME",
		Short: "Create a namespace, optionally under a parent",
		Example: `  loadout namespace create acme --kind project
  loadout namespace create payments --parent acme
  loadout namespace create mobile --parent acme --remote office   # as an owner of acme`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			var ns api.Namespace
			err := d.withManager(cmd.Context(), *dataDir, *remoteName, func(m manager) error {
				var err error
				ns, err = m.CreateNamespace(cmd.Context(), args[0], parent, kind)
				return err
			})
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if ns.Parent == "" {
				fmt.Fprintf(out, "created %s %s\n", ns.Kind, ns.Name)
				return nil
			}
			fmt.Fprintf(out, "created %s %s under %s; its tokens also read %s packages\n", ns.Kind, ns.Name, ns.Parent, ns.Parent)
			return nil
		},
	}
	f := cmd.Flags()
	f.StringVar(&parent, "parent", "", "parent namespace, whose packages this one reads")
	f.StringVar(&kind, "kind", string(registry.KindTeam), "team or project")
	return cmd
}

func newNamespaceSetCmd(d Deps, dataDir, remoteName *string) *cobra.Command {
	var (
		review, wiki, course string
		models, efforts      []string
	)
	cmd := &cobra.Command{
		Use:   "set NAME [--require-review on|off] [--wiki on|off] [--course TEAM/NAME|none] [--model TOOL:HINT=MODEL] [--effort TOOL:HINT=LEVEL]",
		Short: "Change a namespace's settings (owners and admins)",
		Long: `--require-review on refuses direct publishes to the namespace: every
version goes through loadout propose and a maintainer's approval.

--wiki on gives the namespace a Wiki tab: one page its members write and
everyone in it reads. Off hides the page and keeps its history.

--course TEAM/NAME shows a knowledge base of the namespace, or of a
project above it, as a Course box at the end of the wiki: its Markdown
files at the top level are the lessons, in name order. It follows the
package's latest release. none clears it; a team without a course of its
own shows its project's.

--model and --effort set which model and reasoning effort an AI tool runs
for agents that ask for a hint (fast, balanced or deep). The value is the
tool's own: a Claude Code alias or ID (opus, claude-sonnet-5, opus[1m]),
or a Copilot model as its picker names it (Claude Sonnet 5). Effort is low,
medium, high, xhigh or max. An empty value clears it, so the namespace
inherits the project's again. Teams inherit a project's values one by one.`,
		Example: `  loadout namespace set payments --require-review on
  loadout namespace set lime --wiki on
  loadout namespace set lime --course lime/lime-course
  loadout namespace set acme --model copilot:deep="Claude Sonnet 5" --effort copilot:deep=high
  loadout namespace set payments --model copilot:fast=`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if review == "" && wiki == "" && course == "" && len(models) == 0 && len(efforts) == 0 {
				return errors.New("nothing to change: give --require-review, --wiki, --course, --model or --effort")
			}
			reviewOn, err := onOff("--require-review", review)
			if err != nil {
				return err
			}
			wikiOn, err := onOff("--wiki", wiki)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			return d.withManager(cmd.Context(), *dataDir, *remoteName, func(m manager) error {
				// The course first: it is the setting refused for its
				// value, and a refusal should come before any change.
				if course != "" {
					value := course
					if value == "none" {
						value = ""
					}
					ns, err := m.SetCourse(cmd.Context(), args[0], value)
					if err != nil {
						return err
					}
					printCourse(out, ns, d.siteOf(*dataDir, *remoteName), wiki == "on")
				}
				if len(models) > 0 || len(efforts) > 0 {
					resp, err := d.setTiers(cmd.Context(), m, args[0], models, efforts)
					if err != nil {
						return err
					}
					fmt.Fprintf(out, "model settings of %s changed; agents pick them up on the next install or sync\n\n", args[0])
					if err := d.printTiers(cmd, resp); err != nil {
						return err
					}
				}
				if review != "" {
					ns, err := m.SetRequireReview(cmd.Context(), args[0], reviewOn)
					if err != nil {
						return err
					}
					if ns.RequireReview {
						fmt.Fprintf(out, "%s now requires review: publish is refused, propose instead\n", ns.Name)
					} else {
						fmt.Fprintf(out, "%s no longer requires review\n", ns.Name)
					}
				}
				if wiki != "" {
					ns, err := m.SetWiki(cmd.Context(), args[0], wikiOn)
					if err != nil {
						return err
					}
					printWikiSwitch(out, ns, d.siteOf(*dataDir, *remoteName))
				}
				return nil
			})
		},
	}
	f := cmd.Flags()
	f.StringVar(&review, "require-review", "", "on or off")
	f.StringVar(&wiki, "wiki", "", "on or off")
	f.StringVar(&course, "course", "", "a knowledge base as TEAM/NAME the wiki shows as its course, or none")
	f.StringArrayVar(&models, "model", nil, "TOOL:HINT=MODEL, for example copilot:deep=\"Claude Sonnet 5\"; empty MODEL clears it (repeatable)")
	f.StringArrayVar(&efforts, "effort", nil, "TOOL:HINT=LEVEL, for example copilot:deep=high; empty LEVEL clears it (repeatable)")
	return cmd
}

// onOff reads a switch flag; an empty value is false and means unset.
func onOff(flag, value string) (bool, error) {
	switch value {
	case "on":
		return true, nil
	case "off", "":
		return false, nil
	}
	return false, fmt.Errorf("%s %q: want on or off", flag, value)
}

// printWikiSwitch says where the wiki is now; site is the registry's
// address, empty on the registry host, which does not know its own.
func printWikiSwitch(out io.Writer, ns api.Namespace, site string) {
	if !ns.Wiki {
		fmt.Fprintf(out, "%s's wiki is off; its history is kept\n", ns.Name)
		return
	}
	if site == "" {
		fmt.Fprintf(out, "%s now has a wiki: open its Wiki tab, or loadout wiki pull --team %s\n", ns.Name, ns.Name)
		return
	}
	fmt.Fprintf(out, "%s now has a wiki: open it at %s/t/%s/wiki, or loadout wiki pull --team %s\n", ns.Name, strings.TrimRight(site, "/"), ns.Name, ns.Name)
}

// printCourse says what the wiki's Course box shows now; site is the
// registry's address, empty on the registry host. wikiOn is set when the
// same command turns the wiki on.
func printCourse(out io.Writer, ns api.Namespace, site string, wikiOn bool) {
	if ns.Course == "" {
		if ns.CourseAbove != "" {
			fmt.Fprintf(out, "%s has no course of its own now; its wiki shows %s, from %s\n", ns.Name, ns.CourseAbove, ns.CourseFrom)
			return
		}
		fmt.Fprintf(out, "%s has no course of its own now\n", ns.Name)
		return
	}
	if site == "" {
		fmt.Fprintf(out, "%s's wiki now shows the course %s\n", ns.Name, ns.Course)
	} else {
		fmt.Fprintf(out, "%s's wiki now shows the course %s: open it at %s/t/%s/wiki#course\n", ns.Name, ns.Course, strings.TrimRight(site, "/"), ns.Name)
	}
	if !ns.Wiki && !wikiOn {
		fmt.Fprintf(out, "It shows once the wiki is on: loadout namespace set %s --wiki on\n", ns.Name)
	}
}

func newNamespaceDeleteCmd(d Deps, dataDir, remoteName *string) *cobra.Command {
	return &cobra.Command{
		Use:   "delete NAME",
		Short: "Delete an empty namespace and its wiki (admins)",
		Long:  "Delete an empty namespace (admins). Its wiki and the wiki's history go with it.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			err := d.withManager(cmd.Context(), *dataDir, *remoteName, func(m manager) error {
				return m.DeleteNamespace(cmd.Context(), args[0])
			})
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "deleted %s and its wiki, if it had one\n", args[0])
			return nil
		},
	}
}

func newNamespaceListCmd(d Deps, dataDir, remoteName *string) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List namespaces (on a remote: the ones your token reads)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if remote := d.manageRemote(*dataDir, *remoteName); remote != "" {
				return d.listRemoteNamespaces(cmd, remote)
			}
			var nss []registry.Namespace
			err := withRegistry(cmd.Context(), d, *dataDir, func(reg Registry) error {
				var err error
				nss, err = reg.Service.Namespaces(cmd.Context())
				return err
			})
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if len(nss) == 0 {
				fmt.Fprintln(out, "no namespaces yet; create one with: loadout namespace create NAME")
				return nil
			}
			w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "NAME\tPARENT\tKIND\tCREATED")
			for _, ns := range nss {
				parent := ns.Parent
				if parent == "" {
					parent = "-"
				}
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", ns.Name, parent, ns.Kind, ns.CreatedAt.Local().Format("2006-01-02 15:04"))
			}
			return w.Flush()
		},
	}
}

// listRemoteNamespaces lists every namespace the signed-in account can
// open, with its role there. A token from before account tokens (or a
// registry without /v1/me) lists what the token reads instead.
func (d Deps) listRemoteNamespaces(cmd *cobra.Command, remote string) error {
	c, r, err := d.connect(remote)
	if err != nil {
		return err
	}
	out := cmd.OutOrStdout()
	w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	if me, err := c.Me(cmd.Context()); err == nil {
		fmt.Fprintln(w, "NAME\tKIND\tLAYER\tPARENT\tROLE")
		for _, n := range me.Namespaces {
			layer := "L1"
			if n.Parent == "" {
				layer = "L0"
			}
			role := n.Role
			if n.Via != "" {
				role += " (through " + n.Via + ")"
			}
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", n.Name, n.Kind, layer, dashIfEmpty(n.Parent), role)
		}
		if err := w.Flush(); err != nil {
			return err
		}
		fmt.Fprintf(out, "\nWhere %s belongs on %s (%s).\n", me.Email, r.Name, r.URL)
		return nil
	}
	layers, err := c.Layers(cmd.Context())
	if err != nil {
		return err
	}
	fmt.Fprintln(w, "NAME\tKIND\tLAYER")
	for _, l := range slices.Backward(layers) {
		layer := "L1"
		if l.Kind == api.LayerKind(registry.KindProject) {
			layer = "L0"
		}
		fmt.Fprintf(w, "%s\t%s\t%s\n", l.Namespace, l.Kind, layer)
	}
	if err := w.Flush(); err != nil {
		return err
	}
	fmt.Fprintf(out, "\nThese are what your token for %s reads. Every project and team you belong to: %s\n", r.Name, r.URL)
	return nil
}
