package buildinfo

import (
	"net/url"
	"path/filepath"
	"strings"
)

// RegistryPlaceholder stands for the registry's address in the command of
// a method that installs from a registry; For fills it in.
const RegistryPlaceholder = "{registry}"

// UnknownRegistry is what For writes into a command when it has no usable
// registry address.
const UnknownRegistry = "<the address of your registry>"

// Method is how this binary was installed, which decides how to update it.
// The texts are loadout's own, never something a registry sent.
type Method struct {
	// Name is "registry", "homebrew", "release" or "source".
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
		// Chosen when the binary is built (deploy.sh), because only a
		// registry's own download is meant to be updated from it.
		method:  Method{Name: "registry", Command: "curl -fsSL " + RegistryPlaceholder + "/install.sh | sh"},
		matches: func(in Install) bool { return in.Channel == ChannelRegistry },
	},
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
	// Channel is set at build time for binaries a registry hands out
	// (-X main.installMethod=registry).
	Channel string
}

// ChannelRegistry is the Channel of a CLI downloaded from a registry.
const ChannelRegistry = "registry"

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

// For returns m with the registry it should update from filled in: the
// address as the person's own config writes it, cut down to scheme and
// host. A method that names no registry is returned as it is; an address
// that is not a plain http(s) one gives a command that says what is
// missing, so an odd config value never lands in a command to paste.
func (m Method) For(registry string) Method {
	if !strings.Contains(m.Command, RegistryPlaceholder) {
		return m
	}
	base := UnknownRegistry
	if u, err := url.Parse(registry); err == nil && (u.Scheme == "https" || u.Scheme == "http") && safeHost(u.Host) {
		base = u.Scheme + "://" + u.Host
	}
	m.Command = strings.ReplaceAll(m.Command, RegistryPlaceholder, base)
	return m
}

// safeHost accepts what a host and port are made of, so the address can
// sit in a shell command unquoted.
func safeHost(h string) bool {
	if h == "" {
		return false
	}
	for _, r := range h {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '.', r == '-', r == ':':
		default:
			return false
		}
	}
	return true
}

// Update is the sentence that tells a person how to update: "To update,
// in your loadout folder: git pull && go install ./cmd/loadout".
func (m Method) Update() string {
	if m.InFolder {
		return "To update, in your loadout folder: " + m.Command
	}
	return "To update: " + m.Command
}
