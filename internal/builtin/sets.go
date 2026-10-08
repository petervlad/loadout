package builtin

import (
	"cmp"
	"fmt"
	"io/fs"
	"maps"
	"path"
	"slices"
	"strings"

	"github.com/petervlad/loadout/deploy"
)

// The names of the sets.
const (
	SetEveryday = "everyday"
	SetHost     = "host"
)

// Set is a group of built-in skills that are installed together.
type Set struct {
	// Name is how the set is asked for: "everyday", "host".
	Name string
	// Auto sets are installed by login and sync. Others are installed only
	// when the person asks for them (setup --host-skills), and after that
	// sync keeps them in step without ever adding them somewhere new.
	Auto bool
	// dir is the set's folder in the embedded files.
	dir string
	// order is the order a person meets the skills in; a skill not listed
	// comes after them, by name.
	order []string
	// assets adds files to a skill, such as the deploy files from
	// deploy.FS, so the folder under deploy/ stays their only copy. Nil
	// adds nothing.
	assets func(skill string) (map[string][]byte, error)
}

var (
	// The skills that teach an agent to use loadout: set up, write,
	// review, run a team.
	everyday = Set{
		Name: SetEveryday, Auto: true, dir: "skills",
		order: []string{"loadout-setup", "loadout-author", "loadout-review", "loadout-admin"},
	}
	// The skills that set up a registry on the person's own machine or
	// cloud account.
	host = Set{
		Name: SetHost, dir: "host",
		order:  []string{"loadout-host-setup", "loadout-host-provision"},
		assets: hostAssets,
	}
)

// Sets returns every set of built-in skills, everyday first.
func Sets() []Set { return []Set{everyday, host} }

// SetNamed returns the set called name.
func SetNamed(name string) (Set, bool) {
	for _, s := range Sets() {
		if s.Name == name {
			return s, true
		}
	}
	return Set{}, false
}

// Names returns the set's skills' names, in the order a person meets them.
func (s Set) Names() ([]string, error) {
	entries, err := fs.ReadDir(embedded, s.dir)
	if err != nil {
		return nil, fmt.Errorf("built-in skills: %w", err)
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() {
			out = append(out, e.Name())
		}
	}
	rank := func(n string) int {
		if i := slices.Index(s.order, n); i >= 0 {
			return i
		}
		return len(s.order)
	}
	slices.SortStableFunc(out, func(a, b string) int { return cmp.Compare(rank(a), rank(b)) })
	return out, nil
}

// Files returns the files of the skill name, keyed by path in the skill
// folder, SKILL.md included, with the set's assets added.
func (s Set) Files(name string) (map[string][]byte, error) {
	dir := path.Join(s.dir, name)
	files := map[string][]byte{}
	err := fs.WalkDir(embedded, dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := fs.ReadFile(embedded, p)
		if err != nil {
			return err
		}
		files[strings.TrimPrefix(p, dir+"/")] = data
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("built-in skill %s: %w", name, err)
	}
	if s.assets != nil {
		extra, err := s.assets(name)
		if err != nil {
			return nil, fmt.Errorf("built-in skill %s: %w", name, err)
		}
		maps.Copy(files, extra)
	}
	return files, nil
}

// hostAssets gives the provision skill the deploy files, under assets/, and
// the setup skill the settings template.
func hostAssets(skill string) (map[string][]byte, error) {
	var names []string
	switch skill {
	case "loadout-host-provision":
		err := fs.WalkDir(deploy.FS, ".", func(p string, d fs.DirEntry, err error) error {
			if err == nil && !d.IsDir() {
				names = append(names, p)
			}
			return err
		})
		if err != nil {
			return nil, err
		}
	case "loadout-host-setup":
		names = []string{"loadout.env.example"}
	}
	out := make(map[string][]byte, len(names))
	for _, n := range names {
		data, err := deploy.FS.ReadFile(n)
		if err != nil {
			return nil, err
		}
		out["assets/"+n] = data
	}
	return out, nil
}
