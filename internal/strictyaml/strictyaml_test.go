package strictyaml_test

import (
	"errors"
	"testing"

	"github.com/petervlad/loadout/internal/strictyaml"
)

type doc struct {
	Name string `yaml:"name"`
}

func TestDecode(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		want    string
		wantErr bool
		empty   bool
	}{
		{name: "one document", in: "name: lime\n", want: "lime"},
		{name: "leading separator", in: "---\nname: lime\n", want: "lime"},
		{name: "empty", in: "", wantErr: true, empty: true},
		{name: "only a comment", in: "# nothing\n", wantErr: true, empty: true},
		{name: "unknown field", in: "name: lime\ncolor: red\n", wantErr: true},
		{name: "second document", in: "name: lime\n---\nname: koala\n", wantErr: true},
		{name: "broken second document", in: "name: lime\n---\n: [\n", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var d doc
			err := strictyaml.Decode([]byte(tt.in), &d)
			if (err != nil) != tt.wantErr {
				t.Fatalf("Decode(%q) error = %v, want error %t", tt.in, err, tt.wantErr)
			}
			if got := errors.Is(err, strictyaml.ErrEmpty); got != tt.empty {
				t.Errorf("Decode(%q) error = %v, want ErrEmpty %t", tt.in, err, tt.empty)
			}
			if err == nil && d.Name != tt.want {
				t.Errorf("Decode(%q) name = %q, want %q", tt.in, d.Name, tt.want)
			}
		})
	}
}
