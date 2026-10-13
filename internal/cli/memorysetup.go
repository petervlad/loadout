package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/spf13/cobra"

	"github.com/petervlad/loadout/internal/client"
	"github.com/petervlad/loadout/internal/mcpsetup"
	"github.com/petervlad/loadout/internal/target"
)

// ensureMemory makes the AI tools' team memory servers match what the
// person's registries say, after install and sync. It never fails the
// command: what went wrong is a warning, since the packages stand.
func (d Deps) ensureMemory(cmd *cobra.Command) {
	if d.MemorySetup == nil {
		return
	}
	asked := false
	rep := d.MemorySetup.EnsureFrom(cmd.Context(), func(ctx context.Context) (mcpsetup.Listing, error) {
		spaces, n, complete, err := d.memorySpaces(ctx)
		asked = n > 0
		return mcpsetup.Listing{Spaces: spaces, Complete: complete}, err
	})
	if !asked && rep.Unreachable == nil {
		return // no registry to ask: nothing says what the tools should have
	}
	printMemory(cmd.OutOrStdout(), rep)
}

// memorySpaces asks every remote the person is signed in to which teams
// have memory on, and returns them all with the number of remotes asked.
// A registry older than team memory (404) or one that does not take this
// token (403) gives no list, and neither does a remote the person is not
// signed in to: complete is then false, and nothing is removed because of
// it, since none of them said a team's memory is off. Any other failure, a
// network one included, fails the whole list, so a laptop that is offline
// keeps what it has.
func (d Deps) memorySpaces(ctx context.Context) (spaces []mcpsetup.Space, asked int, complete bool, err error) {
	cfg, err := d.Remotes.Load()
	if err != nil {
		return nil, 0, false, err
	}
	complete = true
	for _, r := range cfg.Remotes {
		if err := ctx.Err(); err != nil {
			return nil, 0, false, err
		}
		token, err := d.Remotes.Token(r.Name)
		if err != nil {
			return nil, 0, false, err
		}
		if token == "" {
			complete = false
			continue
		}
		c, err := d.newClient(r, token)
		if err != nil {
			return nil, 0, false, err
		}
		list, err := c.MemorySpaces(ctx)
		switch {
		case err == nil:
		case noMemoryHere(err):
			asked++
			complete = false
			continue
		default:
			return nil, 0, false, fmt.Errorf("ask %s for its team memory: %w", r.Name, err)
		}
		asked++
		for _, sp := range list {
			spaces = append(spaces, mcpsetup.Space{Namespace: sp.Namespace, Endpoint: sp.Endpoint, Remote: r.Name, Several: len(cfg.Remotes) > 1})
		}
	}
	return spaces, asked, complete, nil
}

// noMemoryHere is true for a registry that has no team memory for this
// person: one that predates it, or that refuses the kind of token.
func noMemoryHere(err error) bool {
	e, ok := errors.AsType[*client.APIError](err)
	return ok && (e.Status == http.StatusNotFound || e.Status == http.StatusForbidden)
}

// printMemory says what changed, one line each. What could not be done is
// already a warning in the log, which reaches the terminal.
func printMemory(out io.Writer, rep mcpsetup.Report) {
	for _, o := range rep.Tools {
		team := teamOf(o.Server)
		switch o.Action {
		case mcpsetup.ActionAdded:
			fmt.Fprintf(out, "added %s's team memory to %s (%s)\n", team, o.Product, o.Where)
		case mcpsetup.ActionUpdated:
			fmt.Fprintf(out, "updated %s's team memory in %s (%s)\n", team, o.Product, o.Where)
		case mcpsetup.ActionRemoved:
			fmt.Fprintf(out, "removed %s's team memory from %s (%s)\n", team, o.Product, o.Where)
		}
	}
	for _, e := range rep.Env {
		if e.Action == mcpsetup.ActionAdded {
			fmt.Fprintf(out, "set %s=%s in %s. This raises the limit for every MCP server in %s, not only loadout's, so the whole memory index fits. Remove it there to undo.\n", e.Name, e.Value, e.Where, e.Product)
		}
	}
}

// teamOf is the namespace in an entry's name, loadout-memory-lime.
func teamOf(server string) string {
	if team, ok := strings.CutPrefix(server, target.MemoryServerPrefix); ok {
		return team
	}
	return server
}

// setupMemory handles setup --memory and --no-memory.
func setupMemory(cmd *cobra.Command, d Deps, targets []string, want bool) error {
	if d.MemorySetup == nil {
		return errors.New("this loadout was built without team memory setup")
	}
	if !want {
		rep, err := d.MemorySetup.Decline(cmd.Context(), targets...)
		if err != nil {
			return err
		}
		printMemory(cmd.OutOrStdout(), rep)
		fmt.Fprintln(cmd.OutOrStdout(), "loadout will not add team memory to your AI tools; bring it back with: loadout setup --memory")
		return nil
	}
	if err := d.MemorySetup.Allow(targets...); err != nil {
		return err
	}
	fmt.Fprintln(cmd.OutOrStdout(), "loadout may add team memory to your AI tools again")
	d.ensureMemory(cmd)
	return nil
}
