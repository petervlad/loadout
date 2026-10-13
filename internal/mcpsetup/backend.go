package mcpsetup

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"slices"
	"strings"

	"github.com/petervlad/loadout/internal/target"
)

// backend is how loadout changes one tool's MCP servers: by editing a file
// the tool leaves alone, or by asking the tool's own command line. The
// service does not care which.
type backend interface {
	// where names the file for people: ~/.copilot/mcp-config.json, or the
	// ~/.claude.json that the tool's own command changes.
	where() string
	// ready says why the tool cannot be driven now, or nil.
	ready() error
	// names lists the entries loadout added.
	names(ctx context.Context) ([]string, error)
	put(ctx context.Context, s target.MCPServer) (Action, error)
	drop(ctx context.Context, name string) (Action, error)
}

// env is what both backends need from the service.
type env struct {
	home     string
	run      Runner
	lookPath func(string) (string, error)
	log      *slog.Logger
}

// fileBackend edits a file with jsonedit, like hook.Service does.
type fileBackend struct {
	env
	t target.MCPConfig
}

func (b fileBackend) where() string { return "~/" + b.t.MCPFile() }

func (fileBackend) ready() error { return nil }

func (b fileBackend) names(context.Context) ([]string, error) {
	root, err := os.OpenRoot(b.home)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	if err := noLinks(root, b.t.MCPFile()); err != nil {
		return nil, err
	}
	data, err := read(root, b.t.MCPFile())
	if err != nil {
		return nil, err
	}
	return b.t.LoadoutServers(data)
}

func (b fileBackend) put(_ context.Context, s target.MCPServer) (Action, error) {
	before, after, err := b.change(func(cur []byte) ([]byte, error) { return b.t.WithMCPServer(cur, s) })
	switch {
	case err != nil:
		return ActionRefused, err
	case bytes.Equal(before, after):
		return ActionUnchanged, nil
	case b.has(before, s.Name):
		return ActionUpdated, nil
	}
	return ActionAdded, nil
}

func (b fileBackend) drop(_ context.Context, name string) (Action, error) {
	before, after, err := b.change(func(cur []byte) ([]byte, error) { return b.t.WithoutMCPServer(cur, name) })
	switch {
	case err != nil:
		return ActionRefused, err
	case before == nil || bytes.Equal(before, after):
		return ActionAbsent, nil
	}
	return ActionRemoved, nil
}

func (b fileBackend) has(data []byte, name string) bool {
	if data == nil {
		return false
	}
	names, err := b.t.LoadoutServers(data)
	return err == nil && slices.Contains(names, name)
}

// change applies fn to the tool's MCP file and writes the result.
func (b fileBackend) change(fn func(cur []byte) ([]byte, error)) (before, after []byte, err error) {
	return editFile(b.env, b.t.MCPFile(), b.holdsMore, fn)
}

// holdsMore reports whether data has anything besides loadout's entries.
func (b fileBackend) holdsMore(data []byte) bool {
	names, err := b.t.LoadoutServers(data)
	if err != nil {
		return true
	}
	for _, n := range names {
		rest, err := b.t.WithoutMCPServer(data, n)
		if err != nil {
			return true
		}
		if rest == nil {
			return false
		}
		data = rest
	}
	return true
}

// cmdBackend drives the tool's own command line and only reads its file.
type cmdBackend struct {
	env
	t target.MCPCommands
}

func (b cmdBackend) where() string { return "~/" + b.t.MCPStateFile() }

func (b cmdBackend) ready() error {
	if _, err := b.lookPath(b.t.MCPProgram()); err != nil {
		return fmt.Errorf("the %s program is not on the PATH, so loadout cannot add its memory server; install it or fix the PATH and run loadout sync", b.t.MCPProgram())
	}
	return nil
}

func (b cmdBackend) entries() ([]target.MCPServer, error) {
	root, err := os.OpenRoot(b.home)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	data, err := read(root, b.t.MCPStateFile())
	if err != nil {
		return nil, err
	}
	return b.t.LoadoutServers(data)
}

func (b cmdBackend) names(context.Context) ([]string, error) {
	servers, err := b.entries()
	if err != nil {
		return nil, err
	}
	out := make([]string, len(servers))
	for i, s := range servers {
		out[i] = s.Name
	}
	return out, nil
}

func (b cmdBackend) put(ctx context.Context, s target.MCPServer) (Action, error) {
	servers, err := b.entries()
	if err != nil {
		return ActionRefused, err
	}
	action := ActionAdded
	var old *target.MCPServer
	if i := slices.IndexFunc(servers, func(have target.MCPServer) bool { return have.Name == s.Name }); i >= 0 {
		if servers[i].Equal(s) {
			return ActionUnchanged, nil
		}
		// add-json refuses a name that exists, so the old entry goes first.
		if err := b.exec(ctx, b.t.RemoveMCPArgs(s.Name)); err != nil {
			return ActionRefused, err
		}
		old = &servers[i]
		action = ActionUpdated
	}
	args, err := b.t.AddMCPArgs(s)
	if err == nil {
		err = b.exec(ctx, args)
	}
	if err != nil {
		return ActionRefused, errors.Join(err, b.restore(ctx, old))
	}
	return action, nil
}

// restore puts back the entry that put removed, so a failed update does not
// leave the person with no memory server. It does nothing for a new entry.
func (b cmdBackend) restore(ctx context.Context, old *target.MCPServer) error {
	if old == nil {
		return nil
	}
	args, err := b.t.AddMCPArgs(*old)
	if err == nil {
		err = b.exec(ctx, args)
	}
	if err != nil {
		return fmt.Errorf("the earlier entry %s could not be put back either. Run loadout sync to add it again: %w", old.Name, err)
	}
	return nil
}

func (b cmdBackend) drop(ctx context.Context, name string) (Action, error) {
	if err := b.exec(ctx, b.t.RemoveMCPArgs(name)); err != nil {
		return ActionRefused, err
	}
	return ActionRemoved, nil
}

func (b cmdBackend) exec(ctx context.Context, args []string) error {
	out, err := b.run(ctx, b.t.MCPProgram(), args...)
	if err != nil {
		// The text is the tool's, about its own configuration: it holds
		// no secret, since the entry only runs "loadout mcp".
		return fmt.Errorf("%s %s failed: %w (%s)", b.t.MCPProgram(), strings.Join(args[:min(len(args), 2)], " "), err, strings.TrimSpace(string(out)))
	}
	return nil
}
