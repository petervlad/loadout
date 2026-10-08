package tier

import (
	"errors"
	"fmt"
	"maps"
	"strings"

	"github.com/petervlad/loadout/internal/agent"
)

// Field is the part of a cell an edit changes.
type Field string

// The two values of a cell.
const (
	FieldModel  Field = "model"
	FieldEffort Field = "effort"
)

// Edit sets one value of one cell; an empty Value clears it, so the cell
// inherits it again.
type Edit struct {
	Key   Key
	Field Field
	Value string
}

// ParseEdits reads --model and --effort flags of the form tool:hint=value,
// for example copilot:deep=Claude Sonnet 5 or copilot:deep=high. An empty
// value (copilot:fast=) clears the value.
func ParseEdits(models, efforts []string) ([]Edit, error) {
	var out []Edit
	var errs []error
	for _, group := range []struct {
		field Field
		flags []string
	}{{FieldModel, models}, {FieldEffort, efforts}} {
		for _, f := range group.flags {
			e, err := parseEdit(group.field, f)
			if err != nil {
				errs = append(errs, err)
				continue
			}
			out = append(out, e)
		}
	}
	return out, errors.Join(errs...)
}

func parseEdit(field Field, s string) (Edit, error) {
	key, value, ok := strings.Cut(s, "=")
	tool, hint, ok2 := strings.Cut(key, ":")
	if !ok || !ok2 {
		return Edit{}, fmt.Errorf("--%s %q: want TOOL:HINT=VALUE, for example copilot:deep=%s", field, s, example(field))
	}
	e := Edit{Key: Key{Tool: tool, Hint: agent.Model(hint)}, Field: field, Value: value}
	if err := e.Key.validate(); err != nil {
		return Edit{}, fmt.Errorf("--%s %q: %w", field, s, err)
	}
	return e, nil
}

func example(field Field) string {
	if field == FieldEffort {
		return "high"
	}
	return `"Claude Sonnet 5"`
}

// Apply returns t with the edits made, in order, validated like New.
func (t Table) Apply(edits []Edit) (Table, error) {
	cells := maps.Clone(t.cells)
	if cells == nil {
		cells = map[Key]Choice{}
	}
	for _, e := range edits {
		c := cells[e.Key]
		switch e.Field {
		case FieldModel:
			c.Model = e.Value
		case FieldEffort:
			c.Effort = Effort(e.Value)
		default:
			return Table{}, fmt.Errorf("unknown field %q", e.Field)
		}
		cells[e.Key] = c
	}
	return New(cells)
}
