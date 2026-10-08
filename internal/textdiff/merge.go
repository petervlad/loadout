package textdiff

import (
	"bytes"
	"fmt"
	"slices"
	"strings"
)

// Labels name the three sides in conflict markers.
type Labels struct{ Base, Ours, Theirs string }

// Merge3 merges the changes from base to ours and from base to theirs.
// Changes to different lines both apply; the same change on both sides
// applies once; different changes to the same or adjacent lines become a
// conflict, written diff3-style:
//
//	<<<<<<< ours label
//	||||||| base label
//	=======
//	>>>>>>> theirs label
//
// It returns the merged text and the number of conflicts. A text over
// the diff limit returns ErrTooLarge, for the caller to merge whole.
//
// It is the classic diff3: lines of base kept by both sides are stable
// and copied; between them, a stretch only one side changed takes that
// side, and a stretch both changed differently is a conflict.
func Merge3(base, ours, theirs string, l Labels) (text string, conflicts int, err error) {
	b, o, t := splitLines(base), splitLines(ours), splitLines(theirs)
	if len(b) > maxLines || len(o) > maxLines || len(t) > maxLines {
		return "", 0, fmt.Errorf("%w (over %d lines)", ErrTooLarge, maxLines)
	}
	toOurs, toTheirs := matches(b, o), matches(b, t)
	var out strings.Builder
	i, j, k := 0, 0, 0 // the next line of base, ours and theirs
	for {
		// The next base line both sides kept, or the end of all three.
		p := i
		for p < len(b) && (toOurs[p] < 0 || toTheirs[p] < 0) {
			p++
		}
		jo, ko := len(o), len(t)
		if p < len(b) {
			jo, ko = toOurs[p], toTheirs[p]
		}
		if mergeChunk(&out, b[i:p], o[j:jo], t[k:ko], l) {
			conflicts++
		}
		if p == len(b) {
			return out.String(), conflicts, nil
		}
		out.WriteString(b[p])
		i, j, k = p+1, jo+1, ko+1
	}
}

// matches maps each line of a to the line of b it is kept as, or -1 when
// b deletes or changes it.
func matches(a, b []string) []int {
	out := make([]int, len(a))
	i, j := 0, 0
	for _, o := range diff(a, b) {
		switch o.kind {
		case opEqual:
			out[i] = j
			i++
			j++
		case opDelete:
			out[i] = -1
			i++
		case opInsert:
			j++
		}
	}
	return out
}

// mergeChunk writes the merge of one unstable stretch and reports whether
// it is a conflict.
func mergeChunk(out *strings.Builder, base, ours, theirs []string, l Labels) bool {
	switch {
	case slices.Equal(ours, base), slices.Equal(ours, theirs):
		writeLines(out, theirs)
		return false
	case slices.Equal(theirs, base):
		writeLines(out, ours)
		return false
	}
	out.WriteString("<<<<<<< " + l.Ours + "\n")
	writeSide(out, ours)
	out.WriteString("||||||| " + l.Base + "\n")
	writeSide(out, base)
	out.WriteString("=======\n")
	writeSide(out, theirs)
	out.WriteString(">>>>>>> " + l.Theirs + "\n")
	return true
}

func writeLines(out *strings.Builder, lines []string) {
	for _, s := range lines {
		out.WriteString(s)
	}
}

// writeSide writes one side of a conflict, ending its last line so the
// next marker starts a line of its own.
func writeSide(out *strings.Builder, lines []string) {
	writeLines(out, lines)
	if n := len(lines); n > 0 && !strings.HasSuffix(lines[n-1], "\n") {
		out.WriteString("\n")
	}
}

// Whole is one conflict holding the three versions of a text whole, for a
// text too long to merge line by line, or one whose base is not known
// (base nil leaves its section empty).
func Whole(base, ours, theirs []byte, l Labels) []byte {
	var out bytes.Buffer
	part := func(marker string, data []byte) {
		out.WriteString(marker + "\n")
		out.Write(data)
		if len(data) > 0 && !bytes.HasSuffix(data, []byte("\n")) {
			out.WriteString("\n")
		}
	}
	part("<<<<<<< "+l.Ours, ours)
	part("||||||| "+l.Base, base)
	part("=======", theirs)
	out.WriteString(">>>>>>> " + l.Theirs + "\n")
	return out.Bytes()
}

// Marked reports whether text still holds a conflict marker line with the
// labels of l, as Merge3 and Whole write them.
func Marked(text []byte, l Labels) bool {
	markers := []string{"<<<<<<< " + l.Ours, "||||||| " + l.Base, ">>>>>>> " + l.Theirs}
	for line := range strings.SplitSeq(string(text), "\n") {
		if slices.Contains(markers, strings.TrimRight(line, "\r")) {
			return true
		}
	}
	return false
}
