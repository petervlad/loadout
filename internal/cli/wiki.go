package cli

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"text/tabwriter"
	"unicode"

	"github.com/spf13/cobra"

	"github.com/petervlad/loadout/internal/api"
	"github.com/petervlad/loadout/internal/client"
	"github.com/petervlad/loadout/internal/registry"
	"github.com/petervlad/loadout/internal/remote"
	"github.com/petervlad/loadout/internal/wiki"
)

// wikiPlace is the page the wiki commands work on: --team and --remote.
type wikiPlace struct {
	team, remote string
}

func newWikiCmd(d Deps) *cobra.Command {
	var p wikiPlace
	cmd := &cobra.Command{
		Use:   "wiki",
		Short: "Read and write a project's or team's wiki page",
		Long: `A project or team whose owner turned the wiki on (in Settings, or with
loadout namespace set NS --wiki on) has one wiki page, wiki.md or
wiki.mdx. Pull it into a file, edit it in any editor and push it back.

pull never replaces a file with changes that are not on the registry, and
push is refused when someone saved after the revision you pulled: pull
theirs into another file, merge, and push with --base.

The wiki needs the sign-in of loadout login: it names who saved what.`,
		Example: `  loadout wiki pull --team lime
  loadout wiki push wiki.md --team lime --note "Add the release checklist"
  loadout wiki log --team lime`,
	}
	f := cmd.PersistentFlags()
	f.StringVar(&p.team, "team", "", "the project or team whose wiki it is (required)")
	f.StringVar(&p.remote, "remote", "", "registry (default: the team's registry)")
	_ = cmd.MarkPersistentFlagRequired("team")
	cmd.AddCommand(
		newWikiShowCmd(d, &p),
		newWikiPullCmd(d, &p),
		newWikiPushCmd(d, &p),
		newWikiLogCmd(d, &p),
	)
	return cmd
}

func newWikiShowCmd(d Deps, p *wikiPlace) *cobra.Command {
	var rev int
	cmd := &cobra.Command{
		Use:   "show",
		Short: "Print the wiki page",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := notNegative("--revision", rev); err != nil {
				return err
			}
			r, _, err := d.wikiRemote(p)
			if err != nil {
				return err
			}
			page, err := r.Page(cmd.Context(), p.team, rev)
			if err != nil {
				return err
			}
			if page.Seq == 0 {
				fmt.Fprintf(cmd.ErrOrStderr(), "%s has no wiki page yet; write one and run loadout wiki push wiki.md --team %s\n", p.team, p.team)
				return nil
			}
			_, err = fmt.Fprint(cmd.OutOrStdout(), page.Doc.Text())
			return err
		},
	}
	cmd.Flags().IntVar(&rev, "revision", 0, "an older revision (default: the newest)")
	return cmd
}

func newWikiPullCmd(d Deps, p *wikiPlace) *cobra.Command {
	var (
		rev   int
		force bool
	)
	cmd := &cobra.Command{
		Use:   "pull [FILE]",
		Short: "Write the wiki page to a file",
		Long: `Write the page to FILE (default wiki.md, or wiki.mdx for an MDX page)
and remember its revision for push. A file with changes that are not on
the registry is never replaced without --force.`,
		Example: `  loadout wiki pull --team lime
  loadout wiki pull theirs.md --team lime`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := notNegative("--revision", rev); err != nil {
				return err
			}
			if d.Wiki == nil {
				return errNoWiki
			}
			r, ref, err := d.wikiRemote(p)
			if err != nil {
				return err
			}
			f := wiki.File{Dir: d.WorkDir}
			if len(args) == 1 {
				f.Name = args[0]
			}
			page, f, err := d.Wiki.Pull(cmd.Context(), r, ref, f, rev, force)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if page.Seq == 0 {
				fmt.Fprintf(out, "%s has no wiki page yet; wrote an empty %s. Write it, then run loadout wiki push %s --team %s\n", p.team, f.Name, f.Name, p.team)
				return nil
			}
			fmt.Fprintf(out, "wrote revision %d of %s's wiki (%s, %s) to %s\n", page.Seq, p.team, page.Author, page.CreatedAt.Local().Format("2006-01-02 15:04"), f.Name)
			return nil
		},
	}
	f := cmd.Flags()
	f.IntVar(&rev, "revision", 0, "an older revision (default: the newest)")
	f.BoolVar(&force, "force", false, "replace FILE even if it has changes that are not on the registry")
	return cmd
}

func newWikiPushCmd(d Deps, p *wikiPlace) *cobra.Command {
	var (
		note string
		base int
	)
	cmd := &cobra.Command{
		Use:   "push FILE",
		Short: "Save a file as the next revision of the wiki page",
		Long: `Save FILE as the revision after the one it was pulled at. Its name sets
the format: .md or .markdown is Markdown, .mdx is MDX. If someone saved
in between, the push is refused and FILE is left as it is: pull their
revision into another file, merge it into yours, and push with --base
set to their revision. A file never pulled starts from an empty page.`,
		Example: `  loadout wiki push wiki.md --team lime --note "Add the release checklist"
  loadout wiki push wiki.md --team lime --base 14`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := notNegative("--base", base); err != nil {
				return err
			}
			if d.Wiki == nil {
				return errNoWiki
			}
			r, ref, err := d.wikiRemote(p)
			if err != nil {
				return err
			}
			var from *int
			if cmd.Flags().Changed("base") {
				from = &base
			}
			f := wiki.File{Dir: d.WorkDir, Name: args[0]}
			info, saved, err := d.Wiki.Push(cmd.Context(), r, ref, f, from, note)
			if stale, ok := errors.AsType[*wiki.StaleError](err); ok {
				return staleHint(stale, p, args[0])
			}
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			switch {
			case saved:
				fmt.Fprintf(out, "saved %s as revision %d of %s's wiki\n", args[0], info.Seq, p.team)
			case info.Seq == 0:
				fmt.Fprintln(out, "Nothing changed: the page is still empty.")
			default:
				fmt.Fprintf(out, "Nothing changed: revision %d is the same text.\n", info.Seq)
			}
			return nil
		},
	}
	f := cmd.Flags()
	f.StringVar(&note, "note", "", "what changed, one line for the history")
	f.IntVar(&base, "base", 0, "the revision your text is based on (default: the one FILE was pulled at)")
	return cmd
}

// notNegative refuses a negative revision number, which the registry
// would otherwise read as the newest revision or an empty page.
func notNegative(flag string, n int) error {
	if n < 0 {
		return fmt.Errorf("%s %d: want 0 or more", flag, n)
	}
	return nil
}

// staleHint says how to merge after a push someone else got in before.
func staleHint(e *wiki.StaleError, p *wikiPlace, file string) error {
	flags := "--team " + p.team
	if p.remote != "" {
		flags += " --remote " + p.remote
	}
	theirs := "theirs" + filepath.Ext(file)
	return fmt.Errorf("%w. Run loadout wiki pull %s %s, merge it into %s, then loadout wiki push %s %s --base %d. %s is left as it is",
		e, theirs, flags, file, file, flags, e.Newest.Seq, file)
}

func newWikiLogCmd(d Deps, p *wikiPlace) *cobra.Command {
	return &cobra.Command{
		Use:   "log",
		Short: "List the wiki page's revisions, newest first",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			r, _, err := d.wikiRemote(p)
			if err != nil {
				return err
			}
			revs, err := r.History(cmd.Context(), p.team)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if len(revs) == 0 {
				fmt.Fprintf(out, "%s has no wiki page yet\n", p.team)
				return nil
			}
			w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "REV\tWHEN\tBY\tSIZE\tNOTE")
			for _, in := range revs {
				fmt.Fprintf(w, "%d\t%s\t%s\t%d\t%s\n", in.Seq, in.CreatedAt.Local().Format("2006-01-02 15:04"), oneLine(in.Author), in.Size, oneLine(in.Note))
			}
			return w.Flush()
		},
	}
}

// oneLine drops control characters from text the registry sent, so a note
// cannot move the cursor or recolour the terminal.
func oneLine(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, s)
}

var errNoWiki = errors.New("this loadout was built without wiki support")

// wikiRemote connects to the registry of p.team.
func (d Deps) wikiRemote(p *wikiPlace) (wikiAdapter, wiki.Ref, error) {
	c, r, err := d.connectFor(p.remote, p.team, "")
	if err != nil {
		return wikiAdapter{}, wiki.Ref{}, err
	}
	return wikiAdapter{c: c, r: r}, wiki.Ref{Remote: r.URL, Namespace: p.team}, nil
}

// wikiAdapter lets the wiki commands and wiki.Syncer use a client.
type wikiAdapter struct {
	c *client.Client
	r remote.Remote
}

var _ wiki.Remote = wikiAdapter{}

func (a wikiAdapter) Page(ctx context.Context, ns string, rev int) (wiki.Revision, error) {
	p, err := a.c.Wiki(ctx, ns, rev)
	if err != nil {
		return wiki.Revision{}, a.explain(ns, err)
	}
	doc, err := wiki.NewDoc(pageFormat(p.Format), p.Text)
	if err != nil {
		return wiki.Revision{}, fmt.Errorf("the wiki of %s: %w", ns, err)
	}
	info := wiki.Info{Namespace: ns, Seq: p.Revision, Format: doc.Format(), Size: doc.Size(), Author: p.Author, Note: p.Note, CreatedAt: p.CreatedAt}
	return wiki.Revision{Info: info, Doc: doc}, nil
}

func (a wikiAdapter) History(ctx context.Context, ns string) ([]wiki.Info, error) {
	revs, err := a.c.WikiRevisions(ctx, ns)
	if err != nil {
		return nil, a.explain(ns, err)
	}
	out := make([]wiki.Info, 0, len(revs))
	for _, r := range revs {
		out = append(out, wikiInfo(ns, r))
	}
	return out, nil
}

func (a wikiAdapter) Save(ctx context.Context, ns string, base int, doc wiki.Doc, note string) (wiki.Info, bool, error) {
	p, err := a.c.SaveWiki(ctx, ns, api.WikiSave{Base: base, Format: string(doc.Format()), Text: doc.Text(), Note: note})
	if apiErr, ok := errors.AsType[*client.APIError](err); ok && errors.Is(err, api.ErrStale) && apiErr.Newest != nil {
		return wiki.Info{}, false, &wiki.StaleError{Base: base, Newest: wikiInfo(ns, *apiErr.Newest)}
	}
	if err != nil {
		return wiki.Info{}, false, a.explain(ns, err)
	}
	info := wiki.Info{Namespace: ns, Seq: p.Revision, Format: doc.Format(), Size: doc.Size(), Author: p.Author, Note: p.Note, CreatedAt: p.CreatedAt}
	return info, p.Saved, nil
}

// explain turns the registry's refusals into what to do next.
func (a wikiAdapter) explain(ns string, err error) error {
	apiErr, ok := errors.AsType[*client.APIError](err)
	switch {
	case !ok:
		return err
	case apiErr.Status == http.StatusNotFound && apiErr.Code == "":
		// No API body: the route does not exist on this registry.
		return fmt.Errorf("%s has no wiki yet; it needs a newer loadout on the registry", a.r.URL)
	case apiErr.Status == http.StatusUnauthorized || apiErr.Status == http.StatusForbidden:
		return fmt.Errorf("%s. If you have a role in %s, sign in again with loadout login --remote %s: the wiki needs that sign-in, not a machine token or an older one", sentence(apiErr.Message), ns, a.r.Name)
	}
	// The registry's sentences already name the namespace.
	return errors.New(sentence(apiErr.Message))
}

// sentence is the registry's message without the name of the sentinel it
// starts with, which only repeats the status.
func sentence(msg string) string {
	for _, e := range []error{registry.ErrNotFound, registry.ErrInvalid, registry.ErrForbidden} {
		if rest, ok := strings.CutPrefix(msg, e.Error()+": "); ok {
			return rest
		}
	}
	return msg
}

func wikiInfo(ns string, r api.WikiRevision) wiki.Info {
	return wiki.Info{Namespace: ns, Seq: r.Revision, Format: pageFormat(r.Format), Size: r.Size, Author: r.Author, Note: r.Note, CreatedAt: r.CreatedAt}
}

// pageFormat reads a format the registry sent; an empty one is Markdown.
func pageFormat(s string) wiki.Format {
	if s == "" {
		return wiki.Markdown
	}
	return wiki.Format(s)
}
