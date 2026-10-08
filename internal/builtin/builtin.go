// Package builtin holds the skills that teach a person's AI agent to use
// loadout, shipped inside the binary so their text always matches the
// commands of the CLI that installs them.
//
// Each skill is a normal skill package (kind skill, id loadout/NAME) built
// from the embedded files, so it is validated, rendered, installed, listed
// by status and removed by uninstall like any other. Its lock entry has
// source "builtin" and no remote: the registry pass of sync skips it, and
// Service keeps it in step with the CLI instead.
package builtin

import (
	"embed"
	"fmt"
	"io/fs"

	"github.com/petervlad/loadout/internal/buildinfo"
	"github.com/petervlad/loadout/internal/pack"
	"github.com/petervlad/loadout/internal/semver"
	"github.com/petervlad/loadout/internal/skill"
)

// Namespace is the namespace of the built-in packages' ids.
const Namespace = "loadout"

// Source is what the lock records as the source of a built-in install.
const Source = "builtin"

//go:embed skills host
var embedded embed.FS

// FS returns the embedded skills: one folder per skill under "skills".
// The web pages read the text from here, so they show what this build
// installs.
func FS() fs.FS { return embedded }

// Names returns the everyday skills' names, in the order a person meets
// them.
func Names() ([]string, error) { return everyday.Names() }

// Packs returns the everyday built-in skills as packages at the CLI
// version cliVersion, in the order a person meets them. The web pages and
// login use this set; PacksOf builds any set.
func Packs(cliVersion string) ([]*pack.Pack, error) { return PacksOf(everyday, cliVersion) }

// PacksOf returns the skills of set as packages at the CLI version
// cliVersion.
func PacksOf(set Set, cliVersion string) ([]*pack.Pack, error) {
	names, err := set.Names()
	if err != nil {
		return nil, err
	}
	v := Version(cliVersion)
	out := make([]*pack.Pack, 0, len(names))
	for _, name := range names {
		p, err := packOf(set, name, v)
		if err != nil {
			return nil, fmt.Errorf("built-in skill %s: %w", name, err)
		}
		out = append(out, p)
	}
	return out, nil
}

// Version is the package version of the built-in skills for a CLI
// version, the release it is or was built after (buildinfo.Parse):
// "v1.4.0", "1.4.0-3-gabc" and a Go pseudo-version after 1.4.0 are 1.4.0.
// A build without a release ("dev", a commit hash) gets 0.0.1; the digest
// still changes with the text, which is what sync compares.
func Version(cliVersion string) semver.Version {
	if v, ok := buildinfo.Parse(cliVersion).Release(); ok {
		return v
	}
	return semver.New(0, 0, 1)
}

// packOf builds the package of one skill folder, with a manifest whose
// description is the skill's own.
func packOf(set Set, name string, v semver.Version) (*pack.Pack, error) {
	files, err := set.Files(name)
	if err != nil {
		return nil, err
	}
	main := files[skill.File]
	delete(files, skill.File)
	s, err := skill.Parse(main, files)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", skill.File, err)
	}
	id, err := pack.NewID(Namespace, name)
	if err != nil {
		return nil, err
	}
	manifest, err := pack.MarshalManifest(pack.Manifest{ID: id, Kind: pack.KindSkill, Version: v, Description: s.Description})
	if err != nil {
		return nil, err
	}
	files[skill.File] = main
	files[pack.ManifestFile] = manifest
	return pack.FromFiles(files)
}

// Files returns the files of the everyday built-in skill name, keyed by
// path in the skill folder, SKILL.md included.
func Files(name string) (map[string][]byte, error) { return everyday.Files(name) }
