package remote

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
)

func FuzzStoreLoad(f *testing.F) {
	seeds := []string{
		"default: office\nremotes:\n  - name: office\n    url: http://10.0.4.12:7420\n",
		"remotes:\n  - name: cloud\n    url: https://reg.example.com\n    ca_file: /etc/ca.pem\n",
		"remotes: []\n",
		"",
		"---\n",
		"default: x\nremotes:\n- {name: a, url: b}\n- {name: a, url: b}\n",
		"remotes: {name: a}\n",
		"colour: red\n",
		"remotes:\n  - name: a\n    token: secret\n",
		"a: &a [*a, *a]\n",
		"remotes: !!binary aGVsbG8=\n",
		"default: \"\\u00e9\\t\"\nremotes: ~\n",
		"- 1\n- 2\n",
	}
	for _, s := range seeds {
		f.Add([]byte(s))
	}

	f.Fuzz(func(t *testing.T, data []byte) {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "config.yaml"), data, 0o600); err != nil {
			t.Fatal(err)
		}
		s := NewStore(dir, func(string) string { return "" })
		c, err := s.Load()
		if err != nil {
			return
		}
		if c == nil {
			t.Fatalf("Load(%q) = nil, nil; want a config or an error", data)
		}
		if err := s.Save(c); err != nil {
			t.Fatalf("Save(Load(%q)) error: %v", data, err)
		}
		again, err := s.Load()
		if err != nil {
			t.Fatalf("Load(Save(Load(%q))) error: %v", data, err)
		}
		if diff := cmp.Diff(c, again, cmpopts.EquateEmpty()); diff != "" {
			t.Errorf("Load after Save mismatch (-want +got):\n%s", diff)
		}
	})
}
