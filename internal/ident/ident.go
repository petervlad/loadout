// Package ident validates the names loadout uses for teams and packages.
//
// Names end up in file paths and URLs, so they are restricted to lowercase
// letters, digits and single hyphens.
package ident

import (
	"fmt"
	"regexp"
)

const maxLen = 64

var pattern = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

// Check reports whether s is a valid name. kind ("team", "agent") only
// shapes the error message.
func Check(kind, s string) error {
	switch {
	case s == "":
		return fmt.Errorf("%s name is empty", kind)
	case len(s) > maxLen:
		return fmt.Errorf("%s name %q is longer than %d characters", kind, s, maxLen)
	case !pattern.MatchString(s):
		return fmt.Errorf("%s name %q must be lowercase letters, digits and single hyphens", kind, s)
	}
	return nil
}
