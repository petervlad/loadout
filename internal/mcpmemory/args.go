package mcpmemory

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"unicode/utf8"
)

// userError is a sentence for the person behind the agent, safe to send back:
// it is written here and never holds text from the request.
type userError string

func (e userError) Error() string { return string(e) }

// decodeArgs reads the arguments of a tool call into dst. Unknown fields are
// refused. encoding/json errors can quote the input (a bad character, a
// field name), so none of them is passed on: each becomes a fixed sentence.
func decodeArgs(raw json.RawMessage, dst any) error {
	if len(bytes.TrimSpace(raw)) == 0 {
		raw = json.RawMessage("{}")
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	err := dec.Decode(dst)
	if err == nil {
		// Only one value may follow: {} {} is not an arguments object.
		if _, more := dec.Token(); !errors.Is(more, io.EOF) {
			return userError("The arguments must be one JSON object.")
		}
		return nil
	}
	if te, ok := errors.AsType[*json.UnmarshalTypeError](err); ok {
		if te.Field != "" {
			return userError(fmt.Sprintf("The argument %s has the wrong type. See the tool's input schema.", te.Field))
		}
		return userError("The arguments must be a JSON object.")
	}
	if strings.HasPrefix(err.Error(), "json: unknown field") {
		return userError("An argument is not one this tool takes. See the tool's input schema.")
	}
	return userError("The arguments are not valid JSON.")
}

// inRange checks an optional integer and applies its default.
func inRange(name string, v *int, def, lo, hi int) (int, error) {
	if v == nil {
		return def, nil
	}
	if *v < lo || *v > hi {
		return 0, userError(fmt.Sprintf("The argument %s must be between %d and %d.", name, lo, hi))
	}
	return *v, nil
}

// requireID checks an entry id.
func requireID(id int64) error {
	if id < 1 {
		return userError("The argument id must be the number of a memory.")
	}
	return nil
}

// oneLineArg checks a one-line text argument of at most limit runes.
func oneLineArg(name, s string, limit int) (string, error) {
	s = strings.TrimSpace(s)
	switch {
	case s == "":
		return "", userError(fmt.Sprintf("The argument %s is required.", name))
	case utf8.RuneCountInString(s) > limit:
		return "", userError(fmt.Sprintf("The argument %s is longer than %d characters.", name, limit))
	}
	return s, nil
}
