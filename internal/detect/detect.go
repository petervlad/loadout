// Package detect finds the AI tools a person has, so agents go only into
// tools they use.
package detect

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/petervlad/loadout/internal/target"
)

// Tool is an AI tool that names the folder whose presence shows it is used.
type Tool interface {
	Name() string
	// MarkerDir is the folder, relative to the scope root, that exists when
	// the tool is used in that kind of scope.
	MarkerDir(kind target.ScopeKind) string
}

// ErrNoTool is returned when no marker folder of any tool asked about
// exists in the scope.
var ErrNoTool = errors.New("no supported AI tool found")

// noToolError is ErrNoTool with a message that says where loadout looked.
type noToolError string

func (e noToolError) Error() string { return string(e) }

func (e noToolError) Is(target error) bool { return target == ErrNoTool }

// Detector finds which of a fixed set of tools are present.
type Detector struct {
	tools []Tool
}

// New returns a Detector for tools.
func New(tools ...Tool) *Detector {
	tools = slices.Clone(tools)
	slices.SortFunc(tools, func(a, b Tool) int { return strings.Compare(a.Name(), b.Name()) })
	return &Detector{tools: tools}
}

// Targets returns the names of the tools whose marker folder exists in
// scope, limited to supported (empty means any), sorted by name. It is an
// error when none is found.
func (d *Detector) Targets(scope target.Scope, supported []string) ([]string, error) {
	var found []string
	var looked []string
	for _, t := range d.tools {
		if len(supported) > 0 && !slices.Contains(supported, t.Name()) {
			continue
		}
		dir := t.MarkerDir(scope.Kind)
		looked = append(looked, display(scope, dir))
		if info, err := os.Stat(filepath.Join(scope.Root, filepath.FromSlash(dir))); err == nil && info.IsDir() {
			found = append(found, t.Name())
		}
	}
	if len(found) > 0 {
		return found, nil
	}
	if len(supported) > 0 {
		return nil, noToolError(fmt.Sprintf("this package is for %s, which is not installed here (no %s); use --target to install anyway", strings.Join(supported, ", "), strings.Join(looked, ", ")))
	}
	return nil, noToolError(fmt.Sprintf("no supported AI tool found (looked for %s); use --target to install anyway", strings.Join(looked, ", ")))
}

// display shows a marker folder the way the person knows it: under ~ for
// user scope, as a full path otherwise.
func display(scope target.Scope, dir string) string {
	if scope.Kind == target.ScopeUser {
		return "~/" + dir
	}
	return filepath.Join(scope.Root, filepath.FromSlash(dir))
}
