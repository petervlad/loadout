// Package jsonedit changes a JSON document in place: it adds or removes one
// member or element and leaves every other byte as it was, so a person's
// file keeps its order, its spacing, its numbers and its strings exactly.
//
// It exists for files other programs own, such as Claude Code's
// settings.json, where loadout adds one entry and must never reformat or
// lose anything else. Input that is not valid JSON (comments, trailing
// commas, duplicate names, invalid UTF-8) is refused, never repaired.
package jsonedit

import (
	"bytes"
	"encoding/json"
	"encoding/json/jsontext"
	"errors"
	"fmt"
	"io"
)

// Kind is the kind of a JSON value: '{', '[', '"', '0', 't', 'f' or 'n',
// as jsontext reports it.
type Kind = jsontext.Kind

// Node is one value in a document, with where it is.
type Node struct {
	Kind Kind
	// Start and End are byte offsets of the value in the document.
	Start, End int
	// Members are an object's members, in order.
	Members []Member
	// Elems are an array's elements, in order.
	Elems []Node
}

// Member is one name and value of an object.
type Member struct {
	Name string
	// Start is the offset of the name; the member ends with Value.End.
	Start int
	Value Node
}

// Get returns the value of the member called name.
func (n Node) Get(name string) (Node, bool) {
	for _, m := range n.Members {
		if m.Name == name {
			return m.Value, true
		}
	}
	return Node{}, false
}

// Index returns the position of the member called name, or -1.
func (n Node) Index(name string) int {
	for i, m := range n.Members {
		if m.Name == name {
			return i
		}
	}
	return -1
}

// Len is the number of members or elements.
func (n Node) Len() int { return len(n.Members) + len(n.Elems) }

// span returns where child i starts and ends.
func (n Node) span(i int) (start, end int) {
	if n.Kind == '{' {
		return n.Members[i].Start, n.Members[i].Value.End
	}
	return n.Elems[i].Start, n.Elems[i].End
}

// Raw returns the bytes of n in data.
func (n Node) Raw(data []byte) []byte { return data[n.Start:n.End] }

// String returns a string value's text; "" for any other kind.
func (n Node) String(data []byte) string {
	var s string
	if n.Kind != '"' || json.Unmarshal(n.Raw(data), &s) != nil {
		return ""
	}
	return s
}

// ErrInvalid is returned for input that is not one valid JSON value.
var ErrInvalid = errors.New("not valid JSON")

// Parse reads the one JSON value in data, with the position of every
// value in it.
func Parse(data []byte) (Node, error) {
	p := parser{data: data, dec: jsontext.NewDecoder(bytes.NewReader(data))}
	n, err := p.value()
	if err != nil {
		return Node{}, fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	if _, err := p.dec.ReadToken(); !errors.Is(err, io.EOF) {
		return Node{}, fmt.Errorf("%w: more after the first value", ErrInvalid)
	}
	return n, nil
}

type parser struct {
	data []byte
	dec  *jsontext.Decoder
}

// next is where the next token starts: past white space and the ',' or
// ':' before it. The decoder has checked the syntax already.
func (p *parser) next() int {
	i := int(p.dec.InputOffset())
	for i < len(p.data) {
		switch p.data[i] {
		case ' ', '\t', '\n', '\r', ',', ':':
			i++
		default:
			return i
		}
	}
	return i
}

func (p *parser) value() (Node, error) {
	start := p.next()
	switch kind := p.dec.PeekKind(); kind {
	case '{', '[':
		if _, err := p.dec.ReadToken(); err != nil {
			return Node{}, err
		}
		n := Node{Kind: kind, Start: start}
		for p.dec.PeekKind() != kind+2 { // '}' is '{'+2, ']' is '['+2
			if err := p.child(&n); err != nil {
				return Node{}, err
			}
		}
		if _, err := p.dec.ReadToken(); err != nil {
			return Node{}, err
		}
		n.End = int(p.dec.InputOffset())
		return n, nil
	case 0:
		_, err := p.dec.ReadToken()
		if err == nil || errors.Is(err, io.EOF) {
			err = io.ErrUnexpectedEOF
		}
		return Node{}, err
	default:
		v, err := p.dec.ReadValue()
		if err != nil {
			return Node{}, err
		}
		end := int(p.dec.InputOffset())
		return Node{Kind: kind, Start: end - len(v), End: end}, nil
	}
}

// child reads one member of an object or element of an array into n.
func (p *parser) child(n *Node) error {
	if n.Kind == '[' {
		v, err := p.value()
		if err != nil {
			return err
		}
		n.Elems = append(n.Elems, v)
		return nil
	}
	nameStart := p.next()
	tok, err := p.dec.ReadToken()
	if err != nil {
		return err
	}
	name := tok.String() // before the next read voids tok
	v, err := p.value()
	if err != nil {
		return err
	}
	n.Members = append(n.Members, Member{Name: name, Start: nameStart, Value: v})
	return nil
}

// Append adds a member (for an object, with name) or an element (for an
// array, name ignored) at the end of container, written on its own line
// at the indentation of its siblings. value is marshalled with
// encoding/json and indented to match.
func Append(data []byte, container Node, name string, value any) ([]byte, error) {
	if container.Kind != '{' && container.Kind != '[' {
		return nil, errors.New("jsonedit: append to a value that is not an object or array")
	}
	unit := indentUnit(data)
	outer := lineIndent(data, container.Start)
	inner := outer + unit
	if container.Len() > 0 {
		start, _ := container.span(0)
		if lead := lineIndent(data, start); lead != "" || startsLine(data, start) {
			inner = lead
		}
	}
	body, err := Marshal(value, inner, unit)
	if err != nil {
		return nil, err
	}
	if container.Kind == '{' {
		key, err := json.Marshal(name)
		if err != nil {
			return nil, err
		}
		body = append(append(key, ": "...), body...)
	}
	var out bytes.Buffer
	if container.Len() == 0 {
		// Replace whatever white space the empty container held.
		out.Write(data[:container.Start+1])
		out.WriteString("\n" + inner)
		out.Write(body)
		out.WriteString("\n" + outer)
		out.Write(data[container.End-1:])
		return out.Bytes(), nil
	}
	_, lastEnd := container.span(container.Len() - 1)
	out.Write(data[:lastEnd])
	out.WriteString(",\n" + inner)
	out.Write(body)
	out.Write(data[lastEnd:])
	return out.Bytes(), nil
}

// Replace puts value in place of n, indented like n's line.
func Replace(data []byte, n Node, value any) ([]byte, error) {
	body, err := Marshal(value, lineIndent(data, n.Start), indentUnit(data))
	if err != nil {
		return nil, err
	}
	out := make([]byte, 0, len(data)+len(body))
	out = append(out, data[:n.Start]...)
	out = append(out, body...)
	return append(out, data[n.End:]...), nil
}

// Remove takes member or element i out of container, with the comma and
// white space that joined it to its siblings, so removing what Append
// added gives back the bytes from before.
func Remove(data []byte, container Node, i int) []byte {
	var from, to int
	switch start, end := container.span(i); {
	case container.Len() == 1:
		from, to = container.Start+1, container.End-1
	case i > 0:
		_, prevEnd := container.span(i - 1)
		from, to = prevEnd, end
	default:
		next, _ := container.span(1)
		from, to = start, next
	}
	out := make([]byte, 0, len(data)-(to-from))
	out = append(out, data[:from]...)
	return append(out, data[to:]...)
}

// Marshal is json.MarshalIndent without escaping <, > and &, which shell
// commands are full of and which people read in these files.
func Marshal(v any, prefix, indent string) ([]byte, error) {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	enc.SetIndent(prefix, indent)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(b.Bytes(), []byte("\n")), nil
}

// lineIndent is the white space at the start of the line holding pos.
func lineIndent(data []byte, pos int) string {
	start := bytes.LastIndexByte(data[:pos], '\n') + 1
	end := start
	for end < len(data) && (data[end] == ' ' || data[end] == '\t') {
		end++
	}
	return string(data[start:end])
}

// startsLine reports whether only white space comes before pos on its line.
func startsLine(data []byte, pos int) bool {
	start := bytes.LastIndexByte(data[:pos], '\n') + 1
	return len(bytes.TrimLeft(data[start:pos], " \t")) == 0
}

// indentUnit guesses one level of indentation from the first indented
// line; two spaces, as Claude Code writes, when there is none.
func indentUnit(data []byte) string {
	for line := range bytes.SplitSeq(data, []byte("\n")) {
		trimmed := bytes.TrimLeft(line, " \t")
		if n := len(line) - len(trimmed); n > 0 && len(trimmed) > 0 {
			return string(line[:n])
		}
	}
	return "  "
}
