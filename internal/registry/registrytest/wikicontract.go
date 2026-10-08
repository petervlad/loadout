package registrytest

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/petervlad/loadout/internal/registry"
	"github.com/petervlad/loadout/internal/wiki"
)

// WikiBackend is a store that keeps both namespaces and their wiki pages:
// a page belongs to a namespace and goes when it is deleted.
type WikiBackend interface {
	registry.NamespaceStore
	registry.WikiStore
}

var wikiAt = time.Date(2026, 10, 3, 10, 14, 0, 123456789, time.UTC)

// WikiRev returns a revision of namespace's page for tests.
func WikiRev(t *testing.T, namespace string, f wiki.Format, text string) wiki.Revision {
	t.Helper()
	doc, err := wiki.NewDoc(f, text)
	if err != nil {
		t.Fatal(err)
	}
	return wiki.Revision{
		Namespace:    namespace,
		AuthorUserID: "u-dana",
		Author:       "dana@acme.example",
		Note:         "why " + namespace,
		CreatedAt:    wikiAt,
		Doc:          doc,
	}
}

// RunWikiStoreContract runs the behaviour every registry.WikiStore must
// have. newStore must return a fresh, empty store for each call.
func RunWikiStoreContract(t *testing.T, newStore func(t *testing.T) WikiBackend) {
	t.Helper()
	fresh := func(t *testing.T) WikiBackend {
		t.Helper()
		s := newStore(t)
		if err := s.CreateNamespace(t.Context(), registry.Namespace{Name: "lime", Kind: registry.KindTeam, CreatedAt: wikiAt}); err != nil {
			t.Fatal(err)
		}
		return s
	}
	put := func(t *testing.T, s WikiBackend, rev wiki.Revision, base int) wiki.Info {
		t.Helper()
		info, err := s.PutWikiRevision(t.Context(), rev, base, 0)
		if err != nil {
			t.Fatalf("PutWikiRevision(base %d) error: %v", base, err)
		}
		return info
	}

	t.Run("empty page", func(t *testing.T) {
		s := fresh(t)
		if _, err := s.WikiRevision(t.Context(), "lime", 0); !errors.Is(err, registry.ErrNotFound) {
			t.Errorf("WikiRevision(newest) of an empty page error = %v, want ErrNotFound", err)
		}
		if got, err := s.WikiRevisions(t.Context(), "lime"); err != nil || len(got) != 0 {
			t.Errorf("WikiRevisions() of an empty page = %+v, %v; want none", got, err)
		}
	})

	t.Run("revisions follow their base", func(t *testing.T) {
		s := fresh(t)
		if info := put(t, s, WikiRev(t, "lime", wiki.Markdown, "one"), 0); info.Seq != 1 {
			t.Errorf("first put Seq = %d, want 1", info.Seq)
		}
		if _, err := s.PutWikiRevision(t.Context(), WikiRev(t, "lime", wiki.Markdown, "also one"), 0, 0); !errors.Is(err, registry.ErrStale) {
			t.Errorf("put on base 0 again error = %v, want ErrStale", err)
		}
		if _, err := s.PutWikiRevision(t.Context(), WikiRev(t, "lime", wiki.Markdown, "three"), 2, 0); !errors.Is(err, registry.ErrStale) {
			t.Errorf("put on a base that does not exist error = %v, want ErrStale", err)
		}
		if info := put(t, s, WikiRev(t, "lime", wiki.Markdown, "two"), 1); info.Seq != 2 {
			t.Errorf("put on base 1 Seq = %d, want 2", info.Seq)
		}
		got, err := s.WikiRevision(t.Context(), "lime", 1)
		if err != nil || got.Doc.Text() != "one" {
			t.Errorf("WikiRevision(1) = %q, %v; want the first text", got.Doc.Text(), err)
		}
	})

	t.Run("round trip", func(t *testing.T) {
		s := fresh(t)
		texts := []struct {
			f    wiki.Format
			text string
		}{
			{wiki.Markdown, strings.Repeat("x", wiki.MaxSize)},
			{wiki.MDX, "# Grüße\n\n日本語, 🥝🏳️‍🌈, \u202e, \ufeff, tab\tend, ' \" ` \\ %_\n"},
		}
		for i, tt := range texts {
			in := WikiRev(t, "lime", tt.f, tt.text)
			info := put(t, s, in, i)
			for _, seq := range []int{info.Seq, 0} {
				got, err := s.WikiRevision(t.Context(), "lime", seq)
				if err != nil {
					t.Fatalf("WikiRevision(%d) error: %v", seq, err)
				}
				want := in.Info
				want.Seq, want.Format, want.Size = i+1, tt.f, len(tt.text)
				if got.Info != want || !got.CreatedAt.Equal(wikiAt) {
					t.Errorf("WikiRevision(%d).Info = %+v, want %+v", seq, got.Info, want)
				}
				if !got.Doc.Equal(in.Doc) {
					t.Errorf("WikiRevision(%d) text of %d bytes (%s) is not what was put", seq, got.Doc.Size(), got.Doc.Format())
				}
			}
			if info != (wiki.Info{Namespace: "lime", Seq: i + 1, Format: tt.f, Size: len(tt.text), AuthorUserID: "u-dana", Author: "dana@acme.example", Note: "why lime", CreatedAt: wikiAt}) {
				t.Errorf("PutWikiRevision() = %+v", info)
			}
		}
	})

	t.Run("history newest first", func(t *testing.T) {
		s := fresh(t)
		for i, text := range []string{"a", "bb", "ccc"} {
			put(t, s, WikiRev(t, "lime", wiki.Markdown, text), i)
		}
		got, err := s.WikiRevisions(t.Context(), "lime")
		if err != nil {
			t.Fatal(err)
		}
		var seqs, sizes []int
		for _, info := range got {
			seqs, sizes = append(seqs, info.Seq), append(sizes, info.Size)
			if info.Author != "dana@acme.example" || info.Format != wiki.Markdown || !info.CreatedAt.Equal(wikiAt) {
				t.Errorf("WikiRevisions() entry = %+v", info)
			}
		}
		if fmt.Sprint(seqs, sizes) != "[3 2 1] [3 2 1]" {
			t.Errorf("WikiRevisions() seqs %v sizes %v, want [3 2 1] [3 2 1]", seqs, sizes)
		}
	})

	t.Run("keeps the newest", func(t *testing.T) {
		s := fresh(t)
		for base := range 5 {
			if _, err := s.PutWikiRevision(t.Context(), WikiRev(t, "lime", wiki.Markdown, fmt.Sprint("text ", base+1)), base, 3); err != nil {
				t.Fatal(err)
			}
		}
		got, err := s.WikiRevisions(t.Context(), "lime")
		if err != nil {
			t.Fatal(err)
		}
		var seqs []int
		for _, info := range got {
			seqs = append(seqs, info.Seq)
		}
		if fmt.Sprint(seqs) != "[5 4 3]" {
			t.Errorf("WikiRevisions() after five saves keeping 3 = %v, want [5 4 3]", seqs)
		}
		if _, err := s.WikiRevision(t.Context(), "lime", 1); !errors.Is(err, registry.ErrNotFound) {
			t.Errorf("WikiRevision(pruned 1) error = %v, want ErrNotFound", err)
		}
		if rev, err := s.WikiRevision(t.Context(), "lime", 0); err != nil || rev.Seq != 5 || rev.Doc.Text() != "text 5" {
			t.Errorf("WikiRevision(newest) = %d %q, %v; want 5", rev.Seq, rev.Doc.Text(), err)
		}
		// The next save goes on after the pruned ones.
		if info, err := s.PutWikiRevision(t.Context(), WikiRev(t, "lime", wiki.Markdown, "six"), 5, 3); err != nil || info.Seq != 6 {
			t.Errorf("put after pruning = %+v, %v; want Seq 6", info, err)
		}
	})

	t.Run("concurrent saves on one base", func(t *testing.T) {
		s := fresh(t)
		put(t, s, WikiRev(t, "lime", wiki.Markdown, "base"), 0)
		const n = 20
		errs := make([]error, n)
		var wg sync.WaitGroup
		for i := range n {
			rev := WikiRev(t, "lime", wiki.Markdown, fmt.Sprint("edit ", i))
			wg.Go(func() {
				_, errs[i] = s.PutWikiRevision(t.Context(), rev, 1, 0)
			})
		}
		wg.Wait()
		won := 0
		for _, err := range errs {
			switch {
			case err == nil:
				won++
			case !errors.Is(err, registry.ErrStale):
				t.Errorf("concurrent put error = %v, want nil or ErrStale", err)
			}
		}
		if won != 1 {
			t.Errorf("%d concurrent saves on one base won, want exactly 1", won)
		}
		if got, err := s.WikiRevisions(t.Context(), "lime"); err != nil || len(got) != 2 {
			t.Errorf("WikiRevisions() after the race = %d, %v; want 2", len(got), err)
		}
	})

	t.Run("missing namespace", func(t *testing.T) {
		s := newStore(t)
		if _, err := s.WikiRevision(t.Context(), "ghost", 0); !errors.Is(err, registry.ErrNotFound) {
			t.Errorf("WikiRevision(ghost) error = %v, want ErrNotFound", err)
		}
		if _, err := s.WikiRevisions(t.Context(), "ghost"); !errors.Is(err, registry.ErrNotFound) {
			t.Errorf("WikiRevisions(ghost) error = %v, want ErrNotFound", err)
		}
		if _, err := s.PutWikiRevision(t.Context(), WikiRev(t, "ghost", wiki.Markdown, "x"), 0, 0); !errors.Is(err, registry.ErrNotFound) {
			t.Errorf("PutWikiRevision(ghost) error = %v, want ErrNotFound", err)
		}
	})

	t.Run("delete namespace takes the wiki", func(t *testing.T) {
		s := fresh(t)
		put(t, s, WikiRev(t, "lime", wiki.Markdown, "one"), 0)
		if err := s.DeleteNamespace(t.Context(), "lime"); err != nil {
			t.Fatalf("DeleteNamespace() with a wiki error: %v; a wiki does not keep a namespace", err)
		}
		if err := s.CreateNamespace(t.Context(), registry.Namespace{Name: "lime", Kind: registry.KindTeam, CreatedAt: wikiAt}); err != nil {
			t.Fatal(err)
		}
		if got, err := s.WikiRevisions(t.Context(), "lime"); err != nil || len(got) != 0 {
			t.Errorf("WikiRevisions() after delete and create = %+v, %v; want none", got, err)
		}
		if _, err := s.WikiRevision(t.Context(), "lime", 1); !errors.Is(err, registry.ErrNotFound) {
			t.Errorf("WikiRevision(1) after delete and create error = %v, want ErrNotFound", err)
		}
	})
}
