// Package strictyaml decodes YAML files that loadout owns, refusing anything
// it would otherwise silently ignore.
package strictyaml

import (
	"bytes"
	"errors"
	"io"

	"go.yaml.in/yaml/v3"
)

// ErrEmpty means the input holds no YAML document.
var ErrEmpty = errors.New("no YAML document")

// errMultiple means the input holds a second document, which would be ignored.
var errMultiple = errors.New("more than one YAML document")

// Decode decodes the single YAML document in data into v. Unknown fields and
// a second document are errors; empty input returns ErrEmpty.
func Decode(data []byte, v any) error {
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(v); errors.Is(err, io.EOF) {
		return ErrEmpty
	} else if err != nil {
		return err
	}
	var extra yaml.Node
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		if err != nil {
			return err
		}
		return errMultiple
	}
	return nil
}
