// Package memimport reads a folder of personal memory files and turns the
// ones fit for a team into entries to save. It decides what to offer and what
// to pre-select, refuses anything that holds a secret, and sends the rest
// through a Saver. Only MCPSaver talks to the network, and Read and Run never do.
package memimport

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"slices"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/petervlad/loadout/internal/frontmatter"
	"github.com/petervlad/loadout/internal/secretscan"
)

// Kind is the type a memory file declares.
type Kind string

// The kinds a team memory keeps. A file of type "user" is never offered.
const (
	KindFeedback  Kind = "feedback"
	KindProject   Kind = "project"
	KindReference Kind = "reference"
	kindUser      Kind = "user"
)

// maxFile caps a file read, since a memory is short and the folder is not ours.
const maxFile = 256 << 10

// indexFile is the personal index, which holds links and no memory.
const indexFile = "MEMORY.md"

// Entry is one memory file offered for import. Number is its 1-based place in
// the list the person picks from.
type Entry struct {
	Number      int
	File        string
	Name        string
	Description string
	Kind        Kind
	Body        string

	// Selected is the pre-selection and Why the rows that decided it, one
	// sentence each.
	Selected bool
	Why      []string

	// Refusal is set when a secret scan found something. Such an entry cannot
	// be selected and is never sent.
	Refusal *secretscan.Finding
}

// Skipped is a file that is not offered, with the reason in a sentence.
type Skipped struct {
	File   string
	Reason string
}

// Listing is what Read found.
type Listing struct {
	Entries []Entry
	Skipped []Skipped
}

// Read lists the *.md files in dir, not recursively, without MEMORY.md. Files
// are in name order so numbers are stable between runs.
func Read(dir string) (Listing, error) {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return Listing{}, fmt.Errorf("open memory folder: %w", err)
	}
	defer root.Close()
	files, err := fs.ReadDir(root.FS(), ".")
	if err != nil {
		return Listing{}, fmt.Errorf("list memory folder: %w", err)
	}
	var l Listing
	for _, f := range files {
		name := f.Name()
		if f.IsDir() || !strings.HasSuffix(name, ".md") || name == indexFile {
			continue
		}
		e, err := load(root, name)
		if err != nil {
			l.Skipped = append(l.Skipped, Skipped{name, err.Error()})
			continue
		}
		e.Number = len(l.Entries) + 1
		e.Selected, e.Why = preselect(e)
		if found, ok := scan(e); ok {
			e.Refusal, e.Selected = &found, false
		}
		l.Entries = append(l.Entries, e)
	}
	return l, nil
}

func load(root *os.Root, name string) (Entry, error) {
	info, err := root.Stat(name)
	if err != nil {
		return Entry{}, fmt.Errorf("cannot read the file: %w", err)
	}
	if !info.Mode().IsRegular() {
		return Entry{}, errors.New("not a regular file")
	}
	if info.Size() > maxFile {
		return Entry{}, fmt.Errorf("file is larger than %d KB, a memory should be short", maxFile>>10)
	}
	data, err := root.ReadFile(name)
	if err != nil {
		return Entry{}, fmt.Errorf("cannot read the file: %w", err)
	}
	return parse(name, data)
}

// parse reads name, description and type from the header and ignores every
// other field. A top-level type wins over metadata.type. The error is the reason the file is skipped.
func parse(file string, data []byte) (Entry, error) {
	meta, body, err := frontmatter.Split(data)
	if err != nil {
		return Entry{}, err
	}
	nodes, err := frontmatter.Fields(meta)
	if err != nil {
		return Entry{}, fmt.Errorf("frontmatter does not parse: %w", err)
	}
	vals := scalars(nodes)
	typ := vals["type"]
	if typ == "" {
		typ = nestedType(nodes)
	}
	e := Entry{File: file, Name: vals["name"], Description: vals["description"], Kind: Kind(typ), Body: strings.TrimSpace(string(body))}
	switch {
	case e.Kind == kindUser:
		return Entry{}, errors.New("type user is about a person and is never imported")
	case e.Kind != KindFeedback && e.Kind != KindProject && e.Kind != KindReference:
		return Entry{}, fmt.Errorf("type must be feedback, project or reference, got %q", e.Kind)
	case e.Name == "":
		return Entry{}, errors.New("the frontmatter has no name")
	case e.Description == "":
		return Entry{}, errors.New("the frontmatter has no description")
	case e.Body == "":
		return Entry{}, errors.New("the file has no text after the frontmatter")
	}
	return e, nil
}

// scalars maps the plain text fields of a header, from key and value nodes
// alternating as frontmatter.Fields returns them.
func scalars(nodes []*yaml.Node) map[string]string {
	vals := map[string]string{}
	for i := 0; i+1 < len(nodes); i += 2 {
		if nodes[i+1].Kind == yaml.ScalarNode {
			vals[nodes[i].Value] = strings.TrimSpace(nodes[i+1].Value)
		}
	}
	return vals
}

// nestedType returns metadata.type, or "" when metadata is absent or not a
// mapping. Claude Code writes the type there.
func nestedType(nodes []*yaml.Node) string {
	for i := 0; i+1 < len(nodes); i += 2 {
		if nodes[i].Value == "metadata" && nodes[i+1].Kind == yaml.MappingNode {
			return scalars(nodes[i+1].Content)["type"]
		}
	}
	return ""
}

// scan returns the first secret finding in any field that would be sent.
func scan(e Entry) (secretscan.Finding, bool) {
	for _, f := range []struct{ field, text string }{{"name", e.Name}, {"description", e.Description}, {"body", e.Body}} {
		if found, ok := secretscan.Scan(f.field, f.text); ok {
			return found, true
		}
	}
	return secretscan.Finding{}, false
}

// Numbers returns the numbers of the pre-selected entries.
func (l Listing) Numbers() []int {
	var out []int
	for _, e := range l.Entries {
		if e.Selected {
			out = append(out, e.Number)
		}
	}
	return out
}

// entry finds an entry by number.
func (l Listing) entry(n int) (Entry, bool) {
	i := slices.IndexFunc(l.Entries, func(e Entry) bool { return e.Number == n })
	if i < 0 {
		return Entry{}, false
	}
	return l.Entries[i], true
}
