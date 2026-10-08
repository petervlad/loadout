package importer

import (
	"fmt"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/petervlad/loadout/internal/frontmatter"
)

// quotableFields are the fields whose unquoted ": " quoteFields repairs.
// People write them as prose, and some tools read such a header anyway.
var quotableFields = []string{"name", "description"}

// quoteFields repairs a header that is not valid YAML only because a
// one-line name or description holds an unquoted ": ", by quoting that
// value. It returns data unchanged when the header parses, or when it is
// Split's to refuse. Any other YAML error is returned as it was, and so is
// one that quoting does not cure. Only the returned copy changes; line
// ends and everything else stay as they were.
func quoteFields(data []byte) ([]byte, []string, error) {
	meta, _, err := frontmatter.Split(data)
	if err != nil {
		return data, nil, nil //nolint:nilerr // the caller's own Split reports it, with its file name
	}
	parseErr := validYAML(meta)
	if parseErr == nil {
		return data, nil, nil
	}

	lines := strings.Split(string(data), "\n")
	var notices []string
	// lines[0] is the opening ---; the header ends at the closing one.
	for i := 1; i < len(lines) && strings.TrimRight(lines[i], " \t\r") != frontmatter.Delimiter; i++ {
		line := strings.TrimSuffix(lines[i], "\r")
		for _, f := range quotableFields {
			v, ok := strings.CutPrefix(line, f+": ")
			if !ok || !needsQuotes(strings.TrimSpace(v)) || continued(lines, i) {
				continue
			}
			quoted, err := yaml.Marshal(&yaml.Node{Kind: yaml.ScalarNode, Style: yaml.DoubleQuotedStyle, Value: strings.TrimSpace(v)})
			if err != nil {
				return data, nil, parseErr
			}
			lines[i] = f + ": " + strings.TrimSuffix(string(quoted), "\n") + strings.TrimPrefix(lines[i], line)
			notices = append(notices, fmt.Sprintf(`quoted the %s: it holds ": ", which YAML refuses unquoted (the source is unchanged)`, f))
		}
	}
	if len(notices) == 0 {
		return data, nil, parseErr
	}
	fixed := []byte(strings.Join(lines, "\n"))
	if meta, _, err := frontmatter.Split(fixed); err != nil || validYAML(meta) != nil {
		return data, nil, parseErr
	}
	return fixed, notices, nil
}

func validYAML(meta []byte) error {
	_, err := frontmatter.Fields(meta)
	return err
}

// needsQuotes reports whether v is a plain value holding ": ". A value
// that is quoted, a block, a list or map, or that holds a comment is left
// alone.
func needsQuotes(v string) bool {
	if v == "" || strings.ContainsRune(`"'>|[{&*!%@#`+"`", rune(v[0])) {
		return false
	}
	return strings.Contains(v, ": ") && !strings.Contains(v, " #")
}

// continued reports whether the value on line i goes on over the next
// line, which makes it more than one line.
func continued(lines []string, i int) bool {
	if i+1 >= len(lines) {
		return false
	}
	next := lines[i+1]
	return strings.TrimSpace(next) != "" && (next[0] == ' ' || next[0] == '\t')
}
