// Package kb defines knowledge bases: a set of documents a team shares so
// every member's AI tool can look things up in them.
//
// A knowledge base installs as a skill (package skill) whose SKILL.md
// loadout generates: the description tells the tool when to read it, the
// body lists the documents. Tools load only that description at startup
// and open a document when a task needs it, so a large knowledge base costs
// nothing until it is used, and no tool needs a knowledge base feature of
// its own.
package kb

import (
	"errors"
	"fmt"
	"maps"
	"path"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/petervlad/loadout/internal/ident"
	"github.com/petervlad/loadout/internal/skill"
)

// Extensions are the document types a knowledge base holds: text a tool
// reads directly and a reviewer can diff. Diagrams are Mermaid (.mmd, or a
// mermaid block in Markdown) or uncompressed draw.io; PDFs and images are
// turned into these before they are published.
var Extensions = []string{".md", ".markdown", ".txt", ".rst", ".adoc", ".csv", ".tsv", ".json", ".yaml", ".yml", ".mmd", ".mermaid", ".drawio"}

// Doc is one document of a knowledge base.
type Doc struct {
	// Path is slash-separated, relative to the knowledge base's folder.
	Path string
	// Title is the document's first "# " heading, or its file name.
	Title string
}

// Base is one knowledge base. It is immutable once parsed.
type Base struct {
	Name        string
	Description string
	docs        []Doc
	files       map[string][]byte
}

// Parse builds a knowledge base named name from its documents, keyed by
// slash-separated path.
func Parse(name, description string, files map[string][]byte) (*Base, error) {
	b := &Base{Name: name, Description: strings.TrimSpace(description), files: map[string][]byte{}}
	var errs []error
	if err := ident.Check("knowledge base", name); err != nil {
		errs = append(errs, err)
	}
	if b.Description == "" {
		errs = append(errs, errors.New("description is empty: tools read it to decide when to look in the knowledge base"))
	}
	for _, p := range slices.Sorted(maps.Keys(files)) {
		switch {
		case strings.EqualFold(p, skill.File):
			errs = append(errs, fmt.Errorf("%s: loadout writes a knowledge base's %s itself; rename the document", p, skill.File))
			continue
		case !slices.Contains(Extensions, strings.ToLower(path.Ext(p))):
			errs = append(errs, fmt.Errorf("%s: not a document type a knowledge base holds (want %s)", p, strings.Join(Extensions, ", ")))
			continue
		}
		data := files[p]
		b.files[p] = append([]byte(nil), data...)
		b.docs = append(b.docs, Doc{Path: p, Title: Title(p, data)})
	}
	if len(files) == 0 {
		errs = append(errs, errors.New("a knowledge base needs at least one document"))
	}
	if err := errors.Join(errs...); err != nil {
		return nil, err
	}
	return b, nil
}

// Docs returns the documents, sorted by path.
func (b *Base) Docs() []Doc { return slices.Clone(b.docs) }

// Skill returns the knowledge base as the skill tools load: the documents
// as its files, and an index as its instructions.
func (b *Base) Skill() (*skill.Skill, error) {
	return skill.New(b.Name, b.skillDescription(), "", b.index(), b.files)
}

// skillDescription tells a tool when to open the knowledge base, within
// the length tools accept.
func (b *Base) skillDescription() string {
	suffix := fmt.Sprintf(" Knowledge base (%d documents): read the ones a task needs.", len(b.docs))
	desc := b.Description
	if !strings.HasSuffix(desc, ".") && !strings.HasSuffix(desc, "!") && !strings.HasSuffix(desc, "?") {
		desc += "."
	}
	if room := skill.MaxDescription - utf8.RuneCountInString(suffix); utf8.RuneCountInString(desc) > room {
		// One character of the room goes to the ellipsis.
		desc = strings.TrimSpace(string([]rune(desc)[:room-1])) + "…"
	}
	return desc + suffix
}

// Index sizes. A tool reads the whole index when the skill loads, so a
// large knowledge base lists its folders instead of every document.
const (
	// maxIndexed is the most documents the index lists one by one.
	maxIndexed = 150
	// examples is how many titles a folder's row shows.
	examples = 3
)

// index is the generated body of SKILL.md.
func (b *Base) index() string {
	var s strings.Builder
	fmt.Fprintf(&s, "# %s\n\n%s\n\n", b.Name, b.Description)
	s.WriteString("This is a knowledge base. Read only the documents the task needs; paths are relative to this folder. Prefer what they say over general knowledge, and say which document you used.\n\n")
	if len(b.docs) <= maxIndexed {
		s.WriteString("| Document | Title |\n|---|---|\n")
		for _, d := range b.docs {
			fmt.Fprintf(&s, "| [%s](%s) | %s |\n", d.Path, d.Path, cell(d.Title))
		}
		return s.String()
	}
	fmt.Fprintf(&s, "It has %d documents, too many to list here, so they are grouped by folder. Search the folder that fits the task (Grep for a term, Glob for file names) and read the documents you find.\n\n", len(b.docs))
	s.WriteString("| Folder | Documents | For example |\n|---|---|---|\n")
	for _, g := range b.groups() {
		titles := make([]string, 0, examples)
		for _, d := range g.docs[:min(examples, len(g.docs))] {
			titles = append(titles, cell(d.Title))
		}
		fmt.Fprintf(&s, "| %s | %d | %s |\n", g.name, len(g.docs), strings.Join(titles, "; "))
	}
	return s.String()
}

// group is a row of a grouped index: a top-level folder, one top-level
// document, or all top-level documents when they are too many to list.
type group struct {
	name string
	docs []Doc
}

// groups returns the top-level files, then the top-level folders, each in
// path order. Top-level files get a row each unless there are more than
// maxIndexed, in which case they share one.
func (b *Base) groups() []group {
	var folders, files []group
	byFolder := map[string]int{}
	var top []Doc
	for _, d := range b.docs {
		dir, _, nested := strings.Cut(d.Path, "/")
		if !nested {
			top = append(top, d)
			continue
		}
		i, ok := byFolder[dir]
		if !ok {
			i = len(folders)
			byFolder[dir] = i
			folders = append(folders, group{name: fmt.Sprintf("[%s/](%s/)", dir, dir)})
		}
		folders[i].docs = append(folders[i].docs, d)
	}
	if len(top) > maxIndexed {
		files = []group{{name: "this folder", docs: top}}
	} else {
		for _, d := range top {
			files = append(files, group{name: fmt.Sprintf("[%s](%s)", d.Path, d.Path), docs: []Doc{d}})
		}
	}
	return append(files, folders...)
}

// cell escapes s for a Markdown table cell.
func cell(s string) string { return strings.ReplaceAll(s, "|", `\|`) }

// Title is the first "# " heading in the first 40 lines of a Markdown
// document, else the file name without its extension.
func Title(p string, data []byte) string {
	if ext := strings.ToLower(path.Ext(p)); ext == ".md" || ext == ".markdown" {
		lines := strings.SplitN(strings.TrimPrefix(string(data), "\ufeff"), "\n", 40)
		for _, line := range lines {
			if t, ok := strings.CutPrefix(strings.TrimSpace(line), "# "); ok && strings.TrimSpace(t) != "" {
				return strings.TrimSpace(t)
			}
		}
	}
	return strings.TrimSuffix(path.Base(p), path.Ext(p))
}
