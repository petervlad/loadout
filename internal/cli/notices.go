package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/petervlad/loadout/internal/cliupdate"
	"github.com/petervlad/loadout/internal/notice"
	"github.com/petervlad/loadout/internal/target"
)

// Limits of a session hook: a tool gives it seconds, and what it reads
// comes from the tool.
const (
	hookDeadline = time.Second
	hookInputMax = 64 << 10
)

func newNoticesCmd(d Deps) *cobra.Command {
	var hook string
	cmd := &cobra.Command{
		Use:   "notices [--hook claude|copilot]",
		Short: "Print updates your edits hold back, merge conflicts left to resolve and a newer loadout, without a network call",
		Long: `Print one line per installed package whose update your edits hold back,
as the last loadout sync found them and as your files are now, and one
per package whose install --merge left conflicts to resolve. When a
registry said that a newer loadout is out, the last line says so. It
reads only files on this machine and prints nothing when there is
nothing to say, so it suits a shell prompt.

With --hook it is the session-start hook of Claude Code or Copilot: it
reads the tool's hook input, speaks only when a session starts in a folder
an install applies to, prints the tool's JSON, and on any problem prints
nothing and exits 0. loadout login and loadout setup --hooks set it up.`,
		Example: `  loadout notices
  loadout notices --hook claude`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if d.Notices == nil {
				return errors.New("this loadout was built without notices")
			}
			if hook != "" {
				ctx, cancel := context.WithTimeout(cmd.Context(), hookDeadline)
				defer cancel()
				if err := d.sessionHook(ctx, cmd, hook); err != nil {
					d.Log.Debug("session hook said nothing", "tool", hook, "err", err)
				}
				return nil
			}
			ns, err := d.Notices.List(cmd.Context(), notice.Filter{Kinds: []notice.Kind{notice.Held, notice.Unresolved}})
			if err != nil {
				return err
			}
			for _, n := range ns {
				fmt.Fprintf(cmd.OutOrStdout(), "%s: %s\n", installName(n), n.Sentence())
			}
			writeUpdate(cmd.OutOrStdout(), d, func(s cliupdate.Status) string { return s.Line(cliupdate.Person) })
			return nil
		},
	}
	cmd.Flags().StringVar(&hook, "hook", "", "answer as the session-start hook of this AI tool: claude or copilot")
	return quiet(cmd)
}

// hookInput is the part of a tool's hook input loadout reads.
type hookInput struct {
	Cwd    string `json:"cwd"`
	Source string `json:"source"`
}

// sessionHook prints what the AI tool name shows when a session starts.
// Resumed, cleared, compacted and forked sessions hear nothing again
// (VS Code ignores the matcher that would say so).
func (d Deps) sessionHook(ctx context.Context, cmd *cobra.Command, name string) error {
	r, ok := d.Installer.Renderer(name)
	if !ok {
		return fmt.Errorf("no AI tool called %q", name)
	}
	sh, ok := r.(target.SessionHook)
	if !ok {
		return fmt.Errorf("%s has no session hooks", name)
	}
	in, err := readHookInput(ctx, cmd.InOrStdin())
	if err != nil {
		return err
	}
	if !slices.Contains([]string{"", "startup", "new"}, in.Source) {
		return nil
	}
	dir := in.Cwd
	if dir == "" {
		dir = d.WorkDir
	}
	ns, err := d.Notices.List(ctx, notice.Filter{Kinds: []notice.Kind{notice.Held, notice.Unresolved}, Dir: dir})
	if err != nil {
		return err
	}
	msg := notice.Message(ns)
	if d.Updates != nil {
		// A failure to record the agent's clock still says the news.
		cli, err := d.Updates.SessionMessage(ctx)
		if err != nil {
			d.Log.Debug("cli update clock not saved", "err", err)
		}
		msg = msg.Join(cli)
	}
	out, err := sh.SessionOutput(msg)
	if err != nil || out == nil {
		return err
	}
	_, err = cmd.OutOrStdout().Write(out)
	return err
}

// readHookInput reads at most hookInputMax bytes of JSON. A tool that never
// closes standard input must not hang a session start, so the read gives
// up with ctx; its goroutine ends with the process.
func readHookInput(ctx context.Context, r io.Reader) (hookInput, error) {
	type read struct {
		data []byte
		err  error
	}
	done := make(chan read, 1)
	go func() {
		data, err := io.ReadAll(io.LimitReader(r, hookInputMax))
		done <- read{data, err}
	}()
	select {
	case <-ctx.Done():
		return hookInput{}, fmt.Errorf("read hook input: %w", ctx.Err())
	case got := <-done:
		if got.err != nil {
			return hookInput{}, fmt.Errorf("read hook input: %w", got.err)
		}
		var in hookInput
		if len(got.data) == 0 {
			return in, nil
		}
		if err := json.Unmarshal(got.data, &in); err != nil {
			return hookInput{}, fmt.Errorf("read hook input: %w", err)
		}
		return in, nil
	}
}

// writeNotices prints the status footer: what the last sync found that is
// still true, offline. Nothing when nothing is behind.
func writeNotices(out io.Writer, ns []notice.Notice) error {
	if len(ns) == 0 {
		return nil
	}
	var last time.Time
	for _, n := range ns {
		if n.Checked.After(last) {
			last = n.Checked
		}
	}
	fmt.Fprintf(out, "\nAs of the last sync (%s):\n", last.Local().Format("2 Jan 15:04"))
	w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	for _, n := range ns {
		fmt.Fprintf(w, "  %s\t%s\n", installName(n), n.Sentence())
	}
	return w.Flush()
}

// installName names a notice's install: the package, and the project for
// a project install.
func installName(n notice.Notice) string {
	if n.Scope.Kind == target.ScopeProject {
		return fmt.Sprintf("%s (project %s)", n.ID, n.Scope.Root)
	}
	return n.ID.String()
}
