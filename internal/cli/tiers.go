package cli

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/petervlad/loadout/internal/agent"
	"github.com/petervlad/loadout/internal/api"
	"github.com/petervlad/loadout/internal/client"
	"github.com/petervlad/loadout/internal/target"
	"github.com/petervlad/loadout/internal/tier"
)

// fetchTiers returns the resolved model table of c's view. A registry from
// before model tables answers 404, which means the empty table: installs
// render the built-in models, as they always did.
func fetchTiers(ctx context.Context, c *client.Client) (tier.Table, error) {
	resp, err := c.Tiers(ctx)
	if apiErr, ok := errors.AsType[*client.APIError](err); ok && apiErr.Status == http.StatusNotFound {
		return tier.Table{}, nil
	}
	if err != nil {
		return tier.Table{}, fmt.Errorf("model settings: %w", err)
	}
	t, err := tableOf(resp.Cells, "")
	if err != nil {
		return tier.Table{}, fmt.Errorf("model settings from the registry: %w", err)
	}
	return t, nil
}

// Tiers returns the table of the client's view. A view the person lost
// answers 403, like any listing; the packages' own namespace still
// answers, so sync keeps working through another team.
func (a registryAdapter) Tiers(ctx context.Context) (tier.Table, error) {
	t, err := fetchTiers(ctx, a.c)
	if apiErr, ok := errors.AsType[*client.APIError](err); ok && apiErr.Status == http.StatusForbidden && a.team != "" {
		return fetchTiers(ctx, a.c.In(a.team))
	}
	return t, err
}

// tableOf turns wire cells into a table. With own set it keeps only the
// values that namespace sets itself, which is what a change replaces.
func tableOf(cells []api.TierCell, own string) (tier.Table, error) {
	out := make([]tier.Cell, 0, len(cells))
	for _, c := range cells {
		ch := tier.Choice{Model: c.Model, Effort: tier.Effort(c.Effort)}
		if own != "" {
			if c.ModelFrom != own {
				ch.Model = ""
			}
			if c.EffortFrom != own {
				ch.Effort = ""
			}
		}
		out = append(out, tier.Cell{Tool: c.Tool, Hint: agent.Model(c.Hint), Choice: ch})
	}
	return tier.FromCells(out)
}

// apiTiers is a stack's resolved table in wire form, as the registry
// answers it.
func apiTiers(st tier.Stack) api.TiersResponse {
	t := st.Resolve()
	out := api.TiersResponse{Namespace: st.Namespace(), Cells: []api.TierCell{}, Digest: t.Digest()}
	for _, c := range t.Cells() {
		mf, ef := st.Origin(c.Tool, c.Hint)
		out.Cells = append(out.Cells, api.TierCell{Tool: c.Tool, Hint: string(c.Hint), Model: c.Model, Effort: string(c.Effort), ModelFrom: mf, EffortFrom: ef})
	}
	return out
}

func apiCells(t tier.Table) []api.TierCell {
	out := []api.TierCell{}
	for _, c := range t.Cells() {
		out = append(out, api.TierCell{Tool: c.Tool, Hint: string(c.Hint), Model: c.Model, Effort: string(c.Effort)})
	}
	return out
}

// setTiers applies --model and --effort edits to namespace's own table.
// Only tools this loadout can install are accepted, so a typo does not
// become a column nobody reads.
func (d Deps) setTiers(ctx context.Context, m manager, namespace string, models, efforts []string) (api.TiersResponse, error) {
	edits, err := tier.ParseEdits(models, efforts)
	if err != nil {
		return api.TiersResponse{}, err
	}
	for _, e := range edits {
		if _, ok := d.Installer.Renderer(e.Key.Tool); !ok {
			return api.TiersResponse{}, fmt.Errorf("%s: unknown tool %q; loadout installs into %v", e.Key, e.Key.Tool, d.Installer.Targets())
		}
	}
	cur, err := m.NamespaceTiers(ctx, namespace)
	if err != nil {
		return api.TiersResponse{}, err
	}
	own, err := tableOf(cur.Cells, namespace)
	if err != nil {
		return api.TiersResponse{}, err
	}
	next, err := own.Apply(edits)
	if err != nil {
		return api.TiersResponse{}, err
	}
	return m.SetTiers(ctx, namespace, apiCells(next))
}

func newNamespaceTiersCmd(d Deps, dataDir, remoteName *string) *cobra.Command {
	return &cobra.Command{
		Use:   "tiers NAME",
		Short: "Show which model each AI tool runs for an agent's hint in a namespace",
		Long: `An agent says how capable a model it needs: fast, balanced or deep. The
model tiers of a project, with its teams' own values over them, say which
model and reasoning effort each AI tool gets for each hint. FROM names
where a value comes from: the namespace, a project above it, or built-in.
Owners change them with: loadout namespace set NAME --model TOOL:HINT=MODEL`,
		Example: `  loadout namespace tiers payments`,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			var resp api.TiersResponse
			err := d.withManager(cmd.Context(), *dataDir, *remoteName, func(m manager) error {
				var err error
				resp, err = m.NamespaceTiers(cmd.Context(), args[0])
				return err
			})
			if err != nil {
				return err
			}
			return d.printTiers(cmd, resp)
		},
	}
}

// printTiers shows the resolved table for every tool this loadout installs
// into, with the built-in values where the table sets none.
func (d Deps) printTiers(cmd *cobra.Command, resp api.TiersResponse) error {
	t, err := tableOf(resp.Cells, "")
	if err != nil {
		return err
	}
	from := map[tier.Key]api.TierCell{}
	for _, c := range resp.Cells {
		from[tier.Key{Tool: c.Tool, Hint: agent.Model(c.Hint)}] = c
	}
	w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "TOOL\tHINT\tMODEL\tEFFORT\tFROM")
	for _, tool := range d.Installer.Targets() {
		r, _ := d.Installer.Renderer(tool)
		for _, h := range tier.Hints() {
			k := tier.Key{Tool: tool, Hint: h}
			c := target.Choose(r, t, h)
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", tool, h, dashIfEmpty(c.Model), dashIfEmpty(string(c.Effort)), tierOrigin(from[k], c))
		}
	}
	if err := w.Flush(); err != nil {
		return err
	}
	fmt.Fprintf(cmd.OutOrStdout(), "\nA dash leaves it to the person's own choice in the tool.\n")
	return nil
}

// tierOrigin says where a row's values come from.
func tierOrigin(c api.TierCell, got tier.Choice) string {
	model := c.ModelFrom
	if model == "" && got.Model != "" {
		model = "built-in"
	}
	switch {
	case model == "" && c.EffortFrom == "":
		return "-"
	case c.EffortFrom == "" || c.EffortFrom == model:
		return model
	case model == "":
		return c.EffortFrom
	}
	return fmt.Sprintf("%s, effort from %s", model, c.EffortFrom)
}
