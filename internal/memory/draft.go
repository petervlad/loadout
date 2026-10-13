package memory

import (
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/petervlad/loadout/internal/secretscan"
)

// fields are the parts of an entry a person writes. Draft and Revision share
// one set of checks through them.
type fields struct {
	typ         Type
	name        string
	description string
	body        string
	links       []int64
}

// check runs, in order, the secret filter on every text field, then the type,
// then the limits. The order matters: a body that is both too long and holds
// a key is refused for the key, so the person learns to rotate it. It returns
// the cleaned fields (names trimmed, links without repeats).
func (f fields) check() (fields, error) {
	for _, t := range []struct{ field, text string }{
		{"name", f.name}, {"description", f.description}, {"body", f.body},
	} {
		if err := checkText(t.field, t.text); err != nil {
			return fields{}, err
		}
	}
	for _, t := range []struct{ field, text string }{
		{"name", f.name}, {"description", f.description}, {"body", f.body},
	} {
		if err := noSecret(t.field, t.text); err != nil {
			return fields{}, err
		}
	}
	if !f.typ.Valid() {
		return fields{}, refuse(RuleType, "type", "The type must be feedback, project or reference. Personal preferences stay in your own memory.")
	}
	var err error
	if f.name, err = oneLine("name", f.name, MaxName, RuleMaxName); err != nil {
		return fields{}, err
	}
	if f.description, err = oneLine("description", f.description, MaxDescription, RuleMaxDesc); err != nil {
		return fields{}, err
	}
	if err := checkBody(f.body); err != nil {
		return fields{}, err
	}
	if len(f.body) > MaxBody {
		return fields{}, refuse(RuleMaxBody, "body",
			"The body is longer than 4 KB. Put the text in the team docs (the wiki) and save a memory that links to it.")
	}
	if f.links, err = cleanLinks(f.links); err != nil {
		return fields{}, err
	}
	return f, nil
}

// checkText refuses text a database or a client could not hold safely. It
// runs before the secret filter, which expects valid UTF-8.
func checkText(field, s string) error {
	if !utf8.ValidString(s) || strings.ContainsRune(s, 0) {
		return refuse(RuleText, field, "The %s is not valid text. Use plain UTF-8 without null characters.", field)
	}
	return nil
}

func noSecret(field, s string) error {
	if f, found := secretscan.Scan(field, s); found {
		return secretRefusal(f.Rule, field, f.Line, f.Column)
	}
	return nil
}

// oneLine trims s and checks it is not empty, is one line and is at most limit
// runes.
func oneLine(field, s string, limit int, rule string) (string, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", refuse(RuleRequired, field, "The %s cannot be empty.", field)
	}
	if strings.IndexFunc(s, notPlainLine) >= 0 {
		return "", refuse(RuleOneLine, field, "The %s must be one line of plain text, without line breaks or invisible formatting characters.", field)
	}
	if utf8.RuneCountInString(s) > limit {
		return "", refuse(rule, field, "The %s is longer than %d characters. Shorten it.", field, limit)
	}
	return s, nil
}

// notPlainLine is true for a rune that has no place in a one-line field. It
// covers the control characters, the line and paragraph separators (U+2028 and
// U+2029) that end a line in some clients, and every format character
// (unicode.Cf), which holds the bidirectional controls (U+202A to U+202E and
// U+2066 to U+2069) that make text read differently from what it says, and the
// zero-width characters.
func notPlainLine(r rune) bool {
	return unicode.IsControl(r) || unicode.Is(unicode.Cf, r) || r == '\u2028' || r == '\u2029'
}

// checkBody refuses control characters in a body other than the line break,
// the tab and the carriage return. An escape sequence in a memory would be
// printed to a terminal by a tool that shows it. The null character is
// refused before, as invalid text.
func checkBody(s string) error {
	if strings.IndexFunc(s, func(r rune) bool {
		return unicode.IsControl(r) && r != '\n' && r != '\t' && r != '\r'
	}) >= 0 {
		return refuse(RuleText, "body", "The body holds control characters, such as an escape sequence. Remove them and keep plain text.")
	}
	return nil
}

// cleanLinks drops repeats, keeping order, and checks the count and that each
// id could be an entry. Whether the entries exist is for the store.
func cleanLinks(in []int64) ([]int64, error) {
	out := make([]int64, 0, len(in))
	for _, id := range in {
		if id < 1 {
			return nil, refuse(RuleMaxLinks, "links", "A link must be the id of another memory.")
		}
		if !slices.Contains(out, id) {
			out = append(out, id)
		}
	}
	if len(out) > MaxLinks {
		return nil, refuse(RuleMaxLinks, "links", "A memory can link to at most %d others.", MaxLinks)
	}
	return out, nil
}

// CheckReason checks a retract reason: required, one line, at most MaxNote
// characters, no secret. It returns the trimmed reason.
func CheckReason(reason string) (string, error) { return checkNote("reason", reason, true) }

func checkNote(field, s string, required bool) (string, error) {
	if err := checkText(field, s); err != nil {
		return "", err
	}
	if err := noSecret(field, s); err != nil {
		return "", err
	}
	if strings.TrimSpace(s) == "" && !required {
		return "", nil
	}
	return oneLine(field, s, MaxNote, RuleMaxNote)
}

// Draft is a new entry that passed every check but the duplicate check and the
// check that its links exist. Only NewDraft builds one, so a store can trust
// the values.
type Draft struct {
	ns string
	f  fields
}

// NewDraft checks a new entry. A failure is a *Refusal that wraps
// registry.ErrInvalid and names the limit or rule. It never holds the text.
func NewDraft(ns string, typ Type, name, description, body string, links []int64) (Draft, error) {
	if strings.TrimSpace(ns) == "" {
		return Draft{}, refuse(RuleNamespace, "namespace", "A memory needs a team.")
	}
	f, err := fields{typ, name, description, body, links}.check()
	if err != nil {
		return Draft{}, err
	}
	return Draft{ns: ns, f: f}, nil
}

// Namespace is the team the entry will be saved to.
func (d Draft) Namespace() string { return d.ns }

// Type is the kind of fact.
func (d Draft) Type() Type { return d.f.typ }

// Name is the trimmed name.
func (d Draft) Name() string { return d.f.name }

// Description is the trimmed one-line description.
func (d Draft) Description() string { return d.f.description }

// Body is the body as written.
func (d Draft) Body() string { return d.f.body }

// Links returns a copy of the linked entry ids.
func (d Draft) Links() []int64 { return slices.Clone(d.f.links) }

// Change is an edit to an entry: the fields that are set replace the entry's,
// the others stay. Confirm also moves confirmed_at to now, and a Change with
// only Confirm writes a version that changes nothing else.
type Change struct {
	Type        *Type
	Name        *string
	Description *string
	Body        *string
	Links       *[]int64
	Confirm     bool
	Note        string // one line, why the entry changed
}

// Revision is a checked change applied to the entry it was made from: the full
// next snapshot. Only Change.Revise builds one.
type Revision struct {
	action Action
	f      fields
	conf   time.Time
	note   string
}

// Revise applies c to e and checks the result with the same rules as NewDraft.
// A Change that sets nothing and does not confirm is refused. A confirm-only
// Change skips the content checks, so an old entry can always be confirmed.
func (c Change) Revise(e Entry, now time.Time) (Revision, error) {
	note, err := checkNote("note", c.Note, false)
	if err != nil {
		return Revision{}, err
	}
	cur := fields{e.Type, e.Name, e.Description, e.Body, slices.Clone(e.Links)}
	set := c.Type != nil || c.Name != nil || c.Description != nil || c.Body != nil || c.Links != nil
	if !set && !c.Confirm {
		return Revision{}, refuse(RuleNoChange, "", "Nothing to change. Set a field, or set confirm to say the memory is still true.")
	}
	r := Revision{action: ActionConfirm, f: cur, conf: e.ConfirmedAt, note: note}
	if c.Confirm {
		r.conf = now
	}
	if !set {
		return r, nil
	}
	if c.Type != nil {
		cur.typ = *c.Type
	}
	if c.Name != nil {
		cur.name = *c.Name
	}
	if c.Description != nil {
		cur.description = *c.Description
	}
	if c.Body != nil {
		cur.body = *c.Body
	}
	if c.Links != nil {
		cur.links = *c.Links
	}
	if r.f, err = cur.check(); err != nil {
		return Revision{}, err
	}
	r.action = ActionUpdate
	return r, nil
}

// Action is ActionConfirm for a confirm-only change, else ActionUpdate.
func (r Revision) Action() Action { return r.action }

// Type is the kind of fact after the change.
func (r Revision) Type() Type { return r.f.typ }

// Name is the name after the change.
func (r Revision) Name() string { return r.f.name }

// Description is the description after the change.
func (r Revision) Description() string { return r.f.description }

// Body is the body after the change.
func (r Revision) Body() string { return r.f.body }

// Links returns a copy of the linked entry ids after the change.
func (r Revision) Links() []int64 { return slices.Clone(r.f.links) }

// ConfirmedAt is the entry's confirmed_at after the change.
func (r Revision) ConfirmedAt() time.Time { return r.conf }

// Note is the one-line note, possibly empty.
func (r Revision) Note() string { return r.note }

// Terms splits text into lowercase words: runs of letters and digits, repeats
// dropped, in order of first use.
func Terms(text string) []string {
	var out []string
	for w := range strings.FieldsFuncSeq(strings.ToLower(text), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	}) {
		if !slices.Contains(out, w) {
			out = append(out, w)
		}
	}
	return out
}

// Words is Terms capped at MaxQueryWords: the words of a search. Both stores
// and the duplicate check use it, so user text never reaches full-text syntax.
func Words(text string) []string {
	t := Terms(text)
	return t[:min(MaxQueryWords, len(t))]
}
