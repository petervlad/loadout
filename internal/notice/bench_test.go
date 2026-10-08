package notice

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/petervlad/loadout/internal/install"
	"github.com/petervlad/loadout/internal/lock"
	"github.com/petervlad/loadout/internal/pack"
	"github.com/petervlad/loadout/internal/semver"
	"github.com/petervlad/loadout/internal/syncstate"
	"github.com/petervlad/loadout/internal/target"
	"github.com/petervlad/loadout/internal/target/claudecode"
)

// BenchmarkList is the session hook's work on a big machine: 50 installs,
// one of them a knowledge base of 5,000 files, every one held back. The
// plan's budget is 20 ms per call.
func BenchmarkList(b *testing.B) {
	home := b.TempDir()
	if err := os.Mkdir(filepath.Join(home, ".claude"), 0o755); err != nil {
		b.Fatal(err)
	}
	state := filepath.Join(home, ".loadout")
	in := install.New(lock.NewStore(filepath.Join(state, "lock.yaml"), home), slog.New(slog.DiscardHandler), claudecode.New())
	scope := target.Scope{Kind: target.ScopeUser, Root: home}
	var st syncstate.State
	add := func(p *pack.Pack) {
		res, err := in.Install(b.Context(), p, install.Options{Scope: scope})
		if err != nil {
			b.Fatal(err)
		}
		f := res.Entry.Files[0]
		if err := os.WriteFile(filepath.Join(home, filepath.FromSlash(f.Path)), []byte("edited"), 0o644); err != nil { //nolint:gosec // bench temp dir
			b.Fatal(err)
		}
		st.Put(syncstate.Behind{
			ID: p.ID(), Scope: scope, Version: p.Version(), Latest: semver.New(9, 0, 0),
			Held: []syncstate.Blocker{{Path: f.Path, Reason: string(install.ReasonModified)}},
		})
	}
	for i := range 49 {
		add(benchPack(b, fmt.Sprintf("agent-%d", i), map[string][]byte{
			pack.ManifestFile: fmt.Appendf(nil, "name: lime/agent-%d\nkind: agent\nversion: 1.0.0\ndescription: d\n", i),
			pack.AgentFile:    fmt.Appendf(nil, "---\nname: agent-%d\ndescription: d\n---\nprompt\n", i),
		}))
	}
	docs := map[string][]byte{pack.ManifestFile: []byte("name: lime/runbooks\nkind: kb\nversion: 1.0.0\ndescription: Runbooks.\n")}
	for i := range 4999 {
		docs[fmt.Sprintf("docs/d%02d/doc-%d.md", i%50, i)] = fmt.Appendf(nil, "# Doc %d\n\ntext\n", i)
	}
	add(benchPack(b, "runbooks", docs))
	memory := syncstate.NewStore(filepath.Join(state, "syncstate.yaml"), home)
	if err := memory.Update(b.Context(), func(s *syncstate.State) error {
		for _, r := range st.All() {
			s.Put(r)
		}
		return nil
	}); err != nil {
		b.Fatal(err)
	}
	svc := New(memory, in)
	for b.Loop() {
		ns, err := svc.List(b.Context(), Filter{Kinds: []Kind{Held}, Dir: home})
		if err != nil || len(ns) != 50 {
			b.Fatalf("List() = %d notices, %v; want 50", len(ns), err)
		}
	}
}

func benchPack(b *testing.B, name string, files map[string][]byte) *pack.Pack {
	b.Helper()
	p, err := pack.FromFiles(files)
	if err != nil {
		b.Fatalf("%s: %v", name, err)
	}
	return p
}
