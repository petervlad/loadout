package memory

import (
	"fmt"
	"time"

	"github.com/petervlad/loadout/internal/registry"
)

// Rule names for refusals that are not a secret rule.
const (
	RuleType         = "type"
	RuleNamespace    = "namespace"
	RuleMaxName      = "max-name"
	RuleMaxDesc      = "max-description"
	RuleMaxBody      = "max-body"
	RuleMaxLinks     = "max-links"
	RuleMaxNote      = "max-note"
	RuleOneLine      = "one-line"
	RuleRequired     = "required"
	RuleText         = "text"
	RuleNoChange     = "no-change"
	RuleWritesPerDay = "writes-per-day"
)

// Refusal says why a memory was not accepted. It names the limit or rule and,
// for a secret, where it is. It never holds the refused text, so it is safe to
// log and to send back. It wraps registry.ErrInvalid.
//
//nolint:errname // the name the plan and the service API use
type Refusal struct {
	Rule         string // a Rule constant or a secretscan rule name
	Field        string
	Line, Column int // 1-based, only for a secret
	msg          string
}

func (r *Refusal) Error() string { return r.msg }

func (r *Refusal) Unwrap() error { return registry.ErrInvalid }

func refuse(rule, field, format string, args ...any) *Refusal {
	return &Refusal{Rule: rule, Field: field, msg: fmt.Sprintf(format, args...)}
}

// secretRefusal reports a finding. The text of the match is not available
// here by design.
func secretRefusal(rule, field string, line, col int) *Refusal {
	return &Refusal{Rule: rule, Field: field, Line: line, Column: col, msg: fmt.Sprintf(
		"The %s looks like it holds a secret (rule %s, line %d, column %d), so nothing was saved. Remove it, and rotate the secret if it is real.",
		field, rule, line, col)}
}

// DailyRefusal is the refusal for a write past WritesPerDay in a rolling day.
// oldest is when the oldest write in the window was made: the person can write
// again once it ages out.
func DailyRefusal(oldest time.Time) *Refusal {
	msg := fmt.Sprintf("You have reached the limit of %d writes a day.", WritesPerDay)
	if !oldest.IsZero() {
		msg = fmt.Sprintf("You have reached the limit of %d writes a day. Try again after %s UTC.",
			WritesPerDay, oldest.Add(WriteWindow).UTC().Format("2006-01-02 15:04"))
	}
	return &Refusal{Rule: RuleWritesPerDay, msg: msg}
}
