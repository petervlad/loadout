package buildinfo

import (
	"path/filepath"
	"strings"
)

// Method is how this binary was installed, which decides how to update it.
// The texts are loadout's own, never something a registry sent.
type Method struct {
	// Name is "homebrew", "release" or "source".
	Name string
	// Command is what updates it: a command to run, or where to download
	// it.
	Command string
	// InFolder says the command runs in the person's loadout folder (the
	// clone they built it from), which loadout does not know.
	InFolder bool
}

// methods is the table MethodOf reads, first match wins; a new way to
// install loadout is a row.
var methods = []struct {
	method  Method
	matches func(Install) bool
}{
	{
		method:  Method{Name: "homebrew", Command: "brew upgrade --cask loadout"},
		matches: func(in Install) bool { return underHomebrew(in.Executable) },
	},
	{
		method:  Method{Name: "release", Command: "download the new release from https://loadout.lat/docs/#1-install-the-cli"},
		matches: func(in Install) bool { return in.Packaged && in.Build.Exact() },
	},
	{
		method:  Method{Name: "source", Command: "git pull && go install ./cmd/loadout", InFolder: true},
		matches: func(Install) bool { return true },
	},
}

// Install is what says how a binary was installed.
type Install struct {
	Build Build
	// Executable is the running binary's path, links resolved.
	Executable string
	// Packaged is set by the release pipeline (GoReleaser's ldflags), so a
	// make build of a tag is not mistaken for a download.
	Packaged bool
}

// MethodOf picks how a binary was installed: under a Homebrew Caskroom or
// Cellar, a release download, else a build from source.
func MethodOf(in Install) Method {
	for _, row := range methods {
		if row.matches(in) {
			return row.method
		}
	}
	return methods[len(methods)-1].method
}

func underHomebrew(exe string) bool {
	p := filepath.ToSlash(exe)
	return strings.Contains(p, "/Caskroom/") || strings.Contains(p, "/Cellar/")
}

// Update is the sentence that tells a person how to update: "To update,
// in your loadout folder: git pull && go install ./cmd/loadout".
func (m Method) Update() string {
	if m.InFolder {
		return "To update, in your loadout folder: " + m.Command
	}
	return "To update: " + m.Command
}
