// Package wiki is a namespace's wiki page as a value: its format, its text
// and the limits a page and its history keep. The registry stores pages
// with these rules and the CLI checks a file with them before it sends it,
// so one place says what a valid page is.
package wiki

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"
)

// Format is how a page is written. It is stored, so it is a string.
type Format string

// Page formats. MDX is stored as written, but its components are never
// run: they show as code.
const (
	Markdown Format = "md"
	MDX      Format = "mdx"
)

// Valid reports whether f is a known format.
func (f Format) Valid() bool { return f == Markdown || f == MDX }

// File is the default file name of a page in this format: wiki.md,
// wiki.mdx.
func (f Format) File() string {
	if f == MDX {
		return "wiki.mdx"
	}
	return "wiki.md"
}

// FormatOf reads the format from a file name: .md and .markdown are
// Markdown, .mdx is MDX, in any case.
func FormatOf(name string) (Format, error) {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".md", ".markdown":
		return Markdown, nil
	case ".mdx":
		return MDX, nil
	}
	return "", fmt.Errorf("%w: %s is not a page; name it .md, .markdown or .mdx", ErrFormat, name)
}

// Limits of a page and its history.
const (
	MaxSize      = 256 << 10 // bytes of one page, after line endings are normalised
	MaxNote      = 200       // runes of a save's note
	MaxRevisions = 200       // revisions kept per namespace; older ones are pruned
)

// Errors of NewDoc and FormatOf. The registry wraps them as invalid
// requests.
var (
	ErrTooLarge = errors.New("the page is too large")
	ErrNotText  = errors.New("the page is not text")
	ErrFormat   = errors.New("unknown page format")
)

// Doc is one page's text and format. Built only by NewDoc, never changed
// after. The zero Doc is the empty Markdown page.
type Doc struct {
	format Format
	text   string
}

// NewDoc checks and normalises a page: a known format, UTF-8 without NUL,
// CRLF and lone CR turned into LF (browsers send textarea text with CRLF),
// at most MaxSize bytes.
func NewDoc(f Format, text string) (Doc, error) {
	if !f.Valid() {
		return Doc{}, fmt.Errorf("%w %q; want md or mdx", ErrFormat, f)
	}
	if !utf8.ValidString(text) || strings.ContainsRune(text, 0) {
		return Doc{}, fmt.Errorf("%w: save it as UTF-8 text without NUL bytes", ErrNotText)
	}
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	if len(text) > MaxSize {
		return Doc{}, fmt.Errorf("%w: it is %d bytes and the limit is %d (256 KB); split it or shorten it", ErrTooLarge, len(text), MaxSize)
	}
	return Doc{format: f, text: text}, nil
}

// Format is the page's format, Markdown for the zero Doc.
func (d Doc) Format() Format {
	if d.format == "" {
		return Markdown
	}
	return d.format
}

// Text is the page's text, with LF line endings.
func (d Doc) Text() string { return d.text }

// Size is the length of the text in bytes.
func (d Doc) Size() int { return len(d.text) }

// Equal reports whether d and o have the same format and text.
func (d Doc) Equal(o Doc) bool { return d.Format() == o.Format() && d.text == o.text }

// Info is a revision without its text, for history lists.
type Info struct {
	Namespace string
	// Seq is 1, 2, 3 ... per namespace; 0 means nothing written yet.
	Seq          int
	Format       Format
	Size         int
	AuthorUserID string
	// Author is the author's email when they saved, kept after the user
	// is deleted.
	Author    string
	Note      string
	CreatedAt time.Time
}

// Revision is one saved version of a page.
type Revision struct {
	Info
	Doc Doc
}
