package install

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// adoptable says a conflict is something an older setup left, which adopt
// may move aside. A file loadout wrote and someone edited is not: that is
// the user's work and needs --force.
func adoptable(r Reason) bool {
	return r == ReasonUnmanaged || r == ReasonSymlink || r == ReasonLinkedFolder
}

// adopt moves the adoptable conflicts, and the same-named copies Copilot
// would also read from ~/.agents, into a backup folder. It returns the
// conflicts that remain. Moving a link moves the link, never its target.
func (in *Installer) adopt(files tree, write []renderedFile, conflicts []Conflict) ([]Conflict, error) {
	var rest []Conflict
	var moves []string
	for _, c := range conflicts {
		if adoptable(c.Reason) {
			moves = append(moves, c.Path)
		} else {
			rest = append(rest, c)
		}
	}
	for _, rf := range write {
		if twin, ok := agentsTwin(rf.file.Path); ok && files.exists(twin) {
			moves = append(moves, files.abs(twin))
		}
	}
	for _, from := range moves {
		to, err := backupPath(files.root, from)
		if err != nil {
			return nil, err
		}
		if _, err := os.Lstat(from); err != nil {
			continue // already moved with its parent
		}
		if err := os.MkdirAll(filepath.Dir(to), 0o700); err != nil {
			return nil, fmt.Errorf("make backup folder: %w", err)
		}
		if err := os.Rename(from, to); err != nil {
			return nil, fmt.Errorf("move %s aside: %w", from, err)
		}
		in.log.Info("moved aside", "from", from, "to", to)
	}
	return rest, nil
}

// agentsTwin returns the path in ~/.agents that holds the same agent or
// skill as a Copilot file at rel, because Copilot reads both folders and
// would list the package twice.
func agentsTwin(rel string) (string, bool) {
	segs := strings.Split(rel, "/")
	if len(segs) < 3 || segs[0] != ".copilot" || (segs[1] != "agents" && segs[1] != "skills") {
		return "", false
	}
	return ".agents/" + segs[1] + "/" + segs[2], true
}

var backupStamp = time.Now().Format("20060102-150405")

// backupPath is where abs goes: ~/.loadout/backup/<stamp>/ plus its path
// below root. One stamp per process keeps one run's moves together.
func backupPath(root, abs string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("find home folder: %w", err)
	}
	rel, err := filepath.Rel(root, abs)
	if err != nil || strings.HasPrefix(rel, "..") {
		return "", fmt.Errorf("%s is outside %s", abs, root)
	}
	return filepath.Join(home, ".loadout", "backup", backupStamp, rel), nil
}
