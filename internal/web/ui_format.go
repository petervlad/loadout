package web

import (
	"fmt"
	"hash/crc32"
	"html/template"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/petervlad/loadout/internal/account"
	"github.com/petervlad/loadout/internal/pack"
	"github.com/petervlad/loadout/internal/registry"
)

// uiFuncs are the template functions of the team pages. now dates "2 days
// ago".
func uiFuncs(now func() time.Time) template.FuncMap {
	return template.FuncMap{
		"ago":      func(t time.Time) string { return ago(now(), t) },
		"stamp":    func(t time.Time) string { return t.UTC().Format(time.RFC3339) },
		"exact":    func(t time.Time) string { return t.UTC().Format("2 Jan 2006, 15:04 UTC") },
		"month":    func(t time.Time) string { return month(now(), t) },
		"initials": initials,
		"static":   staticURL,
		"invalid":  invalidAttrs,
		"hue":      hue,
		"plural":   plural,
		"role":     roleName,
		"scope":    scopeName,
		"kind":     kindName,
		"size":     size,
		"add":      func(a, b int) int { return a + b },
		"join":     strings.Join,
		// anyShown reports whether the member search leaves a row shown.
		"anyShown": func(ms []memberRow) bool {
			return slices.ContainsFunc(ms, func(m memberRow) bool { return !m.Hidden })
		},
		// segments splits a file path at its slashes, so a title can
		// break there on a narrow screen.
		"segments": func(p string) []string { return strings.Split(p, "/") },
		"seq": func(n int) []int {
			out := make([]int, n)
			for i := range out {
				out[i] = i + 1
			}
			return out
		},
	}
}

// ago says how long ago t was, in words: "just now", "5 minutes ago",
// "yesterday", "3 weeks ago", "last month", "2 years ago".
func ago(now, t time.Time) string {
	d := now.Sub(t)
	days := int(d.Hours() / 24)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return plural(int(d.Minutes()), "minute") + " ago"
	case d < 24*time.Hour:
		return plural(int(d.Hours()), "hour") + " ago"
	case days == 1:
		return "yesterday"
	case days < 7:
		return plural(days, "day") + " ago"
	case days < 30:
		return plural(days/7, "week") + " ago"
	case days < 60:
		return "last month"
	case days < 365:
		return plural(days/30, "month") + " ago"
	}
	return plural(days/365, "year") + " ago"
}

// month names the month of t, with the year when it is not this year's.
func month(now, t time.Time) string {
	if t.Year() == now.Year() {
		return t.Format("January")
	}
	return t.Format("Jan 2006")
}

// plural writes n and the noun, adding an s when n is not 1.
func plural(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", n, noun)
}

// initials returns up to two capital letters for an avatar: of the first
// and last word of a name, or of an email's local part.
func initials(name string) string {
	name, _, _ = strings.Cut(name, "@")
	words := strings.FieldsFunc(name, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
	switch len(words) {
	case 0:
		return "?"
	case 1:
		return strings.ToUpper(firstRunes(words[0], 2))
	}
	return strings.ToUpper(firstRunes(words[0], 1) + firstRunes(words[len(words)-1], 1))
}

func firstRunes(s string, n int) string {
	i := 0
	for n > 0 && i < len(s) {
		_, size := utf8.DecodeRuneInString(s[i:])
		i += size
		n--
	}
	return s[:i]
}

// hue picks one of the five avatar colors for a stable key such as an
// email.
func hue(key string) int {
	return int(crc32.ChecksumIEEE([]byte(key)) % 5)
}

// roleName is a role as the pages show it.
func roleName(r account.Role) string {
	switch r {
	case account.RoleMember:
		return "Member"
	case account.RoleMaintainer:
		return "Maintainer"
	case account.RoleOwner:
		return "Owner"
	case account.RoleAdmin:
		return "Admin"
	}
	return string(r)
}

// kindName is a package kind as the pages show it: "Agent", "Knowledge
// base".
func kindName(k pack.Kind) string {
	what := k.Describe()
	if what == "" {
		return "Agent"
	}
	r, n := utf8.DecodeRuneInString(what)
	return string(unicode.ToUpper(r)) + what[n:]
}

// size writes a file size the short way: 812 bytes, 4.2 KB, 1.5 MB.
func size(n int) string { return bytesText(int64(n)) }

// scopeName is a token scope as the pages show it.
func scopeName(s registry.TokenScope) string {
	if s == registry.ScopePublish {
		return "Can publish"
	}
	return "Can read"
}

// firstLine returns the first line of s, shortened to n runes.
func firstLine(s string, n int) string {
	s, _, _ = strings.Cut(strings.TrimSpace(s), "\n")
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n-1]) + "…"
}

// invalidAttrs marks the field id as the one the page's error is about,
// when it is: invalid, described by the message, and focused.
func invalidAttrs(field, id string) template.HTMLAttr {
	if field == "" || field != id {
		return ""
	}
	return ` aria-invalid="true" aria-describedby="form-error" autofocus`
}
