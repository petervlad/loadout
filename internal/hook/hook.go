// Package hook sets up and removes loadout's session-start hooks in the
// person's AI tools, so a new session says when their edits hold an update
// back.
//
// It changes files other programs own (~/.claude/settings.json), so it is
// careful: user scope only, through os.Root on the home folder, never
// through a symbolic link, never over a file that does not parse (the
// adapter refuses it), a backup before the first change to a file that
// holds anything of the person's, and a second read just before the write
// so a change made meanwhile is not lost. The adapters decide what the
// bytes are; this package only moves them.
package hook

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/petervlad/loadout/internal/target"
)

// Tool is an AI tool whose user-level session hook loadout manages.
type Tool interface {
	Name() string
	// Product names the tool for people: "Claude Code".
	Product() string
	MarkerDir(kind target.ScopeKind) string
	target.SessionHook
}

// Action is what happened to one tool's hook.
type Action string

// What Setup and Remove do per tool.
const (
	ActionAdded     Action = "added"
	ActionUpdated   Action = "updated"
	ActionUnchanged Action = "unchanged"
	ActionRemoved   Action = "removed"
	// ActionAbsent means there was no hook to remove.
	ActionAbsent Action = "absent"
	// ActionNoTool means the tool's folder does not exist: nothing was
	// written and no folder was made.
	ActionNoTool Action = "no tool"
	// ActionRefused means the file was left as it is; Err says why.
	ActionRefused Action = "refused"
)

// Outcome is the result for one tool.
type Outcome struct {
	Tool    string
	Product string
	// File is the hook file as the person knows it: ~/.claude/settings.json.
	File   string
	Action Action
	// Err explains ActionRefused.
	Err error
	// Snippet is what to put in File by hand when Setup was refused.
	Snippet string
}

// Report is the result of Setup, Remove or Ensure.
type Report struct {
	Tools []Outcome
}

// Changed returns the outcomes that wrote or removed a hook.
func (r Report) Changed() []Outcome {
	return slices.DeleteFunc(slices.Clone(r.Tools), func(o Outcome) bool {
		return o.Action != ActionAdded && o.Action != ActionUpdated && o.Action != ActionRemoved
	})
}

// Of returns the outcomes with action a.
func (r Report) Of(a Action) []Outcome {
	return slices.DeleteFunc(slices.Clone(r.Tools), func(o Outcome) bool { return o.Action != a })
}

// Options configure a Service.
type Options struct {
	// Home is the person's home folder; hooks are user scope only.
	Home string
	// WishPath is the file recording whether the person wants the hooks.
	WishPath string
	// Executable returns the running loadout; LookPath finds a program on
	// the PATH. Tests replace them; nil uses os.Executable and
	// exec.LookPath.
	Executable func() (string, error)
	LookPath   func(file string) (string, error)
	// Windows is set on Windows, where Claude Code runs the PowerShell form.
	Windows bool
	Log     *slog.Logger
}

// Service sets up and removes the hooks.
type Service struct {
	home       string
	tools      []Tool
	wish       wishFile
	executable func() (string, error)
	lookPath   func(string) (string, error)
	windows    bool
	log        *slog.Logger
}

// New returns a service for tools.
func New(opts Options, tools ...Tool) (*Service, error) {
	if opts.Home == "" || opts.WishPath == "" || opts.Executable == nil || opts.LookPath == nil {
		return nil, errors.New("session hooks need a home folder, a wish file and a way to find loadout")
	}
	return &Service{
		home: opts.Home, tools: slices.Clone(tools), wish: wishFile{path: opts.WishPath},
		executable: opts.Executable, lookPath: opts.LookPath, windows: opts.Windows, log: opts.Log,
	}, nil
}

// Wish returns what the person asked for.
func (s *Service) Wish() (Wish, error) { return s.wish.load() }

// Setup writes or updates the hook of every tool whose folder exists, or
// of the tools named, and records that the person wants them.
func (s *Service) Setup(ctx context.Context, names []string) (Report, error) {
	tools, err := s.pick(names)
	if err != nil {
		return Report{}, err
	}
	if err := s.wish.save(Wish{Wanted: true}); err != nil {
		return Report{}, err
	}
	var rep Report
	for _, t := range tools {
		if err := ctx.Err(); err != nil {
			return rep, err
		}
		rep.Tools = append(rep.Tools, s.setup(t))
	}
	return rep, nil
}

// Ensure is Setup for every tool found, unless the person declined the
// hooks: login runs it.
func (s *Service) Ensure(ctx context.Context) (Report, error) {
	w, err := s.wish.load()
	if err != nil || w.Declined {
		return Report{}, err
	}
	return s.Setup(ctx, nil)
}

// Remove takes loadout's hook out of every tool and records that the
// person declined, so login does not set them up again.
func (s *Service) Remove(ctx context.Context) (Report, error) {
	if err := s.wish.save(Wish{Declined: true}); err != nil {
		return Report{}, err
	}
	var rep Report
	for _, t := range s.tools {
		if err := ctx.Err(); err != nil {
			return rep, err
		}
		rep.Tools = append(rep.Tools, s.remove(t))
	}
	return rep, nil
}

// pick returns the tools named, or all of them.
func (s *Service) pick(names []string) ([]Tool, error) {
	if len(names) == 0 {
		return slices.Clone(s.tools), nil
	}
	var out []Tool
	for _, n := range names {
		i := slices.IndexFunc(s.tools, func(t Tool) bool { return t.Name() == n })
		if i < 0 {
			return nil, fmt.Errorf("no AI tool called %q has session hooks; choose from %s", n, strings.Join(s.names(), ", "))
		}
		out = append(out, s.tools[i])
	}
	return out, nil
}

func (s *Service) names() []string {
	out := make([]string, len(s.tools))
	for i, t := range s.tools {
		out[i] = t.Name()
	}
	return out
}

func (s *Service) outcome(t Tool) Outcome {
	return Outcome{Tool: t.Name(), Product: t.Product(), File: "~/" + t.HookFile()}
}

func (s *Service) setup(t Tool) Outcome {
	o := s.outcome(t)
	if !s.hasMarker(t) {
		o.Action = ActionNoTool
		return o
	}
	cmd := s.command(t.Name())
	before, after, err := s.change(t, func(cur []byte) ([]byte, error) { return t.WithHook(cur, cmd) })
	switch {
	case err != nil:
		o.Action, o.Err = ActionRefused, err
		if snippet, serr := t.WithHook(nil, cmd); serr == nil {
			o.Snippet = string(snippet)
		}
		s.log.Warn("left a hook file alone", "file", o.File, "err", err)
	case bytes.Equal(before, after):
		o.Action = ActionUnchanged
	case hasHook(t, before):
		o.Action = ActionUpdated
	default:
		o.Action = ActionAdded
	}
	return o
}

func (s *Service) remove(t Tool) Outcome {
	o := s.outcome(t)
	before, after, err := s.change(t, t.WithoutHook)
	switch {
	case err != nil:
		o.Action, o.Err = ActionRefused, err
		s.log.Warn("left a hook file alone", "file", o.File, "err", err)
	case before == nil || bytes.Equal(before, after):
		o.Action = ActionAbsent
	default:
		o.Action = ActionRemoved
	}
	return o
}

// hasHook reports whether the file data holds loadout's hook.
func hasHook(t Tool, data []byte) bool {
	if data == nil {
		return false
	}
	rest, err := t.WithoutHook(data)
	return err == nil && !bytes.Equal(rest, data)
}

// hasMarker reports whether the tool's folder exists in the home folder.
func (s *Service) hasMarker(t Tool) bool {
	info, err := os.Stat(filepath.Join(s.home, filepath.FromSlash(t.MarkerDir(target.ScopeUser))))
	return err == nil && info.IsDir()
}
