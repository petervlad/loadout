package target

import (
	"bytes"
	"fmt"
	"maps"
	"slices"

	"go.yaml.in/yaml/v3"

	"github.com/petervlad/loadout/internal/agent"
)

// MapTools maps canonical tools to one AI tool's names, keeping the order of
// tools and dropping duplicates. product names the AI tool in the error for
// a tool it has no equivalent for.
//
// A tool missing from names is an error. A tool mapped to an empty list is
// not: the AI tool does that on its own, so there is nothing to grant. The
// result can then be empty although tools is not; adapters must not render
// that as a missing list, which both tools read as "every tool".
func MapTools(names map[agent.Tool][]string, product string, tools []agent.Tool) ([]string, error) {
	var out []string
	for _, t := range tools {
		mapped, ok := names[t]
		if !ok {
			return nil, fmt.Errorf("tool %q has no %s equivalent", t, product)
		}
		for _, n := range mapped {
			if !slices.Contains(out, n) {
				out = append(out, n)
			}
		}
	}
	return out, nil
}

// Markdown returns an agent file: meta as YAML frontmatter, a comment naming
// the package the file came from, then the prompt. name is the agent's name,
// used in errors.
func Markdown(name string, meta any, stamp Stamp, prompt string) ([]byte, error) {
	header, err := yaml.Marshal(meta)
	if err != nil {
		return nil, fmt.Errorf("render %s: %w", name, err)
	}
	var b bytes.Buffer
	b.WriteString("---\n")
	b.Write(header)
	b.WriteString("---\n\n")
	b.WriteString(ManagedNote(stamp))
	b.WriteString("\n\n")
	b.WriteString(prompt)
	b.WriteString("\n")
	return b.Bytes(), nil
}

// ManagedNote is the comment that marks a file loadout wrote, naming the
// package version it came from.
func ManagedNote(stamp Stamp) string {
	return fmt.Sprintf("<!-- managed by loadout: %s (%s). Local edits pause updates; change the package and publish instead. -->", stamp, stamp.Digest.Short())
}

func sortedKeys[V any](m map[string]V) []string {
	return slices.Sorted(maps.Keys(m))
}
