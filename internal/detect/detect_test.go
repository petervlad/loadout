package detect

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/petervlad/loadout/internal/target"
)

type fakeTool struct{ name, dir string }

func (f fakeTool) Name() string                      { return f.name }
func (f fakeTool) MarkerDir(target.ScopeKind) string { return f.dir }

func TestTargets(t *testing.T) {
	home := t.TempDir()
	for _, dir := range []string{".alpha", ".gamma"} {
		if err := os.Mkdir(filepath.Join(home, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	d := New(fakeTool{"gamma", ".gamma"}, fakeTool{"beta", ".beta"}, fakeTool{"alpha", ".alpha"})
	scope := target.Scope{Kind: target.ScopeUser, Root: home}

	tests := []struct {
		name      string
		supported []string
		want      []string
		wantErr   string
	}{
		{name: "any", want: []string{"alpha", "gamma"}},
		{name: "limited", supported: []string{"gamma", "beta"}, want: []string{"gamma"}},
		{name: "supported but absent", supported: []string{"beta"}, wantErr: "no ~/.beta"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := d.Targets(scope, tt.supported)
			if tt.wantErr != "" {
				if !errors.Is(err, ErrNoTool) || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("Targets(%v) error = %v, want one containing %q", tt.supported, err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("Targets(%v) mismatch (-want +got):\n%s", tt.supported, diff)
			}
		})
	}
}

func TestTargetsNoneFound(t *testing.T) {
	d := New(fakeTool{"beta", ".beta"}, fakeTool{"alpha", ".alpha"})
	_, err := d.Targets(target.Scope{Kind: target.ScopeUser, Root: t.TempDir()}, nil)
	if !errors.Is(err, ErrNoTool) || !strings.Contains(err.Error(), "looked for ~/.alpha, ~/.beta") {
		t.Errorf("Targets() error = %v, want the folders looked for, sorted by tool", err)
	}
}
