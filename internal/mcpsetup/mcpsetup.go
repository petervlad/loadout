// Package mcpsetup adds and removes loadout's team memory MCP servers in
// the person's AI tools, so an agent can reach the memory of every team
// that has it switched on.
//
// It changes configuration other programs own, so it is careful in the
// way internal/hook is: user scope only, through os.Root on the home
// folder, never through a symbolic link, never over a file that does not
// parse (the adapter refuses it), a backup before the first change to a
// file that holds anything of the person's, and a second read just before
// the write so a change made meanwhile is not lost. A tool that rewrites
// its own file all the time (Claude Code) is not edited at all: its own
// command line adds and removes the entries. The adapters decide what the
// bytes and the arguments are, this package moves them.
package mcpsetup

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/petervlad/loadout/internal/target"
)

// Space is a namespace whose team memory is on, as the registry lists it.
type Space struct {
	Namespace string
	// Endpoint is the memory endpoint of the namespace. The bridge finds
	// the remote itself, so it names the space for the token.
	Endpoint string
	// Remote names the registry that listed the space, where its token is
	// made and kept.
	Remote string
	// Several is true when the person has more than one registry. The entry
	// then names Remote with --remote, so the bridge never has to guess which
	// registry a memory token belongs to.
	Several bool
}

// args are what the tool runs for the space.
func (sp Space) args() []string {
	a := []string{"mcp", "--team", sp.Namespace}
	if sp.Several && sp.Remote != "" {
		a = append(a, "--remote", sp.Remote)
	}
	return a
}

// Listing is what the registries said about team memory.
type Listing struct {
	Spaces []Space
	// Complete is true when every registry answered with a list, even an
	// empty one. Only then does a team that is missing from Spaces mean its
	// memory is off, so only then are entries removed. A registry that
	// answered "not found" or "forbidden", or that the person is not signed
	// in to, says nothing about the entries it may have brought.
	Complete bool
}

// TokenEnsurer makes sure a memory token is stored for a space, creating
// one when it is missing or about to expire. It lives elsewhere, so this
// package depends on the one method it needs.
type TokenEnsurer interface {
	EnsureToken(ctx context.Context, s Space) error
}

// Runner runs a program and returns what it printed.
type Runner func(ctx context.Context, name string, args ...string) ([]byte, error)

// Tool is an AI tool whose user-level MCP servers loadout manages. It also
// implements target.MCPConfig (a file loadout edits) or target.MCPCommands
// (a tool that edits its own file).
type Tool interface {
	Name() string
	// Product names the tool for people: "Claude Code".
	Product() string
	MarkerDir(kind target.ScopeKind) string
}

// Action is what happened to one entry of one tool.
type Action string

// What Ensure and Decline do per tool and entry.
const (
	ActionAdded     Action = "added"
	ActionUpdated   Action = "updated"
	ActionUnchanged Action = "unchanged"
	ActionRemoved   Action = "removed"
	// ActionAbsent means there was no entry to remove.
	ActionAbsent Action = "absent"
	// ActionNoTool means the tool's folder does not exist: nothing was
	// written and no folder was made.
	ActionNoTool Action = "no tool"
	// ActionDeclined means the person opted this tool out.
	ActionDeclined Action = "declined"
	// ActionRefused means the tool was left as it is; Err says why.
	ActionRefused Action = "refused"
)

// Outcome is the result for one entry of one tool.
type Outcome struct {
	Tool    string
	Product string
	// Server is the entry's name: loadout-memory-lime. Empty when the
	// outcome is about the whole tool.
	Server string
	// Where is the file or command as the person knows it.
	Where  string
	Action Action
	// Err explains ActionRefused.
	Err error
}

// EnvOutcome is the result of setting a tool's description limit, which
// happens only when an entry was added for the tool.
type EnvOutcome struct {
	Tool    string
	Product string
	// Where is the settings file as the person knows it.
	Where string
	// Name and Value are the variable and what loadout set it to.
	Name, Value string
	// Action is ActionAdded, ActionUnchanged (the person set it already)
	// or ActionRefused, with Err.
	Action Action
	Err    error
}

// SpaceFailure is a space that got no entry because its token could not be
// made. Its older entry, if any, stays.
type SpaceFailure struct {
	Space Space
	Err   error
}

// Report is the result of Ensure or Decline.
type Report struct {
	Tools  []Outcome
	Failed []SpaceFailure
	// Env lists the settings variables Ensure set for tools that got an
	// entry.
	Env []EnvOutcome
	// Unreachable is set by EnsureFrom when the registry could not be
	// asked: nothing was changed.
	Unreachable error
}

// Changed returns the outcomes that added, updated or removed an entry.
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
	// Home is the person's home folder; entries are user scope only.
	Home string
	// WishPath is the file recording which tools the person opted out of.
	WishPath string
	// Executable returns the running loadout; LookPath finds a program on
	// the PATH. Tests replace them; nil uses os.Executable and
	// exec.LookPath.
	Executable func() (string, error)
	LookPath   func(file string) (string, error)
	// Run runs a tool's own command line (claude mcp add-json). Tests
	// replace it so the real program never runs; nil runs the program.
	Run Runner
	// Tokens makes the memory token of each space; nil makes none.
	Tokens TokenEnsurer
	Log    *slog.Logger
}

// Service sets up and removes the MCP entries.
type Service struct {
	home       string
	tools      []managed
	wish       wishFile
	executable func() (string, error)
	lookPath   func(string) (string, error)
	tokens     TokenEnsurer
	log        *slog.Logger
}

// managed is a tool with the way to change its servers.
type managed struct {
	Tool
	b backend
}

// New returns a service for tools, each of which must implement
// target.MCPConfig or target.MCPCommands.
func New(opts Options, tools ...Tool) (*Service, error) {
	if opts.Home == "" || opts.WishPath == "" {
		return nil, errors.New("team memory setup needs a home folder and a wish file")
	}
	if opts.Executable == nil {
		opts.Executable = os.Executable
	}
	if opts.LookPath == nil {
		opts.LookPath = exec.LookPath
	}
	if opts.Run == nil {
		opts.Run = runIn(opts.Home)
	}
	if opts.Log == nil {
		opts.Log = slog.New(slog.DiscardHandler)
	}
	e := env{home: opts.Home, run: opts.Run, lookPath: opts.LookPath, log: opts.Log}
	s := &Service{
		home: opts.Home, wish: wishFile{path: opts.WishPath},
		executable: opts.Executable, lookPath: opts.LookPath, tokens: opts.Tokens, log: opts.Log,
	}
	for _, t := range tools {
		var b backend
		switch c := t.(type) {
		case target.MCPConfig:
			b = fileBackend{env: e, t: c}
		case target.MCPCommands:
			b = cmdBackend{env: e, t: c}
		default:
			return nil, fmt.Errorf("the AI tool %q has no team memory setup", t.Name())
		}
		s.tools = append(s.tools, managed{Tool: t, b: b})
	}
	return s, nil
}

// runIn returns a runner whose answer is the stdout and stderr of the
// program together. The program sees home as its home folder, the one
// loadout edits, so it can never change another's. A claude that hangs is
// cut off.
func runIn(home string) Runner {
	return func(ctx context.Context, name string, args ...string) ([]byte, error) {
		ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, name, args...)
		cmd.Env = append(os.Environ(), "HOME="+home, "USERPROFILE="+home)
		return cmd.CombinedOutput()
	}
}

// Wish returns what the person asked for.
func (s *Service) Wish() (Wish, error) { return s.wish.load() }

// Allow records that the tools named (all when none) may have the entries
// again: setup --memory. It adds nothing, the next Ensure does.
func (s *Service) Allow(names ...string) error {
	if err := s.check(names); err != nil {
		return err
	}
	w, err := s.wish.load()
	if err != nil {
		return err
	}
	if len(names) == 0 {
		names = s.names()
	}
	w.Declined = slices.DeleteFunc(w.Declined, func(n string) bool { return slices.Contains(names, n) })
	return s.wish.save(w)
}

// Decline records that the tools named (all when none) are not wanted, and
// removes loadout's entries from them: setup --no-memory.
func (s *Service) Decline(ctx context.Context, names ...string) (Report, error) {
	if err := s.check(names); err != nil {
		return Report{}, err
	}
	w, err := s.wish.load()
	if err != nil {
		return Report{}, err
	}
	if len(names) == 0 {
		names = s.names()
	}
	w.Declined = append(w.Declined, names...)
	if err := s.wish.save(w); err != nil {
		return Report{}, err
	}
	var rep Report
	for _, t := range s.tools {
		if !slices.Contains(names, t.Name()) {
			continue
		}
		if err := ctx.Err(); err != nil {
			return rep, err
		}
		rep.Tools = append(rep.Tools, s.apply(ctx, t, nil, nil, true)...)
	}
	return rep, nil
}

func (s *Service) check(names []string) error {
	for _, n := range names {
		if !slices.Contains(s.names(), n) {
			return fmt.Errorf("no AI tool called %q has team memory; choose from %s", n, strings.Join(s.names(), ", "))
		}
	}
	return nil
}

func (s *Service) names() []string {
	out := make([]string, len(s.tools))
	for i, t := range s.tools {
		out[i] = t.Name()
	}
	return out
}

// namespaceRe is what a namespace may look like before it goes into a
// file key and a command line.
var namespaceRe = regexp.MustCompile(`^[a-z0-9][a-z0-9_.-]{0,62}$`)

// EnsureFrom asks list for the spaces and runs Ensure with them. When it
// cannot, nothing changes: a laptop that is offline keeps its config.
//
// A listing that is not Complete adds and updates entries but removes none.
func (s *Service) EnsureFrom(ctx context.Context, list func(context.Context) (Listing, error)) Report {
	l, err := list(ctx)
	if err != nil {
		s.log.Warn("could not list the team memories, left the AI tools as they are", "err", err)
		return Report{Unreachable: err}
	}
	return s.ensure(ctx, l.Spaces, l.Complete)
}

// Ensure makes every tool's entries match spaces: one for each space,
// removed for each space that is no longer listed. Tools the person
// declined or does not have are left alone. It never fails: what went
// wrong is in the report, and in the log as a warning.
func (s *Service) Ensure(ctx context.Context, spaces []Space) Report {
	return s.ensure(ctx, spaces, true)
}

// ensure is Ensure; prune says whether entries of unlisted spaces go.
func (s *Service) ensure(ctx context.Context, spaces []Space, prune bool) Report {
	var rep Report
	w, err := s.wish.load()
	if err != nil {
		s.log.Warn("left the team memory setup alone", "err", err)
		rep.Unreachable = err
		return rep
	}
	var use []managed
	for _, t := range s.tools {
		switch {
		case w.Has(t.Name()):
			rep.Tools = append(rep.Tools, s.outcome(t, "", ActionDeclined, nil))
		case !s.hasMarker(t):
			rep.Tools = append(rep.Tools, s.outcome(t, "", ActionNoTool, nil))
		default:
			use = append(use, t)
		}
	}
	if len(use) == 0 {
		return rep
	}
	// Tokens are made only when a tool will use them.
	wanted, listed := s.plan(ctx, spaces, &rep)
	for _, t := range use {
		if ctx.Err() != nil {
			break
		}
		outs := s.apply(ctx, t, wanted, listed, prune)
		rep.Tools = append(rep.Tools, outs...)
		if slices.ContainsFunc(outs, func(o Outcome) bool { return o.Action == ActionAdded }) {
			if e, ok := s.setEnv(t); ok {
				rep.Env = append(rep.Env, e)
			}
		}
	}
	return rep
}

// setEnv raises the tool's cut on MCP instructions, when the tool has one
// and the person has not set it. It runs only after an entry was added, so
// a person who never gets team memory never has a setting changed.
func (s *Service) setEnv(t managed) (EnvOutcome, bool) {
	c, ok := t.Tool.(target.MCPEnv)
	if !ok {
		return EnvOutcome{}, false
	}
	name, value := c.MCPEnvVar()
	rel := c.MCPEnvFile()
	e := env{home: s.home, log: s.log}
	before, after, err := editFile(e, rel, func([]byte) bool { return true }, func(cur []byte) ([]byte, error) {
		return c.WithEnvDefault(cur, name, value)
	})
	out := EnvOutcome{Tool: t.Name(), Product: t.Product(), Where: "~/" + rel, Name: name, Value: value, Action: ActionAdded, Err: err}
	switch {
	case err != nil:
		out.Action = ActionRefused
		s.log.Warn("left an AI tool's settings alone", "tool", t.Name(), "file", out.Where, "err", err)
	case bytes.Equal(before, after):
		out.Action = ActionUnchanged
	}
	return out, true
}

// plan turns spaces into the servers to have, making their tokens first,
// and the names of every valid listed space, which stay even when their
// token failed.
func (s *Service) plan(ctx context.Context, spaces []Space, rep *Report) (wanted []target.MCPServer, listed map[string]bool) {
	listed = map[string]bool{}
	cmd := s.command()
	for _, sp := range slices.SortedFunc(slices.Values(spaces), func(a, b Space) int { return strings.Compare(a.Namespace, b.Namespace) }) {
		if listed[sp.Namespace] {
			continue
		}
		if !namespaceRe.MatchString(sp.Namespace) {
			rep.Failed = append(rep.Failed, SpaceFailure{Space: sp, Err: fmt.Errorf("%q is not a namespace name loadout can put in a tool's configuration", sp.Namespace)})
			continue
		}
		listed[sp.Namespace] = true
		if s.tokens != nil {
			if err := s.tokens.EnsureToken(ctx, sp); err != nil {
				s.log.Warn("could not make a memory token, left the entry as it is", "namespace", sp.Namespace, "err", err)
				rep.Failed = append(rep.Failed, SpaceFailure{Space: sp, Err: err})
				continue
			}
		}
		wanted = append(wanted, target.MCPServer{
			Name:    target.MemoryServerPrefix + sp.Namespace,
			Command: cmd,
			Args:    sp.args(),
		})
	}
	return wanted, listed
}

// apply adds or updates the wanted servers in t and removes its entries
// of spaces that are not in keep. A tool with nothing to do is not even
// looked for, so a missing claude program is only a warning when it
// matters.
func (s *Service) apply(ctx context.Context, t managed, wanted []target.MCPServer, keep map[string]bool, prune bool) []Outcome {
	have, err := t.b.names(ctx)
	if err != nil {
		return []Outcome{s.outcome(t, "", ActionRefused, err)}
	}
	stale := slices.DeleteFunc(slices.Clone(have), func(name string) bool {
		return !prune || keep[strings.TrimPrefix(name, target.MemoryServerPrefix)]
	})
	if len(wanted) == 0 && len(stale) == 0 {
		return nil
	}
	if err := t.b.ready(); err != nil {
		return []Outcome{s.outcome(t, "", ActionRefused, err)}
	}
	var out []Outcome
	for _, w := range wanted {
		if ctx.Err() != nil {
			return out
		}
		a, err := t.b.put(ctx, w)
		out = append(out, s.outcome(t, w.Name, a, err))
	}
	for _, name := range stale {
		if ctx.Err() != nil {
			break
		}
		a, err := t.b.drop(ctx, name)
		out = append(out, s.outcome(t, name, a, err))
	}
	return out
}

func (s *Service) outcome(t managed, server string, a Action, err error) Outcome {
	if err != nil {
		s.log.Warn("left an AI tool's MCP servers alone", "tool", t.Name(), "server", server, "err", err)
	}
	return Outcome{Tool: t.Name(), Product: t.Product(), Server: server, Where: t.b.where(), Action: a, Err: err}
}

// hasMarker reports whether the tool's folder exists in the home folder.
func (s *Service) hasMarker(t managed) bool {
	info, err := os.Stat(filepath.Join(s.home, filepath.FromSlash(t.MarkerDir(target.ScopeUser))))
	return err == nil && info.IsDir()
}

// command is what a tool's entry runs: bare loadout when the PATH finds
// this very binary, else its absolute path, since a tool started from the
// Dock may not see the shell's PATH.
func (s *Service) command() string {
	exe, err := s.executable()
	if err != nil {
		return "loadout"
	}
	exe = resolved(exe)
	if found, err := s.lookPath("loadout"); err == nil && resolved(found) == exe {
		return "loadout"
	}
	return exe
}

// resolved is p with its symbolic links followed, or p when that fails.
func resolved(p string) string {
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	return p
}
