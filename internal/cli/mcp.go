package cli

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/petervlad/loadout/internal/mcpbridge"
	"github.com/petervlad/loadout/internal/remote"
)

// BridgeRunner serves one team's memory on stdio until its input closes.
// *mcpbridge.Bridge is one.
type BridgeRunner interface {
	Run(ctx context.Context) error
}

var _ BridgeRunner = (*mcpbridge.Bridge)(nil)

func newMcpCmd(d Deps) *cobra.Command {
	var team, remoteName string
	cmd := &cobra.Command{
		Use:   "mcp --team TEAM [--remote NAME]",
		Short: "Serve a team's memory to an AI tool over stdio",
		Long: `Start an MCP server on standard input and output that forwards every call
to the team's memory on the registry. An AI tool launches it, so you rarely
run it yourself: loadout install and sync set it up.

It reads your memory token from ~/.loadout/memory-credentials.yaml (or
LOADOUT_MEMORY_TOKEN) and trusts the registry's CA file if the remote has
one. Make a token with: loadout memory token create`,
		Example: "  loadout mcp --team lime",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if d.Bridge == nil {
				return fmt.Errorf("this build of loadout has no memory bridge")
			}
			cfg, err := d.bridgeConfig(team, remoteName)
			if err != nil {
				return err
			}
			// The tool's pipes are the command's: stdout carries MCP frames
			// only, so nothing else may print to it while this runs.
			cfg.Stdin, cfg.Stdout = cmd.InOrStdin(), cmd.OutOrStdout()
			b, err := d.Bridge(cfg)
			if err != nil {
				return err
			}
			return b.Run(cmd.Context())
		},
	}
	f := cmd.Flags()
	f.StringVar(&team, "team", "", "the team whose memory to serve")
	f.StringVar(&remoteName, "remote", "", "the registry (default: the one the team is joined on, else the default remote)")
	_ = cmd.MarkFlagRequired("team")
	return cmd
}

// bridgeConfig resolves the remote of team as every command does and reads
// the memory token for it.
func (d Deps) bridgeConfig(team, remoteName string) (mcpbridge.Config, error) {
	cfg, err := d.Remotes.Load()
	if err != nil {
		return mcpbridge.Config{}, err
	}
	r, err := cfg.ResolveFor(remoteName, team, "")
	if err != nil {
		return mcpbridge.Config{}, err
	}
	tok, err := d.memoryTokenFor(cfg, r, remoteName != "")
	if err != nil {
		return mcpbridge.Config{}, err
	}
	if tok == "" {
		return mcpbridge.Config{}, fmt.Errorf("no memory token for %s. Make one with: loadout memory token create --remote %s", r.Name, r.Name)
	}
	return mcpbridge.Config{URL: r.URL, Team: team, Token: tok, CAFile: r.CAFile, Log: d.Log}, nil
}

// memoryTokenFor is the memory token to send to r. LOADOUT_MEMORY_TOKEN names
// no remote, so with several remotes it is used only when the command named
// one (named), or it could go to a registry it was not made for. Otherwise the
// stored token of r is used.
func (d Deps) memoryTokenFor(cfg *remote.Config, r remote.Remote, named bool) (string, error) {
	if d.Remotes.MemoryTokenFromEnv() && !named && len(cfg.Remotes) > 1 {
		stored, err := d.Remotes.StoredMemoryToken(r.Name)
		if err != nil {
			return "", err
		}
		if stored == "" {
			return "", fmt.Errorf("%s is set but you have %d registries, and it does not say which one it belongs to. Name it with --remote NAME", remote.EnvMemoryToken, len(cfg.Remotes))
		}
		return stored, nil
	}
	return d.Remotes.MemoryToken(r.Name)
}
