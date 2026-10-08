package registry_test

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/petervlad/loadout/internal/registry"
	"github.com/petervlad/loadout/internal/registry/registrytest"
	"github.com/petervlad/loadout/internal/wiki"
)

var wikiNow = time.Date(2026, 10, 3, 10, 14, 0, 0, time.UTC)

// keepSpy records the keep a save passes to the store.
type keepSpy struct {
	*registrytest.MemStore
	keep int
}

func (k *keepSpy) PutWikiRevision(ctx context.Context, rev wiki.Revision, base, keep int) (wiki.Info, error) {
	k.keep = keep
	return k.MemStore.PutWikiRevision(ctx, rev, base, keep)
}

// newWiki returns a wiki service over a store with namespace lime, its
// wiki on.
func newWiki(t *testing.T) (*registry.WikiService, *keepSpy) {
	t.Helper()
	mem := registrytest.NewMemStore()
	if err := mem.CreateNamespace(t.Context(), registry.Namespace{Name: "lime", Kind: registry.KindTeam, CreatedAt: wikiNow, Wiki: true}); err != nil {
		t.Fatal(err)
	}
	spy := &keepSpy{MemStore: mem}
	return registry.NewWikiService(spy, mem, func() time.Time { return wikiNow }, slog.New(slog.DiscardHandler)), spy
}

func doc(t *testing.T, text string) wiki.Doc {
	t.Helper()
	d, err := wiki.NewDoc(wiki.Markdown, text)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func edit(t *testing.T, base int, text string) registry.WikiEdit {
	t.Helper()
	return registry.WikiEdit{Base: base, Doc: doc(t, text), Note: "a note", AuthorUserID: "u-dana", Author: "dana@acme.example"}
}

func TestWikiSaveAndRead(t *testing.T) {
	s, spy := newWiki(t)
	ctx := t.Context()

	empty, err := s.Revision(ctx, "lime", 0)
	if err != nil || empty.Seq != 0 || empty.Doc.Text() != "" || empty.Format != wiki.Markdown {
		t.Fatalf("Revision() of an empty page = %+v, %v; want revision 0", empty, err)
	}
	if got, err := s.Revisions(ctx, "lime"); err != nil || len(got) != 0 {
		t.Errorf("Revisions() of an empty page = %+v, %v", got, err)
	}

	info, saved, err := s.Save(ctx, "lime", edit(t, 0, "# Lime\n"))
	if err != nil || !saved {
		t.Fatalf("Save() = %+v, %v, %v", info, saved, err)
	}
	want := wiki.Info{Namespace: "lime", Seq: 1, Format: wiki.Markdown, Size: 7, AuthorUserID: "u-dana", Author: "dana@acme.example", Note: "a note", CreatedAt: wikiNow}
	if info != want {
		t.Errorf("Save() = %+v, want %+v", info, want)
	}
	if spy.keep != wiki.MaxRevisions {
		t.Errorf("Save() keeps %d revisions, want wiki.MaxRevisions", spy.keep)
	}
	rev, err := s.Revision(ctx, "lime", 0)
	if err != nil || rev.Info != want || rev.Doc.Text() != "# Lime\n" {
		t.Errorf("Revision(0) = %+v %q, %v", rev.Info, rev.Doc.Text(), err)
	}
	if _, err := s.Revision(ctx, "lime", 2); !errors.Is(err, registry.ErrNotFound) {
		t.Errorf("Revision(2) error = %v, want ErrNotFound", err)
	}
	if _, err := s.Revision(ctx, "lime", -1); !errors.Is(err, registry.ErrInvalid) {
		t.Errorf("Revision(-1) error = %v, want ErrInvalid", err)
	}

	// The same text again writes nothing and answers the base revision.
	again, saved, err := s.Save(ctx, "lime", edit(t, 1, "# Lime\r\n"))
	if err != nil || saved || again.Seq != 1 {
		t.Errorf("Save(unchanged) = %+v, %v, %v; want revision 1, not saved", again, saved, err)
	}
	// A new format is a change even with the same text.
	mdx, err := wiki.NewDoc(wiki.MDX, "# Lime\n")
	if err != nil {
		t.Fatal(err)
	}
	if info, saved, err := s.Save(ctx, "lime", registry.WikiEdit{Base: 1, Doc: mdx}); err != nil || !saved || info.Format != wiki.MDX {
		t.Errorf("Save(as MDX) = %+v, %v, %v; want revision 2 in MDX", info, saved, err)
	}
}

func TestWikiStale(t *testing.T) {
	s, _ := newWiki(t)
	ctx := t.Context()
	for base := range 2 {
		if _, _, err := s.Save(ctx, "lime", edit(t, base, strings.Repeat("x", base+1))); err != nil {
			t.Fatal(err)
		}
	}
	_, saved, err := s.Save(ctx, "lime", edit(t, 1, "mine"))
	if !errors.Is(err, registry.ErrStale) || saved {
		t.Fatalf("Save(on revision 1 of 2) = %v, %v; want ErrStale", saved, err)
	}
	stale, ok := errors.AsType[*registry.StaleError](err)
	if !ok || stale.Base != 1 || stale.Newest.Seq != 2 || stale.Newest.Author != "dana@acme.example" {
		t.Errorf("stale error = %#v, want base 1 and newest revision 2 by dana", stale)
	}
	const msg = "the wiki of lime changed since revision 1: revision 2 by dana@acme.example (2026-10-03 10:14 UTC) is the newest; pull it, merge your changes and save again"
	if err.Error() != msg {
		t.Errorf("stale message =\n %s\nwant\n %s", err, msg)
	}
	// The unchanged text of an old revision is still stale: saving it would
	// undo the newer one.
	if _, _, err := s.Save(ctx, "lime", edit(t, 1, "xx")); !errors.Is(err, registry.ErrStale) {
		t.Errorf("Save(unchanged on an old base) error = %v, want ErrStale", err)
	}
	if _, _, err := s.Save(ctx, "lime", edit(t, 7, "future")); !errors.Is(err, registry.ErrInvalid) {
		t.Errorf("Save(on a revision that does not exist) error = %v, want ErrInvalid", err)
	}
}

func TestWikiRules(t *testing.T) {
	tests := []struct {
		name string
		edit func(e *registry.WikiEdit)
		want error
	}{
		{"note at the limit", func(e *registry.WikiEdit) { e.Note = strings.Repeat("ü", wiki.MaxNote) }, nil},
		{"note too long", func(e *registry.WikiEdit) { e.Note = strings.Repeat("ü", wiki.MaxNote+1) }, registry.ErrInvalid},
		{"note with a line break", func(e *registry.WikiEdit) { e.Note = "one\ntwo" }, registry.ErrInvalid},
		{"note with a carriage return", func(e *registry.WikiEdit) { e.Note = "one\rtwo" }, registry.ErrInvalid},
		{"note not text", func(e *registry.WikiEdit) { e.Note = "a\xff" }, registry.ErrInvalid},
		{"negative base", func(e *registry.WikiEdit) { e.Base = -1 }, registry.ErrInvalid},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, _ := newWiki(t)
			e := edit(t, 0, "text")
			tt.edit(&e)
			if _, _, err := s.Save(t.Context(), "lime", e); !errors.Is(err, tt.want) {
				t.Errorf("Save() error = %v, want %v", err, tt.want)
			}
		})
	}

	s, _ := newWiki(t)
	if info, _, err := s.Save(t.Context(), "lime", registry.WikiEdit{Doc: doc(t, "a"), Note: "  trimmed  "}); err != nil || info.Note != "trimmed" {
		t.Errorf("Save() note = %q, %v; want it trimmed", info.Note, err)
	}
}

func TestWikiOff(t *testing.T) {
	mem := registrytest.NewMemStore()
	if err := mem.CreateNamespace(t.Context(), registry.Namespace{Name: "lime", Kind: registry.KindTeam, CreatedAt: wikiNow}); err != nil {
		t.Fatal(err)
	}
	s := registry.NewWikiService(mem, mem, func() time.Time { return wikiNow }, slog.New(slog.DiscardHandler))
	ctx := t.Context()
	for _, ns := range []string{"lime", "ghost"} {
		if _, err := s.Revision(ctx, ns, 0); !errors.Is(err, registry.ErrNotFound) {
			t.Errorf("Revision(%s) error = %v, want ErrNotFound", ns, err)
		}
		if _, err := s.Revisions(ctx, ns); !errors.Is(err, registry.ErrNotFound) {
			t.Errorf("Revisions(%s) error = %v, want ErrNotFound", ns, err)
		}
		if _, _, err := s.Save(ctx, ns, edit(t, 0, "text")); !errors.Is(err, registry.ErrNotFound) {
			t.Errorf("Save(%s) error = %v, want ErrNotFound", ns, err)
		}
	}
	if _, err := s.Revision(ctx, "lime", 0); err == nil || !strings.Contains(err.Error(), "an owner turns it on in Settings") {
		t.Errorf("Revision() of a wiki that is off error = %v, want it to say how to turn it on", err)
	}

	// Turning it off keeps the history for when it is on again.
	if err := mem.SetWiki(ctx, "lime", true); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Save(ctx, "lime", edit(t, 0, "kept")); err != nil {
		t.Fatal(err)
	}
	if err := mem.SetWiki(ctx, "lime", false); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Revision(ctx, "lime", 1); !errors.Is(err, registry.ErrNotFound) {
		t.Errorf("Revision(1) while off error = %v, want ErrNotFound", err)
	}
	if err := mem.SetWiki(ctx, "lime", true); err != nil {
		t.Fatal(err)
	}
	if rev, err := s.Revision(ctx, "lime", 0); err != nil || rev.Doc.Text() != "kept" {
		t.Errorf("Revision() on again = %q, %v; want the kept text", rev.Doc.Text(), err)
	}
}

func TestSetWikiSwitch(t *testing.T) {
	s := newService(t)
	tree(t, s)
	ns, err := s.SetWiki(t.Context(), "lime", true)
	if err != nil || !ns.Wiki || ns.Name != "lime" {
		t.Errorf("SetWiki(on) = %+v, %v", ns, err)
	}
	if acme, err := s.Namespace(t.Context(), "acme"); err != nil || acme.Wiki {
		t.Errorf("acme = %+v, %v; want its own switch off", acme, err)
	}
	if _, err := s.SetWiki(t.Context(), "ghost", true); !errors.Is(err, registry.ErrNotFound) {
		t.Errorf("SetWiki(ghost) error = %v, want ErrNotFound", err)
	}
}
