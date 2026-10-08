package review

import (
	"errors"
	"fmt"
	"os"
	"path"
	"strings"

	"github.com/petervlad/loadout/internal/pack"
	"github.com/petervlad/loadout/internal/target"
	"github.com/petervlad/loadout/internal/tier"
)

// workspace is one side's throwaway project folder. It writes rendered
// units through an os.Root and refuses a unit whose path another unit
// already holds, so nothing written later can replace the side's own
// version.
type workspace struct {
	dir   string
	root  *os.Root
	units []string // unit roots written so far, lower-cased
	// contextBytes counts the team packages' rendered bytes, for
	// MaxContextBytes.
	contextBytes int64
}

// newWorkspace makes a fresh folder under tempDir (the system temp folder
// when empty).
func newWorkspace(tempDir, label string) (*workspace, error) {
	dir, err := os.MkdirTemp(tempDir, "loadout-review-"+label+"-")
	if err != nil {
		return nil, fmt.Errorf("compare: %w", err)
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, errors.Join(fmt.Errorf("compare: %w", err), os.RemoveAll(dir))
	}
	return &workspace{dir: dir, root: root}, nil
}

// render renders p for this folder at project scope, under name when set,
// with the reviewer's model table.
func (w *workspace) render(r Renderer, p *pack.Pack, name string, models tier.Table) (target.Unit, error) {
	stamp := target.Stamp{ID: p.ID(), Version: p.Version(), Digest: p.Digest()}
	scope := target.Scope{Kind: target.ScopeProject, Root: w.dir}
	return target.Render(r, p, scope, stamp, target.RenderOptions{Name: name, Models: models})
}

// taken reports whether a unit at root would overlap one already written:
// the same path, or one inside the other. Case is ignored, since macOS and
// Windows would write both to the same place.
func (w *workspace) taken(root string) bool {
	root = strings.ToLower(root)
	for _, u := range w.units {
		if u == root || strings.HasPrefix(u, root+"/") || strings.HasPrefix(root, u+"/") {
			return true
		}
	}
	return false
}

// write writes u, or refuses it when its path is taken.
func (w *workspace) write(u target.Unit) error {
	if w.taken(u.Root) {
		return fmt.Errorf("%s is already in the review folder", u.Root)
	}
	for _, f := range u.Files {
		if err := w.root.MkdirAll(path.Dir(f.Path), 0o755); err != nil {
			return err
		}
		if err := w.root.WriteFile(f.Path, f.Data, 0o644); err != nil {
			return err
		}
	}
	w.units = append(w.units, strings.ToLower(u.Root))
	return nil
}

// remove closes the folder and deletes it with everything in it.
func (w *workspace) remove() error {
	return errors.Join(w.root.Close(), os.RemoveAll(w.dir))
}

// unitSize is the number of bytes u writes.
func unitSize(u target.Unit) int64 {
	var n int64
	for _, f := range u.Files {
		n += int64(len(f.Data))
	}
	return n
}
