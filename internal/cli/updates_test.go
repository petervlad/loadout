package cli

import (
	"bytes"
	"context"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"

	"github.com/petervlad/loadout/internal/buildinfo"
	"github.com/petervlad/loadout/internal/client"
	"github.com/petervlad/loadout/internal/cliupdate"
)

func TestQuietCommands(t *testing.T) {
	root := commandTree()
	tests := []struct {
		args  []string
		quiet bool
	}{
		{[]string{"sync"}, false},
		{[]string{"list"}, false},
		{[]string{"install", "lime/planner"}, false},
		{[]string{"token", "create", "--team", "lime", "--remote", "office"}, false},
		{[]string{"status"}, true},
		{[]string{"version"}, true},
		{[]string{"notices", "--hook", "claude"}, true},
		{[]string{"serve"}, true},
		{[]string{"token", "create", "--team", "lime", "--data", "/srv"}, true},
		{[]string{"sync", "--help"}, true},
		{[]string{"help", "sync"}, true},
		{[]string{"completion", "zsh"}, true},
		{[]string{}, true},
	}
	root.InitDefaultHelpCmd()
	root.InitDefaultCompletionCmd()
	for _, tc := range tests {
		cmd, rest, err := root.Find(tc.args)
		if err != nil {
			t.Fatalf("%v: %v", tc.args, err)
		}
		cmd.InitDefaultHelpFlag() // as Execute does
		if err := cmd.ParseFlags(rest); err != nil {
			t.Fatalf("%v: %v", tc.args, err)
		}
		if got := isQuiet(cmd); got != tc.quiet {
			t.Errorf("isQuiet(%v) = %v, want %v", tc.args, got, tc.quiet)
		}
	}
}

func TestAfterRun(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cliupdate.yaml")
	newDeps := func() Deps {
		b := buildinfo.Parse("0.4.0")
		u := cliupdate.New(cliupdate.Options{Store: cliupdate.NewStore(path), Build: b, Method: buildinfo.MethodOf(buildinfo.Install{Build: b})})
		return Deps{Log: slog.New(slog.DiscardHandler), Updates: u}
	}
	root := commandTree()
	sync, _, _ := root.Find([]string{"sync"})
	status, _, _ := root.Find([]string{"status"})

	// A quiet command records nothing and says nothing.
	d := newDeps()
	d.Updates.Saw("https://registry.example.com", client.CLIAdvice{Latest: "0.5.0"})
	var out bytes.Buffer
	AfterRun(t.Context(), d, status, &out, cliupdate.Person)
	if out.Len() != 0 {
		t.Errorf("after status: %q", out.String())
	}
	// Ctrl-C: nothing either.
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	AfterRun(ctx, d, sync, &out, cliupdate.Person)
	if out.Len() != 0 {
		t.Errorf("after Ctrl-C: %q", out.String())
	}
	// Any other command says it, once.
	AfterRun(t.Context(), d, sync, &out, cliupdate.Person)
	if !strings.HasPrefix(out.String(), "loadout 0.5.0 is out (you have 0.4.0).") {
		t.Errorf("after sync: %q", out.String())
	}
	out.Reset()
	AfterRun(t.Context(), newDeps(), sync, &out, cliupdate.Person)
	if out.Len() != 0 {
		t.Errorf("a second sync the same day: %q", out.String())
	}
	// Without the service there is nothing to do.
	AfterRun(t.Context(), Deps{}, sync, &out, cliupdate.Person)
}

// pushes records what a Pusher was asked to send.
type pushes []string

func (p *pushes) Push(_ context.Context, title, _ string) error {
	*p = append(*p, title)
	return nil
}

func TestAfterSyncPushes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cliupdate.yaml")
	b := buildinfo.Parse("0.4.0")
	p := &pushes{}
	d := Deps{
		Log:     slog.New(slog.DiscardHandler),
		Updates: cliupdate.New(cliupdate.Options{Store: cliupdate.NewStore(path), Build: b, Method: buildinfo.MethodOf(buildinfo.Install{Build: b})}),
		Pusher:  p,
	}
	d.Updates.Saw("https://registry.example.com", client.CLIAdvice{Latest: "0.5.0"})
	root := commandTree()
	list, _, _ := root.Find([]string{"list"})
	sync, _, _ := root.Find([]string{"sync"})
	var out bytes.Buffer
	AfterRun(t.Context(), d, list, &out, cliupdate.Script)
	if len(*p) != 0 {
		t.Errorf("list pushed %q", *p)
	}
	AfterRun(t.Context(), d, sync, &out, cliupdate.Script)
	AfterRun(t.Context(), d, sync, &out, cliupdate.Script)
	if len(*p) != 1 || (*p)[0] != "loadout 0.5.0 is out" {
		t.Errorf("two syncs pushed %q, want one push", *p)
	}
}
