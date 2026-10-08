package wiki_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/petervlad/loadout/internal/wiki"
)

// fakeRemote is a registry with one namespace's page, applying the
// registry's rule for stale bases.
type fakeRemote struct {
	revs  []wiki.Revision // revs[i] is revision i+1
	saves int
}

func (f *fakeRemote) Page(_ context.Context, ns string, rev int) (wiki.Revision, error) {
	if rev == 0 {
		if len(f.revs) == 0 {
			return wiki.Revision{Namespace: ns}, nil
		}
		return f.revs[len(f.revs)-1], nil
	}
	if rev > len(f.revs) {
		return wiki.Revision{}, errors.New("no such revision")
	}
	return f.revs[rev-1], nil
}

func (f *fakeRemote) Save(_ context.Context, ns string, base int, doc wiki.Doc, note string) (wiki.Info, bool, error) {
	f.saves++
	newest, _ := f.Page(context.Background(), ns, 0)
	if base != newest.Seq {
		return wiki.Info{}, false, &wiki.StaleError{Base: base, Newest: newest.Info}
	}
	if doc.Equal(newest.Doc) {
		return newest.Info, false, nil
	}
	info := wiki.Info{Namespace: ns, Seq: base + 1, Format: doc.Format(), Size: doc.Size(), Author: "olga@example.com", Note: note, CreatedAt: time.Now()}
	f.revs = append(f.revs, wiki.Revision{Info: info, Doc: doc})
	return info, true, nil
}

func (f *fakeRemote) put(t *testing.T, format wiki.Format, text string) {
	t.Helper()
	doc, err := wiki.NewDoc(format, text)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := f.Save(t.Context(), "lime", len(f.revs), doc, ""); err != nil {
		t.Fatal(err)
	}
}

var ref = wiki.Ref{Remote: "https://registry.example", Namespace: "lime"}

func newSyncer(t *testing.T) (*wiki.Syncer, string) {
	t.Helper()
	return wiki.NewSyncer(wiki.NewCheckouts(t.TempDir())), t.TempDir()
}

func write(t *testing.T, path, text string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

func read(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestPullWithoutANameUsesTheFormats(t *testing.T) {
	for format, name := range map[wiki.Format]string{wiki.Markdown: "wiki.md", wiki.MDX: "wiki.mdx"} {
		s, dir := newSyncer(t)
		r := &fakeRemote{}
		r.put(t, format, "# Lime\n")
		rev, f, err := s.Pull(t.Context(), r, ref, wiki.File{Dir: dir}, 0, false)
		if err != nil {
			t.Fatal(err)
		}
		if f.Name != name || rev.Seq != 1 || read(t, filepath.Join(dir, name)) != "# Lime\n" {
			t.Errorf("%s: pulled revision %d into %q", format, rev.Seq, f.Name)
		}
	}
}

func TestPullNeverReplacesAnEdit(t *testing.T) {
	r := &fakeRemote{}
	r.put(t, wiki.Markdown, "one\n")
	r.put(t, wiki.Markdown, "two\n")
	tests := []struct {
		name    string
		prepare func(t *testing.T, s *wiki.Syncer, file string)
		force   bool
		wantErr error
	}{
		{"new file", func(*testing.T, *wiki.Syncer, string) {}, false, nil},
		{"unedited since pull", func(t *testing.T, s *wiki.Syncer, file string) {
			t.Helper()
			if _, _, err := s.Pull(t.Context(), r, ref, wiki.File{Name: file}, 1, false); err != nil {
				t.Fatal(err)
			}
		}, false, nil},
		{"edited since pull", func(t *testing.T, s *wiki.Syncer, file string) {
			t.Helper()
			if _, _, err := s.Pull(t.Context(), r, ref, wiki.File{Name: file}, 1, false); err != nil {
				t.Fatal(err)
			}
			write(t, file, "one, edited\n")
		}, false, wiki.ErrEdited},
		{"no record", func(t *testing.T, _ *wiki.Syncer, file string) { t.Helper(); write(t, file, "mine\n") }, false, wiki.ErrEdited},
		{"no record but the same text", func(t *testing.T, _ *wiki.Syncer, file string) { t.Helper(); write(t, file, "two\n") }, false, nil},
		{"edited with force", func(t *testing.T, _ *wiki.Syncer, file string) { t.Helper(); write(t, file, "mine\n") }, true, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, dir := newSyncer(t)
			file := filepath.Join(dir, "wiki.md")
			tt.prepare(t, s, file)
			before, _ := os.ReadFile(file)
			_, _, err := s.Pull(t.Context(), r, ref, wiki.File{Name: file}, 0, tt.force)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("Pull = %v, want %v", err, tt.wantErr)
			}
			if err != nil {
				if got := read(t, file); got != string(before) {
					t.Errorf("refused pull changed the file to %q", got)
				}
				if !strings.HasPrefix(err.Error(), file+" has changes") || !strings.Contains(err.Error(), "pass --force") {
					t.Errorf("error %q does not say what to do", err)
				}
				return
			}
			if got := read(t, file); got != "two\n" {
				t.Errorf("file = %q, want the newest revision", got)
			}
		})
	}
}

func TestPullAndPushRefuseASymlink(t *testing.T) {
	s, dir := newSyncer(t)
	r := &fakeRemote{}
	r.put(t, wiki.Markdown, "one\n")
	target := filepath.Join(dir, "real.md")
	write(t, target, "mine\n")
	link := filepath.Join(dir, "wiki.md")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	for _, force := range []bool{false, true} {
		if _, _, err := s.Pull(t.Context(), r, ref, wiki.File{Name: link}, 0, force); err == nil || !strings.Contains(err.Error(), "symbolic link") {
			t.Errorf("Pull(force=%v) through a symlink = %v", force, err)
		}
	}
	if _, _, err := s.Push(t.Context(), r, ref, wiki.File{Name: link}, nil, ""); err == nil || !strings.Contains(err.Error(), "symbolic link") {
		t.Errorf("Push through a symlink = %v", err)
	}
	if got := read(t, target); got != "mine\n" || r.saves != 1 {
		t.Errorf("target = %q, saves = %d; want both untouched", got, r.saves)
	}
}

func TestPushBase(t *testing.T) {
	two := 2
	tests := []struct {
		name     string
		pulled   int // revision pulled first, 0 for none
		base     *int
		wantSeq  int
		wantErr  bool
		pageRevs int
	}{
		{"recorded base", 2, nil, 3, false, 2},
		{"--base wins over the record", 1, &two, 3, false, 2},
		{"none on an empty page", 0, nil, 1, false, 0},
		{"none on a written page is stale", 0, nil, 0, true, 2},
		{"stale record", 1, nil, 0, true, 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, dir := newSyncer(t)
			r := &fakeRemote{}
			for i := range tt.pageRevs {
				r.put(t, wiki.Markdown, strings.Repeat("x", i+1)+"\n")
			}
			file := filepath.Join(dir, "wiki.md")
			if tt.pulled > 0 {
				if _, _, err := s.Pull(t.Context(), r, ref, wiki.File{Name: file}, tt.pulled, false); err != nil {
					t.Fatal(err)
				}
			}
			write(t, file, "mine\r\n")
			info, saved, err := s.Push(t.Context(), r, ref, wiki.File{Name: file}, tt.base, "note")
			if tt.wantErr {
				stale, ok := errors.AsType[*wiki.StaleError](err)
				if !ok || stale.Newest.Seq != tt.pageRevs {
					t.Fatalf("Push = %v, want a stale error naming revision %d", err, tt.pageRevs)
				}
				if got := read(t, file); got != "mine\r\n" {
					t.Errorf("file after a stale push = %q", got)
				}
				return
			}
			if err != nil || !saved || info.Seq != tt.wantSeq {
				t.Fatalf("Push = %+v, %v, %v; want revision %d saved", info, saved, err, tt.wantSeq)
			}
			if got := r.revs[info.Seq-1].Doc.Text(); got != "mine\n" {
				t.Errorf("sent %q, want LF line endings", got)
			}
			// The record follows the push, and a CRLF file does not look
			// edited, so a pull of the same revision is allowed.
			if _, _, err := s.Pull(t.Context(), r, ref, wiki.File{Name: file}, 0, false); err != nil {
				t.Errorf("pull after push = %v", err)
			}
		})
	}
}

func TestPushRecordIsPerPage(t *testing.T) {
	s, dir := newSyncer(t)
	r := &fakeRemote{}
	r.put(t, wiki.Markdown, "one\n")
	file := filepath.Join(dir, "wiki.md")
	if _, _, err := s.Pull(t.Context(), r, ref, wiki.File{Name: file}, 0, false); err != nil {
		t.Fatal(err)
	}
	// The same file pushed to another namespace starts from an empty page.
	other := wiki.Ref{Remote: ref.Remote, Namespace: "web"}
	empty := &fakeRemote{}
	if info, _, err := s.Push(t.Context(), empty, other, wiki.File{Name: file}, nil, ""); err != nil || info.Seq != 1 {
		t.Errorf("push to another page = %+v, %v; want revision 1 on base 0", info, err)
	}
}

func TestPushUnchanged(t *testing.T) {
	s, dir := newSyncer(t)
	r := &fakeRemote{}
	r.put(t, wiki.Markdown, "one\n")
	file := filepath.Join(dir, "wiki.md")
	if _, _, err := s.Pull(t.Context(), r, ref, wiki.File{Name: file}, 0, false); err != nil {
		t.Fatal(err)
	}
	info, saved, err := s.Push(t.Context(), r, ref, wiki.File{Name: file}, nil, "")
	if err != nil || saved || info.Seq != 1 || len(r.revs) != 1 {
		t.Errorf("Push of an unchanged file = %+v, %v, %v; want revision 1, nothing written", info, saved, err)
	}
}

func TestPushChecksTheFileBeforeAnyRequest(t *testing.T) {
	tests := []struct {
		name, file, text string
		want             error
	}{
		{"too large", "wiki.md", strings.Repeat("x", wiki.MaxSize+1), wiki.ErrTooLarge},
		{"far too large", "wiki.md", strings.Repeat("x", 2*wiki.MaxSize+1), wiki.ErrTooLarge},
		{"not a page", "wiki.txt", "hello", wiki.ErrFormat},
		{"not text", "wiki.md", "a\x00b", wiki.ErrNotText},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, dir := newSyncer(t)
			r := &fakeRemote{}
			file := filepath.Join(dir, tt.file)
			write(t, file, tt.text)
			if _, _, err := s.Push(t.Context(), r, ref, wiki.File{Name: file}, nil, ""); !errors.Is(err, tt.want) {
				t.Errorf("Push = %v, want %v", err, tt.want)
			}
			if r.saves != 0 {
				t.Errorf("%d requests sent", r.saves)
			}
		})
	}
}

func TestPushMissingFile(t *testing.T) {
	s, _ := newSyncer(t)
	r := &fakeRemote{}
	_, _, err := s.Push(t.Context(), r, ref, wiki.File{Dir: t.TempDir(), Name: "wiki.md"}, nil, "")
	if err == nil || !strings.HasPrefix(err.Error(), "wiki.md does not exist") || strings.Contains(err.Error(), "lstat") {
		t.Errorf("Push of a missing file = %v, want \"wiki.md does not exist\"", err)
	}
	if r.saves != 0 {
		t.Errorf("%d requests sent", r.saves)
	}
}

func TestPushMDX(t *testing.T) {
	s, dir := newSyncer(t)
	r := &fakeRemote{}
	file := filepath.Join(dir, "page.mdx")
	write(t, file, "import X from 'x'\n")
	if _, _, err := s.Push(t.Context(), r, ref, wiki.File{Name: file}, nil, ""); err != nil {
		t.Fatal(err)
	}
	if got := r.revs[0].Doc.Format(); got != wiki.MDX {
		t.Errorf("stored format = %q, want mdx", got)
	}
}

func TestFilePath(t *testing.T) {
	tests := []struct {
		f    wiki.File
		want string
	}{
		{wiki.File{Dir: "/work", Name: "wiki.md"}, "/work/wiki.md"},
		{wiki.File{Dir: "/work", Name: "docs/../wiki.md"}, "/work/wiki.md"},
		{wiki.File{Dir: "/work", Name: "/tmp/./wiki.md"}, "/tmp/wiki.md"},
	}
	for _, tt := range tests {
		if got := tt.f.Path(); got != filepath.FromSlash(tt.want) {
			t.Errorf("%+v.Path() = %q, want %q", tt.f, got, tt.want)
		}
	}
}
