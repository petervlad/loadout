// Package guide_test checks docs/CHANGES.md, the source of the What's new
// page, without pandoc: CI never builds the page, so the format and the
// committed HTML are checked here instead.
package guide_test

import (
	"fmt"
	"html"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

// day is one deploy day of the file: its date and how many items of each tag.
type day struct {
	date            time.Time
	features, fixes int
}

// changes is what the parser keeps of a valid file.
type changes struct {
	days []day
	// texts holds each item as written, without its tag, wrapped lines
	// joined by one space.
	texts []string
}

func (c changes) items() (features, fixes int) {
	for _, d := range c.days {
		features += d.features
		fixes += d.fixes
	}
	return features, fixes
}

// emDash is U+2014, written as a number so this file itself has none.
const emDash = rune(0x2014)

var marker = regexp.MustCompile(`^<!-- through [0-9a-f]{7,40} -->$`)

// parser reads the file line by line; each field is the state the next line
// is judged against.
type parser struct {
	out       changes
	title     bool
	month     time.Time // zero before the first month heading
	monthDays int
	cur       *day // nil outside a day section
	wantMark  bool // the line right after a day heading must be its marker
	inItem    bool // indented lines continue the current item
	item      strings.Builder
}

// parseChanges checks the grammar described in docs/plans/whats-new.md and
// returns the first problem, with its line number.
func parseChanges(text string) (changes, error) {
	p := &parser{}
	for i, line := range strings.Split(text, "\n") {
		if err := p.line(line); err != nil {
			return changes{}, fmt.Errorf("line %d: %w", i+1, err)
		}
	}
	if err := p.end(); err != nil {
		return changes{}, err
	}
	return p.out, nil
}

func (p *parser) line(line string) error {
	if strings.ContainsRune(line, emDash) {
		return fmt.Errorf("em-dash in %q", line)
	}
	if p.wantMark {
		if !marker.MatchString(line) {
			return fmt.Errorf("want <!-- through HASH --> right under the date, got %q", line)
		}
		p.wantMark = false
		return nil
	}
	switch {
	case strings.HasPrefix(line, "# "):
		if p.title {
			return fmt.Errorf("a second title %q", line)
		}
		p.title = true
		return nil
	case !p.title:
		return fmt.Errorf("the file must start with a # title, got %q", line)
	case strings.HasPrefix(line, "## "):
		return p.startMonth(strings.TrimPrefix(line, "## "))
	case strings.HasPrefix(line, "### "):
		return p.startDay(strings.TrimPrefix(line, "### "))
	case p.month.IsZero():
		return nil // the intro is free text
	case strings.TrimSpace(line) == "":
		return nil
	case p.cur == nil:
		return fmt.Errorf("text outside a day section: %q", line)
	case strings.HasPrefix(line, "- "):
		if err := p.endItem(); err != nil {
			return err
		}
		return p.startItem(line)
	case p.inItem && strings.HasPrefix(line, "  "):
		p.item.WriteString(" " + strings.TrimSpace(line))
		return nil
	}
	return fmt.Errorf("want an item (- Feature: or - Fix:), got %q", line)
}

func (p *parser) startMonth(s string) error {
	m, err := time.Parse("January 2006", s)
	if err != nil {
		return fmt.Errorf("month heading %q is not Month YYYY", s)
	}
	if err := p.endMonth(); err != nil {
		return err
	}
	if !p.month.IsZero() && !m.Before(p.month) {
		return fmt.Errorf("month %q is not older than the one above it", s)
	}
	p.month, p.monthDays = m, 0
	return nil
}

func (p *parser) startDay(s string) error {
	d, err := time.Parse("2 January 2006", s)
	if err != nil {
		return fmt.Errorf("day heading %q is not D Month YYYY", s)
	}
	if p.month.IsZero() || d.Year() != p.month.Year() || d.Month() != p.month.Month() {
		return fmt.Errorf("day %q is not under its own month", s)
	}
	if err := p.endDay(); err != nil {
		return err
	}
	if n := len(p.out.days); n > 0 && !d.Before(p.out.days[n-1].date) {
		return fmt.Errorf("day %q is not older than the one above it", s)
	}
	p.out.days = append(p.out.days, day{date: d})
	p.cur, p.wantMark = &p.out.days[len(p.out.days)-1], true
	p.monthDays++
	return nil
}

func (p *parser) startItem(line string) error {
	switch {
	case strings.HasPrefix(line, "- Feature: "):
		if p.cur.fixes > 0 {
			return fmt.Errorf("a feature after a fix: %q", line)
		}
		p.cur.features++
	case strings.HasPrefix(line, "- Fix: "):
		p.cur.fixes++
	default:
		return fmt.Errorf("item without - Feature: or - Fix: %q", line)
	}
	p.inItem = true
	p.item.Reset()
	p.item.WriteString(line)
	return nil
}

func (p *parser) endItem() error {
	if !p.inItem {
		return nil
	}
	p.inItem = false
	s := p.item.String()
	if !strings.HasSuffix(s, ".") {
		return fmt.Errorf("item does not end with a full stop: %q", s)
	}
	_, text, _ := strings.Cut(s, ": ")
	p.out.texts = append(p.out.texts, text)
	return nil
}

func (p *parser) endDay() error {
	if err := p.endItem(); err != nil {
		return err
	}
	if p.cur != nil && p.cur.features+p.cur.fixes == 0 {
		return fmt.Errorf("%s has no items", p.cur.date.Format("2 January 2006"))
	}
	p.cur = nil
	return nil
}

func (p *parser) endMonth() error {
	if err := p.endDay(); err != nil {
		return err
	}
	if !p.month.IsZero() && p.monthDays == 0 {
		return fmt.Errorf("%s has no days", p.month.Format("January 2006"))
	}
	return nil
}

func (p *parser) end() error {
	if p.wantMark {
		return fmt.Errorf("the last day has no marker")
	}
	if err := p.endMonth(); err != nil {
		return err
	}
	if !p.title {
		return fmt.Errorf("no # title")
	}
	if len(p.out.days) == 0 {
		return fmt.Errorf("no day sections")
	}
	return nil
}

const example = `# What's new in loadout

What changed in loadout, newest first. Each update lists what it added and what it
fixed. The [user guide](../) explains how to use each one.

## October 2026

### 4 October 2026
<!-- through b7efd71 -->

- Feature: A proposal made from an older release now says it is behind, on its page,
  in the reviews list and in ` + "`loadout review list`" + `.
- Feature: Approving a proposal that is behind asks first.
- Fix: The diff of a proposal now compares with the release it was made from.

### 3 October 2026
<!-- through 1a2b3c4 -->

- Fix: Sign-in links no longer expire early.

## September 2026

### 29 September 2026
<!-- through 0123456789abcdef0123456789abcdef01234567 -->

- Feature: Teams can keep a wiki.
`

func TestParseChanges(t *testing.T) {
	c, err := parseChanges(example)
	if err != nil {
		t.Fatalf("the example is refused: %v", err)
	}
	if f, x := c.items(); len(c.days) != 3 || f != 3 || x != 2 {
		t.Fatalf("got %d days, %d features, %d fixes; want 3, 3, 2", len(c.days), f, x)
	}

	tests := []struct {
		name, old, new string
	}{
		{"untagged item", "- Fix: Sign-in", "- Sign-in"},
		{"tag without colon", "- Feature: Teams", "- Feature Teams"},
		{"unknown tag", "- Fix: Sign-in", "- Change: Sign-in"},
		{"no full stop", "expire early.", "expire early"},
		{"no full stop on a wrapped item", "`loadout review list`.", "`loadout review list`"},
		{"em-dash", "expire early.", "expire early " + string(emDash) + " at last."},
		{"months out of order", "## September 2026", "## November 2026"},
		{"days out of order", "### 3 October 2026", "### 5 October 2026"},
		{"duplicate date", "### 3 October 2026", "### 4 October 2026"},
		{"day under the wrong month", "### 29 September 2026", "### 29 August 2026"},
		{"bad month heading", "## October 2026", "## Oct 2026"},
		{"bad day heading", "### 3 October 2026", "### October 3, 2026"},
		{"missing marker", "<!-- through 1a2b3c4 -->\n", ""},
		{"marker not right under the date", "### 3 October 2026\n", "### 3 October 2026\n\n"},
		{"short hash", "through 1a2b3c4", "through 1a2b3c"},
		{"hash not hex", "through 1a2b3c4", "through 1a2b3cz"},
		{"empty day", "\n- Fix: Sign-in links no longer expire early.\n", "\n"},
		{"empty month", "### 29 September 2026\n<!-- through 0123456789abcdef0123456789abcdef01234567 -->\n\n- Feature: Teams can keep a wiki.\n", ""},
		{"fix before feature", "- Feature: Approving a proposal that is behind asks first.\n- Fix: The diff of a proposal now compares with the release it was made from.", "- Fix: The diff of a proposal now compares with the release it was made from.\n- Feature: Approving a proposal that is behind asks first."},
		{"second title", "## September 2026", "# More\n\n## September 2026"},
		{"no title", "# What's new in loadout\n", ""},
		{"stray text in a day", "- Fix: Sign-in links no longer expire early.", "Some words.\n- Fix: Sign-in links no longer expire early."},
		{"no days", "## October" + strings.SplitN(example, "## October", 2)[1], ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			in := strings.Replace(example, tt.old, tt.new, 1)
			if in == example {
				t.Fatalf("the replacement of %q changed nothing", tt.old)
			}
			if _, err := parseChanges(in); err == nil {
				t.Errorf("accepted:\n%s", in)
			}
		})
	}
}

// TestChangesFile checks the real file, and that the committed page was
// rebuilt after the file last changed.
func TestChangesFile(t *testing.T) {
	src, err := os.ReadFile(filepath.Join("..", "CHANGES.md"))
	if err != nil {
		t.Fatal(err)
	}
	c, err := parseChanges(string(src))
	if err != nil {
		t.Fatalf("docs/CHANGES.md: %v", err)
	}

	page, err := os.ReadFile(filepath.Join("..", "..", "deploy", "site", "docs", "whats-new", "index.html"))
	if err != nil {
		t.Fatalf("%v: run sh docs/guide/build.sh", err)
	}
	built := string(page)
	stale := "deploy/site/docs/whats-new is out of date: run sh docs/guide/build.sh"
	for _, d := range c.days {
		if !strings.Contains(built, `datetime="`+d.date.Format(time.DateOnly)+`"`) {
			t.Errorf("%s: %s is missing", stale, d.date.Format("2 January 2006"))
		}
	}
	features, fixes := c.items()
	if n := strings.Count(built, `<span class="tag tag-feature">Feature</span>`); n != features {
		t.Errorf("%s: the page has %d features, the file %d", stale, n, features)
	}
	if n := strings.Count(built, `<span class="tag tag-fix">Fix</span>`); n != fixes {
		t.Errorf("%s: the page has %d fixes, the file %d", stale, n, fixes)
	}
	if strings.Contains(built, "<!-- through") {
		t.Errorf("the page shows the deploy markers; changes.lua should drop them")
	}
	// The words too: an item reworded after the last build must fail, not
	// only one added or removed.
	words := plainHTML(built)
	for _, text := range c.texts {
		if w := plainMarkdown(text); !strings.Contains(words, w) {
			t.Errorf("%s: the page does not say %q", stale, w)
		}
	}
}

var (
	tags       = regexp.MustCompile(`<[^>]*>`)
	mdLink     = regexp.MustCompile(`\[([^\]]*)\]\([^)]*\)`)
	mdEmphasis = regexp.MustCompile("\\*+|`")
	spaces     = regexp.MustCompile(`\s+`)
)

// plainHTML is the text of a page, as the item sentences read on it.
func plainHTML(s string) string {
	return spaces.ReplaceAllString(html.UnescapeString(tags.ReplaceAllString(s, "")), " ")
}

// plainMarkdown is the text of an item without the Markdown the file uses
// in items: links, emphasis and code.
func plainMarkdown(s string) string {
	s = mdLink.ReplaceAllString(s, "$1")
	return spaces.ReplaceAllString(mdEmphasis.ReplaceAllString(s, ""), " ")
}
