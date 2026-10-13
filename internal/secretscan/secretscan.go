// Package secretscan finds credentials in text before team memory stores it.
//
// The filter rejects, it never redacts: a redacted memory may be wrong, and a
// stored secret ends up in backups. A false positive is fixed by adding a row
// to the allowlist (allowlist.go) and a test case, never by a bypass flag.
//
// Scan is pure and linear in the input: Go's regexp has no backtracking, and
// the callers cap the text (the request body limit) before decoding it.
package secretscan

import (
	"regexp"
	"strings"
	"unicode/utf8"
)

// Finding says which rule matched where. It never holds the match, so it is
// safe to log and to put in an error message.
type Finding struct {
	Rule   string // "aws-access-key-id"
	Field  string // "body"
	Line   int    // 1-based
	Column int    // 1-based, in runes
}

// rule is one detector. accept, when set, vets each regexp match (the span)
// with the text before it, so a rule can be broad and still precise.
type rule struct {
	name   string
	re     *regexp.Regexp
	accept func(span, before string) bool
}

// rules are tried in order and the first rule with an accepted match wins, so
// specific rules come before the catch-all high-entropy one and a key is
// reported by its own name.
var rules = []rule{
	{name: "aws-access-key-id", re: regexp.MustCompile(`(?:AKIA|ASIA|ABIA|ACCA|A3T[A-Z0-9])[A-Z0-9]{16}`)},
	{name: "aws-secret-access-key", re: regexp.MustCompile(`(?i)(?:aws_)?secret_access_key[ \t]*[=:][ \t]*["']?[A-Za-z0-9/+=]{40}`)},
	{name: "jwt", re: regexp.MustCompile(`eyJ[A-Za-z0-9_-]{5,}\.[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}`)},
	{name: "private-key", re: regexp.MustCompile(`-----BEGIN (?:RSA |EC |DSA |OPENSSH |PGP |ENCRYPTED )?PRIVATE KEY(?: BLOCK)?-----`)},
	{name: "github-token", re: regexp.MustCompile(`gh[pousr]_[A-Za-z0-9]{36,}|github_pat_[A-Za-z0-9_]{22,}`)},
	{name: "atlassian-token", re: regexp.MustCompile(`ATATT3[A-Za-z0-9_=-]{20,}`)},
	// Loadout's own tokens are lower-case base32, but case is not worth a miss.
	{name: "loadout-token", re: regexp.MustCompile(`ld[tm]_[A-Za-z2-7]{52}`)},
	{name: "slack-token", re: regexp.MustCompile(`xox[abprs]-[A-Za-z0-9-]{10,}`)},
	// A password in the address of a service: scheme://user:password@host.
	{name: "url-password", re: regexp.MustCompile(`[A-Za-z][A-Za-z0-9+.-]*://[^\s/:@]+:[^\s/@]+@`), accept: acceptURLPassword},
	// A key that names a secret, quoted as JSON or YAML writes it: "password": "x".
	{name: "json-secret-key", re: regexp.MustCompile(`(?i)"[A-Za-z0-9_.-]*(?:password|passwd|pwd|secret|api[_-]?key)"[ \t]*:[ \t]*"[^"\s]+"`), accept: acceptJSONValue},
	{name: "api-key-assignment", re: regexp.MustCompile(`(?i)(?:api[_-]?key|token)["']?[ \t]*[=:][ \t]*["']?[A-Za-z0-9+/=_.~-]{20,}`)},
	{name: "password-assignment", re: regexp.MustCompile(`(?i)(?:password|passwd|pwd|secret)[ \t]*[=:][ \t]*\S{4,}`), accept: acceptPassword},
	// Covers Jira and Confluence Data Center personal access tokens, which have
	// no prefix to match on.
	{name: "high-entropy", re: regexp.MustCompile(`[A-Za-z0-9+/=_-]{32,}`), accept: acceptHighEntropy},
}

// minEntropy is the Shannon entropy, in bits per character, from which a long
// mixed-case run counts as a random token and not as a word or an identifier.
const minEntropy = 4.0

// Scan returns the first finding in text, labelled with field. The ok result is
// false when the text is clean.
func Scan(field, text string) (Finding, bool) {
	for _, r := range rules {
		for _, loc := range r.re.FindAllStringIndex(text, -1) {
			span, before := text[loc[0]:loc[1]], text[:loc[0]]
			if isExample(span) || (r.accept != nil && !r.accept(span, before)) {
				continue
			}
			line, col := position(text, loc[0])
			return Finding{Rule: r.name, Field: field, Line: line, Column: col}, true
		}
	}
	return Finding{}, false
}

// position turns a byte offset into a 1-based line and a 1-based rune column.
func position(text string, offset int) (line, column int) {
	head := text[:offset]
	line = 1 + strings.Count(head, "\n")
	column = 1 + utf8.RuneCountInString(head[strings.LastIndexByte(head, '\n')+1:])
	return line, column
}

func acceptPassword(span, _ string) bool {
	i := strings.IndexAny(span, "=:")
	return !isPlaceholder(strings.TrimLeft(span[i+1:], " \t"))
}

// acceptURLPassword vets a "scheme://user:password@" span: the password is
// what follows the colon after the user.
func acceptURLPassword(span, _ string) bool {
	rest := span[strings.Index(span, "://")+len("://"):]
	pw := strings.TrimSuffix(rest[strings.IndexByte(rest, ':')+1:], "@")
	return !isPlaceholder(pw)
}

// acceptJSONValue vets a `"key": "value"` span. The value holds no quote, so
// it starts after the last quote before the closing one.
func acceptJSONValue(span, _ string) bool {
	body := strings.TrimSuffix(span, `"`)
	return !isPlaceholder(body[strings.LastIndexByte(body, '"')+1:])
}

func acceptHighEntropy(span, before string) bool {
	if isBenignRun(span, before) {
		return false
	}
	var upper, lower, digit bool
	counts := make(map[rune]int)
	for _, c := range span {
		counts[c]++
		switch {
		case c >= 'A' && c <= 'Z':
			upper = true
		case c >= 'a' && c <= 'z':
			lower = true
		case c >= '0' && c <= '9':
			digit = true
		}
	}
	return upper && lower && digit && entropy(counts, len(span)) >= minEntropy
}
