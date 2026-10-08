package cli

import (
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"github.com/petervlad/loadout/internal/builtin"
	"github.com/petervlad/loadout/internal/hook"
	"github.com/petervlad/loadout/internal/pack"
)

// noToolLine is what login prints when neither tool is on the machine.
const noToolLine = "No Claude Code or Copilot found here (~/.claude, ~/.copilot), so loadout's own skills were not added; the next loadout sync adds them once you install one, or add them now with: loadout setup --skills --target claude"

func newSetupCmd(d Deps) *cobra.Command {
	var (
		skills  bool
		host    bool
		hooks   bool
		noHooks bool
		targets []string
		force   bool
	)
	cmd := &cobra.Command{
		Use:   "setup --skills | --host-skills | --hooks | --no-hooks",
		Short: "Install loadout's own skills, or the session hooks, in your AI tools",
		Long: `--skills installs the skills that come with this loadout (loadout-setup,
loadout-author, loadout-review, loadout-admin) for your user, into every AI
tool found (~/.claude, ~/.copilot), or into the tools given with --target.
loadout login does this for you; setup brings back skills you uninstalled
and adds them for a tool loadout did not find. sync keeps them in step with
the CLI. A skill you edited is never overwritten without --force.

--host-skills installs the two skills that set up a registry of your own
(loadout-host-setup, loadout-host-provision), the same way. They are not
added by login or sync, which only keep installed ones up to date, since
most people never run a registry. Your AI agent follows them with your own
cloud account and tools; loadout calls no cloud and holds no key.

--hooks sets up a session-start hook in Claude Code (~/.claude/settings.json)
and Copilot (~/.copilot/hooks/loadout.json) that runs loadout notices, so a
new session says when your edits hold an update back, or a newer loadout
is out. It only reads files
on this machine. loadout login sets them up too; --no-hooks removes them,
and login then leaves them out.`,
		Example: `  loadout setup --skills
  loadout setup --skills --target claude
  loadout setup --host-skills
  loadout setup --hooks
  loadout setup --no-hooks`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			switch {
			case !skills && !host && !hooks && !noHooks:
				return errors.New("nothing to set up; pass --skills for loadout's own skills, --host-skills for the skills that set up a registry, --hooks for the session hooks, or --no-hooks to remove them")
			case hooks && noHooks:
				return errors.New("--hooks and --no-hooks contradict each other; pass one")
			}
			if sets := askedSets(skills, host); len(sets) > 0 {
				if err := setupSkills(cmd, d, sets, targets, force); err != nil {
					return err
				}
			}
			if hooks || noHooks {
				return setupHooks(cmd, d, targets, hooks)
			}
			return nil
		},
	}
	f := cmd.Flags()
	f.BoolVar(&skills, "skills", false, "install loadout's own skills for your AI agent")
	f.BoolVar(&host, "host-skills", false, "install the skills that set up a loadout registry of your own")
	f.BoolVar(&hooks, "hooks", false, "set up the session hooks that say when your edits hold an update back, or a newer loadout is out")
	f.BoolVar(&noHooks, "no-hooks", false, "remove the session hooks; login then leaves them out")
	f.StringSliceVar(&targets, "target", nil, "AI tools to set up (default: every tool found)")
	f.BoolVar(&force, "force", false, "overwrite a built-in skill you edited")
	return cmd
}

// askedSets names the sets of skills the flags ask for.
func askedSets(skills, host bool) []string {
	var sets []string
	if skills {
		sets = append(sets, builtin.SetEveryday)
	}
	if host {
		sets = append(sets, builtin.SetHost)
	}
	return sets
}

func setupSkills(cmd *cobra.Command, d Deps, sets, targets []string, force bool) error {
	if d.Builtins == nil {
		return errors.New("this loadout was built without its skills")
	}
	rep, err := d.Builtins.SetupSets(cmd.Context(), sets, targets, force)
	if err != nil {
		return err
	}
	if rep.NoTool {
		return errors.New("no Claude Code or Copilot found (~/.claude, ~/.copilot); install one and run loadout sync, or add the skills now with --target claude or --target copilot")
	}
	out := cmd.OutOrStdout()
	printBuiltins(out, rep)
	if slices.Contains(sets, builtin.SetHost) {
		fmt.Fprintln(out, "to run your own registry, ask your AI agent to set up a loadout registry")
	}
	if unchanged := rep.Names(builtin.ActionUnchanged); len(unchanged) == len(rep.Skills) {
		fmt.Fprintf(out, "loadout's skills are up to date (%s): %s\n", strings.Join(rep.Targets, ", "), strings.Join(unchanged, ", "))
	}
	return nil
}

func setupHooks(cmd *cobra.Command, d Deps, targets []string, add bool) error {
	if d.Hooks == nil {
		return errors.New("this loadout was built without session hooks")
	}
	if !add {
		rep, err := d.Hooks.Remove(cmd.Context())
		if err != nil {
			return err
		}
		printHooks(cmd.OutOrStdout(), rep)
		if len(rep.Changed()) == 0 && len(rep.Of(hook.ActionRefused)) == 0 {
			fmt.Fprintln(cmd.OutOrStdout(), "no session hooks to remove; login will not set them up")
		}
		return refusedHooks(rep)
	}
	rep, err := d.Hooks.Setup(cmd.Context(), targets)
	if err != nil {
		return err
	}
	if len(rep.Of(hook.ActionNoTool)) == len(rep.Tools) {
		return errors.New("no Claude Code or Copilot found (~/.claude, ~/.copilot); start one once, then run loadout setup --hooks again")
	}
	printHooks(cmd.OutOrStdout(), rep)
	return refusedHooks(rep)
}

// printHooks says what happened to each tool's hook.
func printHooks(out io.Writer, rep hook.Report) {
	for _, o := range rep.Tools {
		switch o.Action {
		case hook.ActionAdded:
			fmt.Fprintf(out, "set up the session hook in %s (%s): a new session says when your edits hold an update back, or a newer loadout is out\n", o.Product, o.File)
		case hook.ActionUpdated:
			fmt.Fprintf(out, "updated the session hook in %s (%s)\n", o.Product, o.File)
		case hook.ActionUnchanged:
			fmt.Fprintf(out, "the session hook in %s is set up (%s)\n", o.Product, o.File)
		case hook.ActionRemoved:
			fmt.Fprintf(out, "removed the session hook from %s (%s)\n", o.Product, o.File)
		case hook.ActionRefused:
			fmt.Fprintf(out, "left %s alone: %v\n", o.File, o.Err)
			if o.Snippet != "" {
				fmt.Fprintf(out, "To set the hook up by hand, merge this into %s:\n%s", o.File, o.Snippet)
			}
		}
	}
}

// refusedHooks fails the command when a hook file was left alone.
func refusedHooks(rep hook.Report) error {
	if n := len(rep.Of(hook.ActionRefused)); n > 0 {
		return fmt.Errorf("%s left as it was; see above", plural(n, "hook file was", "hook files were"))
	}
	return nil
}

// ensureHooks sets up the session hooks after login, unless the person
// removed them, and says so in one line. It never fails the sign-in.
func (d Deps) ensureHooks(cmd *cobra.Command) {
	if d.Hooks == nil {
		return
	}
	rep, err := d.Hooks.Ensure(cmd.Context())
	if err != nil {
		d.Log.Warn("could not set up the session hooks; try: loadout setup --hooks", "err", err)
		return
	}
	out := cmd.OutOrStdout()
	var files []string
	for _, o := range rep.Tools {
		if o.Action == hook.ActionAdded || o.Action == hook.ActionUpdated {
			files = append(files, o.File)
		}
	}
	if len(files) > 0 {
		fmt.Fprintf(out, "set up session hooks (%s): a new session in your AI tool says when your edits hold an update back, or a newer loadout is out; remove them with: loadout setup --no-hooks\n", strings.Join(files, ", "))
	}
	for _, o := range rep.Of(hook.ActionRefused) {
		fmt.Fprintf(out, "left %s alone (%v); see: loadout setup --hooks\n", o.File, o.Err)
	}
}

// hookTip suggests the session hooks once per sync when an update is held
// back and the person has neither asked for them nor declined them.
func (d Deps) hookTip(out io.Writer) {
	if d.Hooks == nil {
		return
	}
	if w, err := d.Hooks.Wish(); err == nil && !w.Wanted && !w.Declined {
		fmt.Fprintln(out, "\nTip: loadout setup --hooks makes Claude Code and Copilot say this when a session starts.")
	}
}

// printBuiltins reports what changed; nothing when nothing did.
func printBuiltins(out io.Writer, rep builtin.Report) {
	tools := strings.Join(rep.Targets, ", ")
	if names := rep.Names(builtin.ActionInstalled); len(names) > 0 {
		fmt.Fprintf(out, "added loadout's skills for your AI agent (%s): %s\n", tools, strings.Join(names, ", "))
	}
	if names := rep.Names(builtin.ActionUpdated); len(names) > 0 {
		fmt.Fprintf(out, "updated loadout's skills to match this loadout (%s): %s\n", tools, strings.Join(names, ", "))
	}
	for _, o := range rep.Skills {
		switch o.Action {
		case builtin.ActionKept:
			fmt.Fprintf(out, "kept your own %s skill: a file you edited is in the way (loadout setup %s --force replaces it)\n", o.Name, setFlag(o.Set))
		case builtin.ActionOther:
			fmt.Fprintf(out, "left %s/%s alone: it was installed from a registry\n", builtin.Namespace, o.Name)
		}
	}
}

// builtinSet returns the set of id when it is a built-in skill.
func (d Deps) builtinSet(id pack.ID) (builtin.Set, bool) {
	if d.Builtins == nil {
		return builtin.Set{}, false
	}
	return d.Builtins.SetOf(id)
}

// setFlag is the setup flag that installs the skills of a set.
func setFlag(set string) string {
	if set == builtin.SetHost {
		return "--host-skills"
	}
	return "--skills"
}

// ensureBuiltins brings loadout's own skills in step after login and
// sync. It never fails the command: the sign-in or the sync stands.
func (d Deps) ensureBuiltins(cmd *cobra.Command, atLogin bool) {
	if d.Builtins == nil {
		return
	}
	ctx := cmd.Context()
	if atLogin {
		if err := d.Builtins.Want(ctx); err != nil {
			d.Log.Warn("could not add loadout's skills; try: loadout setup --skills", "err", err)
			return
		}
	}
	rep, err := d.Builtins.Ensure(ctx)
	if err != nil {
		d.Log.Warn("could not update loadout's skills; try: loadout setup --skills", "err", err)
		return
	}
	out := cmd.OutOrStdout()
	if rep.NoTool {
		// sync runs from cron; it stays quiet until a tool appears.
		if atLogin {
			fmt.Fprintln(out, noToolLine)
		}
		return
	}
	printBuiltins(out, rep)
}
