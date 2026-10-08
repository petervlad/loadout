// Package importer turns agents, skills and documents written for an AI
// tool into loadout package folders. It only writes folders; publishing
// them is a separate, deliberate step.
//
// Every import converts all its inputs before it writes anything, and
// every folder it writes is loaded back with pack.Load, so a folder it
// reports is one publish accepts.
package importer

import (
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/petervlad/loadout/internal/pack"
	"github.com/petervlad/loadout/internal/semver"
)

// Options are the settings every import takes.
type Options struct {
	// Team is the namespace the packages belong to.
	Team string
	// Out is the folder the package folders are created in, one per
	// package, named after it.
	Out string
	// Version is the first version; zero means 0.1.0.
	Version semver.Version
	// Name replaces the package name the input gives, for an import of
	// one input only.
	Name string
}

// ErrNameForMany is returned when Options.Name is set for several inputs.
var ErrNameForMany = errors.New("a package name can be given only when importing one input")

// checkName refuses Options.Name for more than one input.
func (o Options) checkName(inputs int) error {
	if o.Name != "" && inputs > 1 {
		return fmt.Errorf("%w, not %d", ErrNameForMany, inputs)
	}
	return nil
}

// CollisionError is returned when inputs would become a package folder
// that exists or that another input also becomes.
type CollisionError struct {
	// Sources are the inputs, one when the folder already exists.
	Sources []string
	Dir     string
}

func (e *CollisionError) Error() string {
	if len(e.Sources) == 1 {
		return fmt.Sprintf("%s would become %s, which already exists", e.Sources[0], e.Dir)
	}
	all := "all"
	if len(e.Sources) == 2 {
		all = "both"
	}
	return fmt.Sprintf("%s would %s become %s", strings.Join(e.Sources, " and "), all, e.Dir)
}

// Result is one package folder an import wrote.
type Result struct {
	// Source is the file or folder it came from.
	Source string
	// Dir is the package folder.
	Dir  string
	ID   pack.ID
	Kind pack.Kind
	// Notices say what the import changed or left out, for the author to
	// check before publishing.
	Notices []string
}

// plan is one package folder to write.
type plan struct {
	result Result
	files  map[string][]byte
}

// plan returns the package folder for content, validated but not written.
// content must not hold a manifest; plan writes it.
func (o Options) plan(source, name string, kind pack.Kind, description string, content map[string][]byte, notices []string) (plan, error) {
	id, err := pack.NewID(o.Team, name)
	if err != nil {
		return plan{}, err
	}
	version := o.Version
	if version == (semver.Version{}) {
		version = semver.New(0, 1, 0)
	}
	manifest, err := pack.MarshalManifest(pack.Manifest{ID: id, Kind: kind, Version: version, Description: description})
	if err != nil {
		return plan{}, err
	}
	files := maps.Clone(content)
	files[pack.ManifestFile] = manifest
	if _, err := pack.FromFiles(files); err != nil {
		return plan{}, err
	}
	return plan{
		result: Result{Source: source, Dir: filepath.Join(o.Out, name), ID: id, Kind: kind, Notices: notices},
		files:  files,
	}, nil
}

// write creates every planned folder. It refuses before writing any of
// them if one exists or two would be the same.
func write(plans []plan) ([]Result, error) {
	sources := map[string][]string{}
	var dirs []string
	for _, p := range plans {
		dir := p.result.Dir
		if _, ok := sources[dir]; !ok {
			dirs = append(dirs, dir)
		}
		sources[dir] = append(sources[dir], p.result.Source)
	}
	var errs []error
	for _, dir := range dirs {
		if len(sources[dir]) > 1 {
			errs = append(errs, &CollisionError{Sources: sources[dir], Dir: dir})
		} else if _, err := os.Lstat(dir); err == nil {
			errs = append(errs, &CollisionError{Sources: sources[dir], Dir: dir})
		}
	}
	if err := errors.Join(errs...); err != nil {
		return nil, err
	}

	results := make([]Result, 0, len(plans))
	for _, p := range plans {
		if _, err := pack.Create(p.result.Dir, p.files); err != nil {
			return results, fmt.Errorf("%s: %w", p.result.Source, err)
		}
		results = append(results, p.result)
	}
	return results, nil
}

// maxSummary is the longest package description an import derives, in
// characters: loadout list shows it on one line.
const maxSummary = 200

// summary is the first sentence of desc, on one line and at most
// maxSummary characters, for a package description.
func summary(desc string) string {
	s := strings.Join(strings.Fields(desc), " ")
	if i := sentenceEnd(s); i > 0 {
		s = s[:i]
	}
	if utf8.RuneCountInString(s) <= maxSummary {
		return s
	}
	cut := string([]rune(s)[:maxSummary-1])
	if i := strings.LastIndexByte(cut, ' '); i > maxSummary/2 {
		cut = cut[:i]
	}
	return strings.TrimRightFunc(cut, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsPunct(r) }) + "…"
}

// abbreviations end in a full stop that does not end a sentence.
var abbreviations = []string{"e.g", "i.e", "etc", "vs", "cf"}

// sentenceEnd returns the length of the first sentence of s, or 0 when s
// is one sentence.
func sentenceEnd(s string) int {
	for i := 0; i+1 < len(s); i++ {
		if !strings.ContainsRune(".!?", rune(s[i])) || s[i+1] != ' ' {
			continue
		}
		word := s[strings.LastIndexByte(s[:i], ' ')+1 : i]
		abbrev := false
		for _, a := range abbreviations {
			if strings.EqualFold(word, a) {
				abbrev = true
			}
		}
		if !abbrev {
			return i + 1
		}
	}
	return 0
}
