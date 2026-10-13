package pack

import (
	"path/filepath"
	"testing"
)

func TestScaffoldStartVersion(t *testing.T) {
	for kind, want := range map[Kind]string{KindAgent: "0.1.0", KindPipeline: "1.0.0"} {
		id, err := ParseID("acme/thing")
		if err != nil {
			t.Fatal(err)
		}
		p, err := Scaffold(filepath.Join(t.TempDir(), "thing"), id, kind)
		if err != nil {
			t.Fatal(err)
		}
		if got := p.Version().String(); got != want {
			t.Errorf("a new %s starts at %s, want %s", kind, got, want)
		}
	}
}
