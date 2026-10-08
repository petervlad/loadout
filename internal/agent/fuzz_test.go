package agent

import (
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
)

func FuzzParse(f *testing.F) {
	seeds := []string{
		validFile,
		"---\nname: planner\ndescription: Plans.\n---\nYou plan.\n",
		"---\r\nname: planner\r\ndescription: d\r\n---\r\nbody\r\n",
		"---\nname: a-b-1\ndescription: d\nmodel: fast\ntools: []\n---\nbody",
		"---\nname: planner\ndescription: |\n  multi\n  line\n---\nbody\n---\nmore body",
		"---\nname: planner\ndescription: \"a\\n---\\nb\"\n---\nbody",
		"---\nname: planner\ndescription: d\ntools: [read, read]\n---\nbody",
		"---\nname: planner\ndescription: d\nmodel: opus\n---\nbody",
		"---\nname: Planner\ndescription: d\n---\nbody",
		"---\nname: planner\ndescription: d\n---\n",
		"---\n---\n",
		"---\nname: [a, b]\n---\nbody",
		"---\n&a [*a]\n---\nbody",
		"---\nname: planner\ndescription: !!binary aGVsbG8=\n---\nbody",
		"  ---  \nname: planner\ndescription: d\n  ---  \nbody",
		"",
		"---",
		"no frontmatter",
	}
	for _, s := range seeds {
		f.Add([]byte(s))
	}

	f.Fuzz(func(t *testing.T, data []byte) {
		d, err := Parse(data)
		if err != nil {
			return
		}
		if err := d.Validate(); err != nil {
			t.Fatalf("Parse(%q) returned a Definition that fails Validate(): %v", data, err)
		}
		out, err := d.Marshal()
		if err != nil {
			t.Fatalf("Marshal(%#v) error: %v", d, err)
		}
		again, err := Parse(out)
		if err != nil {
			t.Fatalf("Parse(Marshal(%#v)) error: %v\nmarshalled:\n%s", d, err, out)
		}
		if diff := cmp.Diff(d, again, cmpopts.EquateEmpty()); diff != "" {
			t.Errorf("Parse(Marshal(d)) mismatch (-want +got):\n%s\nmarshalled:\n%s", diff, out)
		}
	})
}
