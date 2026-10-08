// Package textdiff produces line-based unified diffs for small text files
// such as agent definitions.
package textdiff

import (
	"errors"
	"fmt"
	"strings"
)

// maxLines bounds the O(n*m) comparison. Agent files are far smaller.
const maxLines = 5000

// ErrTooLarge means a text has more lines than the diff compares.
var ErrTooLarge = errors.New("files too large to diff")

type opKind int

const (
	opEqual opKind = iota + 1
	opDelete
	opInsert
)

type op struct {
	kind opKind
	line string
}

// Unified returns a unified diff of a and b with the given context lines, or
// "" when they are equal.
func Unified(nameA, nameB, a, b string, context int) (string, error) {
	if a == b {
		return "", nil
	}
	if context < 0 {
		context = 0
	}
	al, bl := splitLines(a), splitLines(b)
	if len(al) > maxLines || len(bl) > maxLines {
		return "", fmt.Errorf("%w (over %d lines)", ErrTooLarge, maxLines)
	}
	ops := diff(al, bl)

	var out strings.Builder
	fmt.Fprintf(&out, "--- %s\n+++ %s\n", nameA, nameB)
	for _, h := range hunks(ops, min(context, len(ops))) {
		out.WriteString(h)
	}
	return out.String(), nil
}

// splitLines keeps each line's "\n", so a missing final newline is a change.
func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	lines := strings.SplitAfter(s, "\n")
	if lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

// line formats one diff line; a line without "\n" gets the usual marker.
func line(prefix, l string) string {
	if strings.HasSuffix(l, "\n") {
		return prefix + l
	}
	return prefix + l + "\n\\ No newline at end of file\n"
}

// diff computes an edit script with a longest-common-subsequence table.
func diff(a, b []string) []op {
	n, m := len(a), len(b)
	lcs := make([][]int, n+1)
	for i := range lcs {
		lcs[i] = make([]int, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if a[i] == b[j] {
				lcs[i][j] = lcs[i+1][j+1] + 1
			} else {
				lcs[i][j] = max(lcs[i+1][j], lcs[i][j+1])
			}
		}
	}
	ops := make([]op, 0, n+m)
	i, j := 0, 0
	for i < n && j < m {
		switch {
		case a[i] == b[j]:
			ops = append(ops, op{opEqual, a[i]})
			i++
			j++
		case lcs[i+1][j] >= lcs[i][j+1]:
			ops = append(ops, op{opDelete, a[i]})
			i++
		default:
			ops = append(ops, op{opInsert, b[j]})
			j++
		}
	}
	for ; i < n; i++ {
		ops = append(ops, op{opDelete, a[i]})
	}
	for ; j < m; j++ {
		ops = append(ops, op{opInsert, b[j]})
	}
	return ops
}

// hunks groups changes with up to context unchanged lines around them.
func hunks(ops []op, context int) []string {
	var out []string
	aLine, bLine := 1, 1
	for i := 0; i < len(ops); {
		if ops[i].kind == opEqual {
			aLine++
			bLine++
			i++
			continue
		}
		// Start a hunk `context` lines before this change.
		start := max(0, i-context)
		aStart, bStart := aLine-(i-start), bLine-(i-start)
		end := i
		for end < len(ops) {
			if ops[end].kind != opEqual {
				end++
				continue
			}
			run := end
			for run < len(ops) && ops[run].kind == opEqual {
				run++
			}
			if run == len(ops) || run-end > 2*context {
				end = min(end+context, len(ops))
				break
			}
			end = run
		}

		var body strings.Builder
		aCount, bCount := 0, 0
		for _, o := range ops[start:end] {
			switch o.kind {
			case opEqual:
				body.WriteString(line(" ", o.line))
				aCount++
				bCount++
			case opDelete:
				body.WriteString(line("-", o.line))
				aCount++
			case opInsert:
				body.WriteString(line("+", o.line))
				bCount++
			}
		}
		out = append(out, fmt.Sprintf("@@ -%d,%d +%d,%d @@\n%s", aStart, aCount, bStart, bCount, body.String()))

		for _, o := range ops[i:end] {
			if o.kind != opInsert {
				aLine++
			}
			if o.kind != opDelete {
				bLine++
			}
		}
		i = end
	}
	return out
}
