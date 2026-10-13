package pipeline

import (
	"errors"
	"fmt"
	"slices"
)

// Compatible reports whether next may follow prev inside one major: the same
// slots, and the same verdicts per slot. Briefs, files, edges and gates may
// change, because a binding does not depend on them. Renaming a slot's file
// is accepted, though it strands runs that already hold the old file.
// Reordering slots is accepted too, even though it can change which edges are
// back edges: the order only matters when a file is written, and a new order
// that breaks a max_rounds rule fails to parse.
func Compatible(prev, next *Template) error {
	var errs []error
	for _, p := range prev.slots {
		n, ok := next.Slot(p.Name)
		if !ok {
			errs = append(errs, fmt.Errorf("slot %s was removed or renamed", p.Name))
			continue
		}
		for _, v := range p.Verdicts {
			if !slices.Contains(n.Verdicts, v) {
				errs = append(errs, fmt.Errorf("slot %s no longer returns %s", p.Name, v))
			}
		}
		for _, v := range n.Verdicts {
			if !slices.Contains(p.Verdicts, v) {
				errs = append(errs, fmt.Errorf("slot %s now also returns %s", p.Name, v))
			}
		}
	}
	for _, n := range next.slots {
		if prev.index(n.Name) < 0 {
			errs = append(errs, fmt.Errorf("slot %s is new", n.Name))
		}
	}
	return errors.Join(errs...)
}
