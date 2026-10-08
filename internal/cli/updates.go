package cli

import (
	"context"
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/petervlad/loadout/internal/cliupdate"
)

// quietUpdates marks a command after which AfterRun says nothing: it
// shows the news in its own output (status, version), prints only what a
// tool reads (notices --hook), or runs on the registry host (serve).
const quietUpdates = "loadout/quiet-updates"

// pushUpdates marks the command after which AfterRun also tells the push
// notifier: sync, which runs from cron.
const pushUpdates = "loadout/push-updates"

// quiet marks cmd with quietUpdates.
func quiet(cmd *cobra.Command) *cobra.Command {
	if cmd.Annotations == nil {
		cmd.Annotations = map[string]string{}
	}
	cmd.Annotations[quietUpdates] = "true"
	return cmd
}

// AfterRun runs after every command, an error or not: it keeps what the
// registries said about loadout releases and, when it is due, writes one
// line about a newer loadout to w (stderr), for a. It never changes the
// exit code; a failure is logged at debug. main calls it with the command
// that ran, after printing that command's error.
func AfterRun(ctx context.Context, d Deps, cmd *cobra.Command, w io.Writer, a cliupdate.Audience) {
	if d.Updates == nil || ctx.Err() != nil || isQuiet(cmd) {
		return
	}
	line, err := d.Updates.Finish(ctx, a)
	if err != nil {
		d.Log.Debug("cli update state not saved", "err", err)
	}
	if line != "" {
		fmt.Fprintln(w, line)
	}
	if d.Pusher != nil && cmd.Annotations[pushUpdates] != "" {
		if err := d.Updates.Push(ctx, d.Pusher); err != nil {
			d.Log.Warn("could not push that a newer loadout is out; next sync tries again", "err", err)
		}
	}
}

// isQuiet reports whether nothing should follow cmd: a quiet command or
// one below it, help, shell completion, and the registry host's own
// commands (--data).
func isQuiet(cmd *cobra.Command) bool {
	if cmd == nil || !cmd.Runnable() {
		return true
	}
	for _, flag := range []string{"help", "data"} {
		if f := cmd.Flags().Lookup(flag); f != nil && f.Changed {
			return true
		}
	}
	for c := cmd; c != nil; c = c.Parent() {
		if c.Annotations[quietUpdates] != "" {
			return true
		}
		switch c.Name() {
		case "help", "completion", cobra.ShellCompRequestCmd, cobra.ShellCompNoDescRequestCmd:
			return true
		}
	}
	return false
}

// writeUpdate prints the CLI update sentence, from what registries said
// last, for status and notices; nothing when there is no news.
func writeUpdate(w io.Writer, d Deps, sentence func(cliupdate.Status) string) {
	if d.Updates == nil {
		return
	}
	if s := sentence(d.Updates.Status()); s != "" {
		fmt.Fprintln(w, s)
	}
}
