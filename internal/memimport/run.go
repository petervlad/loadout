package memimport

import (
	"context"
	"fmt"
	"slices"

	"github.com/petervlad/loadout/internal/secretscan"
)

// Result is what a Saver says about one save.
type Result struct {
	ID        int64
	Duplicate bool // an equal memory exists and nothing was saved
	Detail    string
}

// Saver stores one entry in a team memory. The CLI and the MCP client
// implement it. With allowDup set, a likely duplicate is saved anyway.
type Saver interface {
	Save(ctx context.Context, e Entry, allowDup bool) (Result, error)
}

// Outcome is how one selected entry ended.
type Outcome string

// The outcomes of a run.
const (
	Saved     Outcome = "saved"
	Duplicate Outcome = "duplicate"
	Refused   Outcome = "refused"
	Failed    Outcome = "failed"
)

// Report is the result for one selected entry.
type Report struct {
	Number  int
	File    string
	Outcome Outcome
	Result  Result
	Finding *secretscan.Finding // set when Refused
	Err     error               // set when Failed
}

// Run saves the entries with the chosen numbers, in number order. A number
// that is not in the listing is an error before anything is saved. An entry
// with a secret is scanned again here and is never passed to the Saver. A
// failure on one entry does not stop the others, but a cancelled context does.
func Run(ctx context.Context, l Listing, numbers []int, s Saver, allowDup bool) ([]Report, error) {
	nums := slices.Clone(numbers)
	slices.Sort(nums)
	nums = slices.Compact(nums)
	entries := make([]Entry, 0, len(nums))
	for _, n := range nums {
		e, ok := l.entry(n)
		if !ok {
			return nil, fmt.Errorf("there is no entry number %d, choose from 1 to %d", n, len(l.Entries))
		}
		entries = append(entries, e)
	}
	var out []Report
	for _, e := range entries {
		if err := ctx.Err(); err != nil {
			return out, err
		}
		out = append(out, saveOne(ctx, e, s, allowDup))
	}
	return out, nil
}

func saveOne(ctx context.Context, e Entry, s Saver, allowDup bool) Report {
	r := Report{Number: e.Number, File: e.File}
	if f, ok := scan(e); ok {
		r.Outcome, r.Finding = Refused, &f
		return r
	}
	res, err := s.Save(ctx, e, allowDup)
	switch {
	case err != nil:
		r.Outcome, r.Err = Failed, err
	case res.Duplicate && !allowDup:
		r.Outcome, r.Result = Duplicate, res
	default:
		r.Outcome, r.Result = Saved, res
	}
	return r
}
