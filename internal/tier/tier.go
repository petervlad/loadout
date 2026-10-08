// Package tier maps an agent's model hint (fast, balanced, deep) to what
// each AI tool runs: a model name in the tool's own words and, optionally,
// a reasoning effort. A project keeps a table; its teams inherit it and may
// override single values. loadout only writes the values into agent files
// on the person's machine: it never runs a model.
package tier

import (
	"cmp"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/petervlad/loadout/internal/agent"
	"github.com/petervlad/loadout/internal/ident"
)

// Effort is a reasoning effort level Claude Code and Copilot both read.
type Effort string

// Effort levels, lowest first. EffortUnset leaves it to the tool.
const (
	EffortUnset  Effort = ""
	EffortLow    Effort = "low"
	EffortMedium Effort = "medium"
	EffortHigh   Effort = "high"
	EffortXHigh  Effort = "xhigh"
	EffortMax    Effort = "max"
)

var efforts = []Effort{EffortLow, EffortMedium, EffortHigh, EffortXHigh, EffortMax}

// Efforts returns the effort levels, lowest first, without EffortUnset.
func Efforts() []Effort { return slices.Clone(efforts) }

// Valid reports whether e is a level or unset.
func (e Effort) Valid() bool { return e == EffortUnset || slices.Contains(efforts, e) }

// Hints returns the model hints a table has rows for, cheapest first.
func Hints() []agent.Model {
	return []agent.Model{agent.ModelFast, agent.ModelBalanced, agent.ModelDeep}
}

// Limits of a table. Names change often, so a model is checked loosely.
const (
	MaxModelLen = 80
	MaxTools    = 8
)

// Choice is what one tool runs for one hint. The zero value leaves both to
// the person.
type Choice struct {
	// Model is the tool's own value: "opus" for Claude Code, the picker
	// name "Claude Sonnet 5" for Copilot.
	Model  string
	Effort Effort
}

// IsZero reports whether c sets nothing.
func (c Choice) IsZero() bool { return c == Choice{} }

// Over fills what c leaves empty from farther, value by value, so a team
// that sets only an effort keeps the project's model.
func (c Choice) Over(farther Choice) Choice {
	return Choice{Model: cmp.Or(c.Model, farther.Model), Effort: cmp.Or(c.Effort, farther.Effort)}
}

func (c Choice) validate() error {
	var errs []error
	if c.Model != "" {
		if err := CheckModel(c.Model); err != nil {
			errs = append(errs, err)
		}
	}
	if !c.Effort.Valid() {
		errs = append(errs, fmt.Errorf("effort %q: want one of %s, or empty", c.Effort, joinEfforts()))
	}
	return errors.Join(errs...)
}

// CheckModel reports whether s can be a model value: 1 to MaxModelLen
// printable characters without spaces at either end. Anything else is up
// to the tool, which falls back to the person's own model for a name it
// does not know.
func CheckModel(s string) error {
	n := utf8.RuneCountInString(s)
	switch {
	case n == 0:
		return errors.New("model is empty")
	case n > MaxModelLen:
		return fmt.Errorf("model %q is longer than %d characters", s, MaxModelLen)
	case !utf8.ValidString(s):
		return fmt.Errorf("model %q is not valid UTF-8", s)
	case strings.TrimSpace(s) != s:
		return fmt.Errorf("model %q starts or ends with a space", s)
	}
	for _, r := range s {
		if !unicode.IsPrint(r) {
			return fmt.Errorf("model %q holds a character that is not printable", s)
		}
	}
	return nil
}

// Key names one cell: a tool (the target name, "claude" or "copilot") and a
// hint.
type Key struct {
	Tool string
	Hint agent.Model
}

func (k Key) validate() error {
	if err := ident.Check("tool", k.Tool); err != nil {
		return err
	}
	if k.Hint == agent.ModelUnset || !k.Hint.Valid() {
		return fmt.Errorf("hint %q: want fast, balanced or deep", k.Hint)
	}
	return nil
}

// String formats k as tool:hint, the form flags use.
func (k Key) String() string { return k.Tool + ":" + string(k.Hint) }

// Cell is one key with its choice.
type Cell struct {
	Key
	Choice
}

// Table maps keys to choices. It is built only by New and never changes,
// so it can be shared.
type Table struct{ cells map[Key]Choice }

// New validates cells and returns their table. A choice that sets nothing
// means "inherit" and is left out.
func New(cells map[Key]Choice) (Table, error) {
	out := make(map[Key]Choice, len(cells))
	tools := map[string]bool{}
	var errs []error
	for k, c := range cells {
		if err := k.validate(); err != nil {
			errs = append(errs, err)
			continue
		}
		if err := c.validate(); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", k, err))
			continue
		}
		if c.IsZero() {
			continue
		}
		out[k] = c
		tools[k.Tool] = true
	}
	if len(tools) > MaxTools {
		errs = append(errs, fmt.Errorf("the table names %d tools, the limit is %d", len(tools), MaxTools))
	}
	if err := errors.Join(errs...); err != nil {
		return Table{}, err
	}
	return Table{cells: out}, nil
}

// FromCells is New for a list of cells; a key given twice is an error.
func FromCells(cells []Cell) (Table, error) {
	m := make(map[Key]Choice, len(cells))
	for _, c := range cells {
		if _, dup := m[c.Key]; dup {
			return Table{}, fmt.Errorf("%s is given twice", c.Key)
		}
		m[c.Key] = c.Choice
	}
	return New(m)
}

// Get returns the choice for tool and hint, and whether the table has one.
func (t Table) Get(tool string, h agent.Model) (Choice, bool) {
	c, ok := t.cells[Key{Tool: tool, Hint: h}]
	return c, ok
}

// Empty reports whether the table sets nothing.
func (t Table) Empty() bool { return len(t.cells) == 0 }

// Over returns t with farther below it: per cell and per value, t wins
// where it sets something.
func (t Table) Over(farther Table) Table {
	out := maps.Clone(farther.cells)
	if out == nil {
		out = map[Key]Choice{}
	}
	for k, c := range t.cells {
		out[k] = c.Over(out[k])
	}
	return Table{cells: out}
}

// Cells returns every cell, sorted by tool and then from fast to deep.
func (t Table) Cells() []Cell {
	out := make([]Cell, 0, len(t.cells))
	for k, c := range t.cells {
		out = append(out, Cell{Key: k, Choice: c})
	}
	slices.SortFunc(out, func(a, b Cell) int {
		return cmp.Or(cmp.Compare(a.Tool, b.Tool), cmp.Compare(hintRank(a.Hint), hintRank(b.Hint)))
	})
	return out
}

// Tools returns the tools the table names, sorted.
func (t Table) Tools() []string {
	var out []string
	for k := range t.cells {
		if !slices.Contains(out, k.Tool) {
			out = append(out, k.Tool)
		}
	}
	slices.Sort(out)
	return out
}

// Digest identifies the table's content: the same cells give the same
// digest. It is empty for an empty table, which is what old lock entries
// hold, so they need no rewrite.
func (t Table) Digest() string {
	if t.Empty() {
		return ""
	}
	h := sha256.New()
	for _, c := range t.Cells() {
		// Unit separators cannot appear in a valid value.
		fmt.Fprintf(h, "%s\x1f%s\x1f%s\x1f%s\x1e", c.Tool, c.Hint, c.Model, c.Effort)
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil))[:16]
}

func hintRank(h agent.Model) int { return slices.Index(Hints(), h) }

func joinEfforts() string {
	s := make([]string, len(efforts))
	for i, e := range efforts {
		s[i] = string(e)
	}
	return strings.Join(s, ", ")
}
