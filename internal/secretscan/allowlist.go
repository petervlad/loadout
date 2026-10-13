package secretscan

import (
	"regexp"
	"strings"
)

// The allowlist is a table in code. A row is added together with a test case
// when real text is refused by mistake. There is no flag, field or header that
// skips the filter: a bypass would turn the filter into a suggestion.

// examples are keys printed in vendor documentation. They are public, so
// refusing them only teaches people to distrust the filter. Any rule skips a
// match that holds one.
var examples = []string{
	"AKIAIOSFODNN7EXAMPLE",
	"wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
}

// isExample is true when the span is an example key, or an assignment of one
// (aws_secret_access_key = <example>). A span that merely contains an example,
// such as a longer random run, is not one, or the example could be used to
// smuggle a real secret past the filter.
func isExample(span string) bool {
	for _, e := range examples {
		if span == e || assignmentOf(e).MatchString(span) {
			return true
		}
	}
	return false
}

// assignmentOf matches `name = value`, `name: "value"` and the like, where
// value is exactly e.
func assignmentOf(e string) *regexp.Regexp {
	return regexp.MustCompile(`^[A-Za-z_]+[ \t]*[=:][ \t]*["']?` + regexp.QuoteMeta(e) + `["']?$`)
}

// Benign runs look random to the entropy rule but are identifiers or
// checksums that teams quote all the time.
var (
	// Pure hex of up to 64 characters: git SHAs and sha256 digests.
	hexRun = regexp.MustCompile(`^[0-9A-Fa-f]{1,64}$`)
	uuid   = regexp.MustCompile(`^[0-9A-Fa-f]{8}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{12}$`)
	// "sha256:" digests: the colon ends the run, so the label is in before.
	digestLabel = regexp.MustCompile(`(?i)sha(?:1|224|256|384|512):$`)
	// Subresource integrity and npm lockfiles: "sha512-" plus base64, in the run.
	integrityPrefix = regexp.MustCompile(`^sha(?:256|384|512)-`)
)

func isBenignRun(span, before string) bool {
	switch {
	case hexRun.MatchString(span), uuid.MatchString(span), integrityPrefix.MatchString(span):
		return true
	case isWordy(span):
		return true
	case digestLabel.MatchString(before):
		return strings.Trim(span, "0123456789abcdefABCDEF") == "" // sha512 is 128 hex, longer than hexRun
	}
	return false
}

// wordLike is one part of a path or a folder name: UPPER, lower, Capitalised,
// 2611, or letters then digits (v2611, TRK).
var wordLike = regexp.MustCompile(`^(?:[A-Z]+|[a-z]+|[A-Z][a-z]+|[0-9]+|[A-Za-z]+[0-9]+)$`)

// isWordy is true for file paths and ticket-style names: at least two parts
// split on / - _ and ., every one word-like. A random base64 secret almost never
// splits into parts like that, and a real token inside a path is a part that
// is not word-like, so the path is still refused.
func isWordy(span string) bool {
	parts := strings.FieldsFunc(span, func(r rune) bool { return strings.ContainsRune("/-_.", r) })
	if len(parts) < 2 {
		return false
	}
	for _, p := range parts {
		if !wordLike.MatchString(p) {
			return false
		}
	}
	return true
}

// placeholder matches the values docs and templates put where a password goes.
var placeholder = regexp.MustCompile(`^(?:[*xX.#_-]{3,}|<[^<>]*>|\$\{[^{}]*\}|\$[A-Za-z_][A-Za-z0-9_]*)$`)

// isPlaceholder reports whether a password value is a stand-in such as ***,
// <password>, ${DB_PASSWORD} or $DB_PASSWORD. Quotes around the value and a
// trailing comma or bracket are not part of it. A closing brace is kept,
// because ${VAR} ends with one.
func isPlaceholder(value string) bool {
	v := strings.TrimLeft(value, "\"'`")
	v = strings.TrimRight(v, "\"'`,;)]")
	return placeholder.MatchString(v)
}
