// Package pack reads and creates loadout packages.
//
// A package is a folder with a manifest, loadout.yaml (id, kind, version,
// description, targets), and the content of its kind (see kinds.go):
//
//	agent     agent.md, the agent in canonical form (package agent)
//	skill     SKILL.md and optional supporting files in folders (package skill)
//	kb        documents in folders, installed as a skill (package kb)
//	pipeline  pipeline.yaml, a template of slots or a team's binding (package pipeline)
//
// Files are UTF-8 text and may sit in folders; CheckPath says which paths
// are allowed.
package pack

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"slices"
	"strings"

	"github.com/petervlad/loadout/internal/agent"
	"github.com/petervlad/loadout/internal/kb"
	"github.com/petervlad/loadout/internal/pipeline"
	"github.com/petervlad/loadout/internal/semver"
	"github.com/petervlad/loadout/internal/skill"
	"github.com/petervlad/loadout/internal/strictyaml"
)

// File names inside a package folder.
const (
	ManifestFile = "loadout.yaml"
	AgentFile    = "agent.md"
	PipelineFile = "pipeline.yaml"
)

// Manifest is the content of loadout.yaml.
type Manifest struct {
	ID          ID             `yaml:"name"`
	Kind        Kind           `yaml:"kind"`
	Version     semver.Version `yaml:"version"`
	Description string         `yaml:"description"`
	// Targets lists the AI tools the package supports ("claude", "copilot").
	// Empty means every tool loadout has an adapter for.
	Targets []string `yaml:"targets,omitempty,flow"`
}

// Pack is a loaded, validated package. It is immutable.
type Pack struct {
	manifest Manifest
	files    map[string][]byte
	digest   Digest
	// The parsed content; which one is set depends on the kind. A
	// knowledge base sets kb and skill (the skill it installs as).
	agent *agent.Definition
	skill *skill.Skill
	kb    *kb.Base
	// pipeline is a *pipeline.Template or *pipeline.Binding.
	pipeline pipeline.Package
}

// Manifest returns the package manifest.
func (p *Pack) Manifest() Manifest {
	m := p.manifest
	m.Targets = slices.Clone(m.Targets)
	return m
}

// ID returns the package id.
func (p *Pack) ID() ID { return p.manifest.ID }

// Kind returns what the package contains.
func (p *Pack) Kind() Kind { return p.manifest.Kind }

// Version returns the package version.
func (p *Pack) Version() semver.Version { return p.manifest.Version }

// Agent returns the agent of an agent package.
func (p *Pack) Agent() (agent.Definition, bool) {
	if p.agent == nil {
		return agent.Definition{}, false
	}
	d := *p.agent
	d.Tools = slices.Clone(d.Tools)
	return d, true
}

// Skill returns the skill a skill or knowledge base package installs as.
// The skill is immutable, so it is shared.
func (p *Pack) Skill() (*skill.Skill, bool) { return p.skill, p.skill != nil }

// KB returns the knowledge base of a kb package.
func (p *Pack) KB() (*kb.Base, bool) { return p.kb, p.kb != nil }

// Pipeline returns the template or binding of a pipeline package. It is
// immutable, so it is shared.
func (p *Pack) Pipeline() (pipeline.Package, bool) { return p.pipeline, p.pipeline != nil }

// Digest returns the content digest.
func (p *Pack) Digest() Digest { return p.digest }

// Files returns a copy of the package files keyed by slash-separated path.
func (p *Pack) Files() map[string][]byte {
	out := make(map[string][]byte, len(p.files))
	for k, v := range p.files {
		out[k] = bytes.Clone(v)
	}
	return out
}

// Paths returns the file paths in reading order: the manifest, the main
// file, then the rest sorted.
func (p *Pack) Paths() []string {
	first := []string{ManifestFile}
	if main := p.Kind().MainFile(); main != "" {
		first = append(first, main)
	}
	out := slices.Clone(first)
	for _, path := range slices.Sorted(maps.Keys(p.files)) {
		if !slices.Contains(first, path) {
			out = append(out, path)
		}
	}
	return out
}

// Main returns the name and content of the file that says what the package
// does: agent.md, SKILL.md, or for a knowledge base the index loadout
// generates.
func (p *Pack) Main() (string, []byte, error) {
	if main := p.Kind().MainFile(); main != "" {
		return main, bytes.Clone(p.files[main]), nil
	}
	if p.skill != nil {
		data, err := p.skill.Marshal("")
		return skill.File, data, err
	}
	return "", nil, fmt.Errorf("%s has no main file", p.ID())
}

// Load reads and validates the package in dir, with its folders.
func Load(dir string) (*Pack, error) {
	files, err := ReadDir(dir)
	if err != nil {
		return nil, err
	}
	p, err := FromFiles(files)
	if err != nil {
		return nil, fmt.Errorf("package %s: %w", dir, err)
	}
	return p, nil
}

// FromFiles builds a package from file contents keyed by slash-separated
// path. It is how a package is rebuilt from a downloaded bundle as well as
// from a folder, both of which stop at MaxLimits while reading. The
// package must fit the limits of its kind.
func FromFiles(files map[string][]byte) (*Pack, error) {
	if err := checkFiles(files); err != nil {
		return nil, err
	}
	manifestData, ok := files[ManifestFile]
	if !ok {
		return nil, fmt.Errorf("missing %s", ManifestFile)
	}
	m, err := parseManifest(manifestData)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", ManifestFile, err)
	}
	if err := m.Kind.Limits().check(files, article(m.Kind.Describe())); err != nil {
		return nil, err
	}

	owned := make(map[string][]byte, len(files))
	for k, v := range files {
		owned[k] = bytes.Clone(v)
	}
	content := maps.Clone(owned)
	delete(content, ManifestFile)
	p := &Pack{manifest: m, files: owned, digest: digestOf(owned)}
	if err := kinds[m.Kind].parse(p, content); err != nil {
		return nil, err
	}
	return p, nil
}

func parseManifest(data []byte) (Manifest, error) {
	var m Manifest
	if err := strictyaml.Decode(data, &m); err != nil {
		if errors.Is(err, strictyaml.ErrEmpty) {
			return Manifest{}, errors.New("file is empty")
		}
		return Manifest{}, err
	}

	var errs []error
	if m.ID.IsZero() {
		errs = append(errs, errors.New("name is missing (want team/name)"))
	}
	if m.Version == (semver.Version{}) {
		errs = append(errs, errors.New("version is missing or 0.0.0 (start at 0.1.0)"))
	}
	if !m.Kind.Valid() {
		errs = append(errs, fmt.Errorf("kind %q is not supported: want %s", m.Kind, joinKinds()))
	}
	if strings.TrimSpace(m.Description) == "" {
		errs = append(errs, errors.New("description is empty"))
	}
	return m, errors.Join(errs...)
}

// article puts "a" or "an" before a noun: "an agent", "a skill".
func article(noun string) string {
	if strings.ContainsRune("aeiou", rune(noun[0])) {
		return "an " + noun
	}
	return "a " + noun
}

func joinKinds() string {
	names := make([]string, 0, len(kinds))
	for _, k := range Kinds() {
		names = append(names, string(k))
	}
	return strings.Join(names, ", ")
}

// ReadDir reads the files under dir the way Load does, keyed by
// slash-separated path, without validating them as a package.
func ReadDir(dir string) (map[string][]byte, error) {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, fmt.Errorf("open package %s: %w", dir, err)
	}
	defer root.Close()

	files, err := readFiles(root, MaxLimits())
	if err != nil {
		return nil, fmt.Errorf("read package %s: %w", dir, err)
	}
	return files, nil
}

// readFiles reads the regular files under root, skipping hidden files and
// folders such as .git or .DS_Store. It stops at limits, so a wrong folder
// (a home directory) fails fast. Load passes MaxLimits, since the kind is
// not known yet; FromFiles then applies the kind's own.
func readFiles(root *os.Root, limits Limits) (map[string][]byte, error) {
	files := map[string][]byte{}
	var total int64
	err := fs.WalkDir(root.FS(), ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if path == "." {
			return nil
		}
		if strings.HasPrefix(d.Name(), ".") {
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			return nil
		}
		if d.Type()&fs.ModeSymlink != 0 {
			return fmt.Errorf("%q is a symbolic link; a package holds regular files only, so copy the file in", path)
		}
		if !d.Type().IsRegular() {
			return fmt.Errorf("%q is not a regular file", path)
		}
		if len(files) == limits.MaxFiles {
			return fmt.Errorf("%w: more than %d files, the most any package may have", ErrTooLarge, limits.MaxFiles)
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if info.Size() > int64(limits.MaxFileSize) {
			return fmt.Errorf("%w: %s is %d bytes, the limit per file for any package is %d", ErrTooLarge, path, info.Size(), limits.MaxFileSize)
		}
		if total += info.Size(); total > int64(limits.MaxTotal) {
			return fmt.Errorf("%w: over %d bytes in all, the most any package may hold", ErrTooLarge, limits.MaxTotal)
		}
		data, err := root.ReadFile(path)
		if err != nil {
			return err
		}
		files[path] = data
		return nil
	})
	if err != nil {
		return nil, err
	}
	return files, nil
}
