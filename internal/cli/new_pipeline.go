package cli

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/petervlad/loadout/internal/api"
	"github.com/petervlad/loadout/internal/client"
	"github.com/petervlad/loadout/internal/pack"
	"github.com/petervlad/loadout/internal/pipeline"
	"github.com/petervlad/loadout/internal/release"
	"github.com/petervlad/loadout/internal/semver"
)

// newBinding writes a binding of the template named by uses into dir: every
// slot listed and empty, the template's edges as comments. The template comes
// from the registry of the new package's team, as install reads a binding's.
func (d Deps) newBinding(cmd *cobra.Command, id pack.ID, dir, uses, remoteName string) error {
	ref, err := pipeline.ParseRef(uses)
	if err != nil {
		// ParseRef starts its message with "uses", so the flag name is not said twice.
		return errors.New("--uses " + strings.TrimPrefix(err.Error(), "uses "))
	}
	tid, err := pack.ParseID(ref.Layer + "/" + ref.Name)
	if err != nil {
		return fmt.Errorf("--uses %q: %w", uses, err)
	}
	c, _, err := d.connectFor(remoteName, id.Team(), "")
	if err != nil {
		return err
	}
	p, err := release.Resolve(cmd.Context(), c, pack.Requirement{ID: tid, Major: ref.Major})
	if errors.Is(err, api.ErrNotFound) {
		return noMajorError(cmd.Context(), c, tid, ref)
	}
	if err != nil {
		return err
	}
	pl, _ := p.Pipeline()
	t, ok := pl.(*pipeline.Template)
	if !ok {
		return fmt.Errorf("%s is a binding, not a template, so nothing can use it. Name a template, one made with loadout new pipeline and no --uses", tid)
	}
	files := map[string][]byte{pack.PipelineFile: pipeline.Scaffold(ref, t)}
	if _, err := pack.ScaffoldWith(dir, id, pack.KindPipeline, files); err != nil {
		return err
	}
	fmt.Fprintf(cmd.OutOrStdout(), "created %s\nfill bind: in %s with one of your agents for each slot, then run: loadout publish %s\n",
		dir, filepath.Join(dir, pack.PipelineFile), dir)
	return nil
}

// noMajorError says why ref cannot be used: the template is unknown, or it
// has releases only in other majors, which it lists.
func noMajorError(ctx context.Context, c *client.Client, tid pack.ID, ref pipeline.Ref) error {
	rels, err := c.Releases(ctx, tid)
	if err != nil && !errors.Is(err, api.ErrNotFound) {
		return err
	}
	var majors []string // usable ones, from 1
	zero := false
	for _, r := range rels {
		v, err := semver.Parse(r.Version)
		if err != nil {
			continue
		}
		m := strconv.Itoa(v.Major())
		switch {
		case v.Major() == 0:
			zero = true
		case !slices.Contains(majors, m):
			majors = append(majors, m)
		}
	}
	switch {
	case len(majors) > 0:
		return fmt.Errorf("%s has no release of major %d, only major %s. Name one of those, for example --uses %s/%s@%s", tid, ref.Major, strings.Join(majors, ", "), ref.Layer, ref.Name, majors[len(majors)-1])
	case zero:
		return fmt.Errorf("%s has only 0.x releases, which a team cannot use. Its maintainers need to publish 1.0.0 first, with: loadout publish DIR --bump major", tid)
	}
	return fmt.Errorf("%s has no release that your team can read. Check the name, or ask the maintainer of %s to publish it", tid, ref.Layer)
}
