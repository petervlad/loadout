package install

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// Wanted is a package the registry offers, by the name its files carry on
// disk: an agent is NAME.md, a skill or knowledge base is the folder NAME.
type Wanted struct {
	Name  string
	Agent bool
}

// Legacy is an agent, skill or knowledge base an older setup left that has
// the name of a package the registry offers, and that loadout did not write.
type Legacy struct {
	Path string
	// LinkTo is where it points when it is a symlink, empty otherwise.
	LinkTo string
}

// legacyFolders are the folders each tool reads below the user's home. The
// shared ~/.agents folder is read by Copilot and VS Code, not by Claude
// Code, so it is only scanned for them.
var legacyFolders = map[string][]string{
	"copilot": {".copilot", ".agents"},
	"claude":  {".claude"},
	// VS Code reads all three: an old copy in ~/.claude lists an agent
	// twice, and ~/.agents/skills is read before ~/.copilot/skills.
	"vscode": {".copilot", ".agents", ".claude"},
}

// FindLegacy returns the entries below root, in the folders the given tools
// read, that match a wanted package and are not managed by loadout. An
// entry is managed when its file carries loadout's marker; a symlink never
// does, because loadout never writes through one.
func FindLegacy(root string, wanted []Wanted, tools []string) ([]Legacy, error) {
	var out []Legacy
	seen := map[string]bool{}
	for _, tool := range tools {
		for _, base := range legacyFolders[tool] {
			for _, w := range wanted {
				for _, p := range candidates(root, base, w) {
					if seen[p] {
						continue
					}
					seen[p] = true
					l, ok, err := legacyAt(p)
					if err != nil {
						return nil, err
					}
					if ok {
						out = append(out, l)
					}
				}
			}
		}
	}
	slices.SortFunc(out, func(a, b Legacy) int { return strings.Compare(a.Path, b.Path) })
	return out, nil
}

func candidates(root, base string, w Wanted) []string {
	if w.Agent {
		return []string{
			filepath.Join(root, base, "agents", w.Name+".md"),
			filepath.Join(root, base, "agents", w.Name+".agent.md"),
		}
	}
	return []string{filepath.Join(root, base, "skills", w.Name)}
}

func legacyAt(p string) (Legacy, bool, error) {
	info, err := os.Lstat(p)
	if os.IsNotExist(err) {
		return Legacy{}, false, nil
	}
	if err != nil {
		return Legacy{}, false, fmt.Errorf("look at %s: %w", p, err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		to, _ := os.Readlink(p)
		return Legacy{Path: p, LinkTo: to}, true, nil
	}
	file := p
	if info.IsDir() {
		file = filepath.Join(p, "SKILL.md")
	}
	data, err := os.ReadFile(file)
	if errors.Is(err, fs.ErrNotExist) {
		return Legacy{Path: p}, true, nil // no file inside: not ours
	}
	if err != nil {
		return Legacy{}, false, fmt.Errorf("read %s: %w", file, err)
	}
	if strings.Contains(string(data), "managed by loadout:") {
		return Legacy{}, false, nil
	}
	return Legacy{Path: p}, true, nil
}

// Backup moves path, a file, folder or link below root, into
// ~/.loadout/backup/<stamp>/ and returns where it went. Nothing is deleted.
func Backup(root, path string) (string, error) {
	to, err := backupPath(root, path)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(to), 0o700); err != nil {
		return "", fmt.Errorf("make backup folder: %w", err)
	}
	if err := os.Rename(path, to); err != nil {
		return "", fmt.Errorf("move %s aside: %w", path, err)
	}
	return to, nil
}

// FindDuplicates returns the copies, in the folders the given tools read,
// that loadout did not write and whose name also exists in another of those
// folders. It keeps one copy of each name (a loadout one if there is one,
// else the first folder in the tool's order) and returns the rest, so the
// tool lists each agent or skill once. Names no registry package has are
// covered too, which FindLegacy cannot do.
func FindDuplicates(root string, tools []string) ([]Legacy, error) {
	type copyAt struct {
		path    string
		managed bool
		order   int
	}
	groups := map[string][]copyAt{}
	order := 0
	seenBase := map[string]bool{}
	for _, tool := range tools {
		for _, base := range legacyFolders[tool] {
			if seenBase[base] {
				continue
			}
			seenBase[base] = true
			order++
			for _, kind := range []string{"agents", "skills"} {
				dir := filepath.Join(root, base, kind)
				entries, err := os.ReadDir(dir)
				if os.IsNotExist(err) {
					continue
				}
				if err != nil {
					return nil, fmt.Errorf("read %s: %w", dir, err)
				}
				for _, e := range entries {
					name := e.Name()
					if strings.HasPrefix(name, ".") || name == "README.md" {
						continue
					}
					if kind == "agents" {
						if !strings.HasSuffix(name, ".md") {
							continue
						}
						name = strings.TrimSuffix(strings.TrimSuffix(name, ".md"), ".agent")
					}
					p := filepath.Join(dir, e.Name())
					_, unmanaged, err := legacyAt(p)
					if err != nil {
						return nil, err
					}
					key := kind + "/" + name
					groups[key] = append(groups[key], copyAt{path: p, managed: !unmanaged, order: order})
				}
			}
		}
	}
	var out []Legacy
	for _, g := range groups {
		if len(g) < 2 {
			continue
		}
		keep := 0
		for i, c := range g {
			if c.managed && !g[keep].managed || c.managed == g[keep].managed && c.order < g[keep].order {
				keep = i
			}
		}
		for i, c := range g {
			if i == keep || c.managed {
				continue
			}
			l := Legacy{Path: c.path}
			if to, err := os.Readlink(c.path); err == nil {
				l.LinkTo = to
			}
			out = append(out, l)
		}
	}
	slices.SortFunc(out, func(a, b Legacy) int { return strings.Compare(a.Path, b.Path) })
	return out, nil
}
