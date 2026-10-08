// Package frontmatter splits a Markdown file into its YAML header and body,
// the shape agent and skill files share.
package frontmatter

import (
	"errors"
	"fmt"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/petervlad/loadout/internal/strictyaml"
)

// Delimiter opens and closes the YAML header.
const Delimiter = "---"

// ErrMissing is returned when a file does not start with a "---" header.
var ErrMissing = errors.New("file must start with a --- frontmatter block")

// Split separates the YAML between the first two "---" lines from the
// Markdown body after them. CRLF line ends and a byte order mark are
// accepted.
func Split(data []byte) (meta, body []byte, err error) {
	text := strings.ReplaceAll(string(data), "\r\n", "\n")
	first, rest, ok := strings.Cut(text, "\n")
	if !ok || strings.TrimRight(strings.TrimPrefix(first, "\ufeff"), " \t") != Delimiter {
		return nil, nil, ErrMissing
	}
	lines := strings.Split(rest, "\n")
	for i, line := range lines {
		// Only an unindented --- closes it: YAML may hold an indented "---"
		// line inside a block string.
		if strings.TrimRight(line, " \t") == Delimiter {
			return []byte(strings.Join(lines[:i], "\n")), []byte(strings.Join(lines[i+1:], "\n")), nil
		}
	}
	return nil, nil, errors.New("frontmatter is not closed with a --- line")
}

// Join writes meta (YAML, ending in a newline) and body as a file.
func Join(meta []byte, body string) []byte {
	var b strings.Builder
	b.WriteString(Delimiter + "\n")
	b.Write(meta)
	b.WriteString(Delimiter + "\n")
	b.WriteString(body)
	b.WriteString("\n")
	return []byte(b.String())
}

// Fields decodes meta, the YAML of a header, into its fields: key and
// value nodes alternating, in their order. Empty YAML has no fields. A
// header that is not a mapping, a key that is not plain text and a key
// that appears twice are errors, as is a second YAML document.
func Fields(meta []byte) ([]*yaml.Node, error) {
	var doc yaml.Node
	if err := strictyaml.Decode(meta, &doc); errors.Is(err, strictyaml.ErrEmpty) {
		return nil, nil
	} else if err != nil {
		return nil, err
	}
	root := doc.Content[0]
	if root.Kind == yaml.ScalarNode && root.Tag == "!!null" {
		return nil, nil
	}
	if root.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("line %d: want fields such as name and description", root.Line)
	}
	seen := map[string]bool{}
	for i := 0; i+1 < len(root.Content); i += 2 {
		k := root.Content[i]
		if k.Kind != yaml.ScalarNode {
			return nil, fmt.Errorf("line %d: a field name must be plain text", k.Line)
		}
		if seen[k.Value] {
			return nil, fmt.Errorf("line %d: field %q appears twice", k.Line, k.Value)
		}
		seen[k.Value] = true
	}
	return root.Content, nil
}
