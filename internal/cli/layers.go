package cli

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/petervlad/loadout/internal/api"
	"github.com/petervlad/loadout/internal/client"
)

func newLayersCmd(d Deps) *cobra.Command {
	var remoteName string
	cmd := &cobra.Command{
		Use:   "layers",
		Short: "Show the layers each of your teams reads, closest first",
		Long: `Show, for each team you joined on the registry, the layers it reads: the
team, then its parent and so on up to the root (L0). When two layers ship
a package with the same name, the closer layer's package is the one
installed.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := d.Remotes.Load()
			if err != nil {
				return err
			}
			c, r, err := d.connect(remoteName)
			if err != nil {
				return err
			}
			views := viewsOf(cfg, r.Name)
			if len(views) == 0 {
				views = []string{""}
			}
			out := cmd.OutOrStdout()
			for i, v := range views {
				layers, err := fetchLayers(cmd.Context(), c.In(v))
				if err != nil {
					return err
				}
				if len(layers) == 0 {
					fmt.Fprintf(out, "%s has no layers: it predates them, so your token reads only its own namespace\n", r.Name)
					return nil
				}
				if i > 0 {
					fmt.Fprintln(out)
				}
				w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
				fmt.Fprintln(w, "LAYER\tNAMESPACE\tKIND\tACCESS")
				for _, l := range layers {
					access := "read"
					if l.Depth == 0 {
						access = "your team"
					}
					fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", level(layers, l.Depth), l.Namespace, l.Kind, access)
				}
				if err := w.Flush(); err != nil {
					return err
				}
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&remoteName, "remote", "", "registry (default: the default remote)")
	return cmd
}

// fetchLayers returns the token's layers, closest first. A registry from
// before layers answers 404; that is no layers, not an error.
func fetchLayers(ctx context.Context, c *client.Client) ([]api.Layer, error) {
	layers, err := c.Layers(ctx)
	if apiErr, ok := errors.AsType[*client.APIError](err); ok && apiErr.Status == http.StatusNotFound {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return layers, nil
}

// depthOf returns the depth of namespace ns, or 0 when it is not a known
// layer (a registry without layers only serves the token's own namespace).
func depthOf(layers []api.Layer, ns string) int {
	for _, l := range layers {
		if l.Namespace == ns {
			return l.Depth
		}
	}
	return 0
}

// level names a depth by its distance from the root: the root is L0.
func level(layers []api.Layer, depth int) string {
	return fmt.Sprintf("L%d", len(layers)-1-depth)
}

// layerLabel describes the layer of namespace ns, such as "L0 project".
func layerLabel(layers []api.Layer, ns string) string {
	for _, l := range layers {
		if l.Namespace == ns {
			return level(layers, l.Depth) + " " + string(l.Kind)
		}
	}
	return "-"
}
