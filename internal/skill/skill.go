// Package skill defines the skill format loadout shares: a folder with a
// SKILL.md and optional supporting files (references, scripts, assets), as
// read by Claude Code, GitHub Copilot and every other tool that follows the
// Agent Skills format.
//
// SKILL.md is Markdown with YAML frontmatter:
//
//	---
//	name: release-notes
//	description: Write release notes from merged PRs. Use when asked for release notes.
//	---
//	Collect the merged PRs since the last tag, then ...
//
// loadout validates name, description and license. Other fields, such as
// allowed-tools, metadata, compatibility or disable-model-invocation, belong
// to the Agent Skills format or to one tool; they are kept verbatim and in
// order, after the fields loadout knows, so no tool loses a setting.
package skill

import (
	"bytes"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"unicode/utf8"

	"go.yaml.in/yaml/v3"

	"github.com/petervlad/loadout/internal/frontmatter"
	"github.com/petervlad/loadout/internal/ident"
)

// File is the skill's main file, at the top of its folder.
const File = "SKILL.md"

// MaxDescription is the longest description, in characters, tools accept:
// they load every skill's description at startup to decide when to use it.
const MaxDescription = 1024

// Skill is one skill in canonical form. It is immutable once parsed.
type Skill struct {
	Name        string
	Description string
	// License is optional, as in the Agent Skills format.
	License string
	// Body is the Markdown after the frontmatter.
	Body string
	// files are the supporting files, keyed by slash-separated path
	// relative to the skill folder; SKILL.md is not among them.
	files map[string][]byte
	// extra is the YAML of the frontmatter fields loadout does not know,
	// in their original order, or nil.
	extra []byte
	// extraKeys names the fields in extra.
	extraKeys []string
}

// header is the on-disk shape of the frontmatter. Field order is the
// output order.
type header struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
	License     string `yaml:"license,omitempty"`
}

// New returns a validated skill. files are the supporting files; they are
// copied.
func New(name, description, license, body string, files map[string][]byte) (*Skill, error) {
	s := &Skill{
		Name:        name,
		Description: strings.TrimSpace(description),
		License:     strings.TrimSpace(license),
		Body:        strings.TrimSpace(body),
		files:       cloneFiles(files),
	}
	if err := s.validate(); err != nil {
		return nil, err
	}
	return s, nil
}

// Parse reads a SKILL.md and the skill's other files. Frontmatter fields
// other than name, description and license are kept; see Extra.
func Parse(main []byte, files map[string][]byte) (*Skill, error) {
	meta, body, err := frontmatter.Split(main)
	if err != nil {
		return nil, err
	}
	h, extra, err := parseHeader(meta)
	if err != nil {
		return nil, fmt.Errorf("parse frontmatter: %w", err)
	}
	s, err := New(h.Name, h.Description, h.License, string(body), files)
	if err != nil {
		return nil, err
	}
	if len(extra.Content) > 0 {
		if s.extra, err = marshalYAML(extra); err != nil {
			return nil, fmt.Errorf("parse frontmatter: %w", err)
		}
		for i := 0; i < len(extra.Content); i += 2 {
			s.extraKeys = append(s.extraKeys, extra.Content[i].Value)
		}
	}
	return s, nil
}

// knownFields are the frontmatter fields loadout reads into header.
var knownFields = []string{"name", "description", "license"}

// parseHeader splits the frontmatter into the fields loadout knows and a
// mapping of the others, in their order.
func parseHeader(meta []byte) (header, *yaml.Node, error) {
	fields, err := frontmatter.Fields(meta)
	if err != nil {
		return header{}, nil, err
	}
	known := &yaml.Node{Kind: yaml.MappingNode}
	extra := &yaml.Node{Kind: yaml.MappingNode}
	for i := 0; i+1 < len(fields); i += 2 {
		k, v := fields[i], fields[i+1]
		if slices.Contains(knownFields, k.Value) {
			known.Content = append(known.Content, k, v)
			continue
		}
		// An alias could point at a field that is written apart from
		// this one, and would no longer resolve.
		if hasAlias(v) {
			return header{}, nil, fmt.Errorf("line %d: field %q uses a YAML alias (*name); write the value out", k.Line, k.Value)
		}
		extra.Content = append(extra.Content, k, v)
	}
	var h header
	if err := known.Decode(&h); err != nil {
		return header{}, nil, err
	}
	return h, extra, nil
}

func hasAlias(n *yaml.Node) bool {
	if n.Kind == yaml.AliasNode {
		return true
	}
	return slices.ContainsFunc(n.Content, hasAlias)
}

// marshalYAML writes v with two-space indents, as people write frontmatter.
func marshalYAML(v any) ([]byte, error) {
	var b bytes.Buffer
	enc := yaml.NewEncoder(&b)
	enc.SetIndent(2)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	if err := enc.Close(); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

// Extra returns the names of the frontmatter fields loadout keeps without
// reading them, in their order.
func (s *Skill) Extra() []string { return slices.Clone(s.extraKeys) }

func (s *Skill) validate() error {
	var errs []error
	if err := ident.Check("skill", s.Name); err != nil {
		errs = append(errs, err)
	}
	switch {
	case s.Description == "":
		errs = append(errs, errors.New("description is empty: tools read it to decide when to use the skill"))
	case utf8.RuneCountInString(s.Description) > MaxDescription:
		errs = append(errs, fmt.Errorf("description is %d characters, the limit is %d", utf8.RuneCountInString(s.Description), MaxDescription))
	}
	if s.Body == "" {
		errs = append(errs, errors.New("the instructions (the text after the frontmatter) are empty"))
	}
	if _, ok := s.files[File]; ok {
		errs = append(errs, fmt.Errorf("%s is the main file, not a supporting file", File))
	}
	return errors.Join(errs...)
}

// Files returns a copy of the supporting files.
func (s *Skill) Files() map[string][]byte { return cloneFiles(s.files) }

// Renamed returns a copy of s under another name, for side-by-side reviews.
func (s *Skill) Renamed(name string) (*Skill, error) {
	r, err := New(name, s.Description, s.License, s.Body, s.files)
	if err != nil {
		return nil, err
	}
	r.extra, r.extraKeys = bytes.Clone(s.extra), slices.Clone(s.extraKeys)
	return r, nil
}

// Marshal writes SKILL.md in canonical form, with note (if any) as a line
// between the frontmatter and the body. The fields loadout knows come
// first, then the others as they were. Parse(Marshal("")) returns s.
func (s *Skill) Marshal(note string) ([]byte, error) {
	meta, err := yaml.Marshal(header{Name: s.Name, Description: s.Description, License: s.License})
	if err != nil {
		return nil, fmt.Errorf("marshal %s: %w", s.Name, err)
	}
	meta = append(meta, s.extra...)
	body := s.Body
	if note != "" {
		body = "\n" + note + "\n\n" + body
	}
	return frontmatter.Join(meta, body), nil
}

func cloneFiles(files map[string][]byte) map[string][]byte {
	out := make(map[string][]byte, len(files))
	for k, v := range maps.All(files) {
		out[k] = append([]byte(nil), v...)
	}
	return out
}
