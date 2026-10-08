package wiki

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/petervlad/loadout/internal/atomicfile"
)

// Remote is what the Syncer needs from a registry.
type Remote interface {
	// Page returns revision rev of namespace's page, the newest for rev 0.
	Page(ctx context.Context, namespace string, rev int) (Revision, error)
	// Save stores doc as the revision after base and reports whether it
	// wrote one. A stale base is a *StaleError.
	Save(ctx context.Context, namespace string, base int, doc Doc, note string) (Info, bool, error)
}

// StaleError is a save refused because someone saved after its base.
type StaleError struct {
	Base   int
	Newest Info
}

func (e *StaleError) Error() string {
	n := e.Newest
	return fmt.Sprintf("the wiki of %s changed since revision %d: revision %d by %s (%s) is the newest",
		n.Namespace, e.Base, n.Seq, n.Author, n.CreatedAt.UTC().Format("2006-01-02 15:04 UTC"))
}

// ErrEdited means pull would replace a file with changes the registry
// does not have.
var ErrEdited = errors.New("has changes that are not on the registry")

// File is a local page file: Name as the person gave it, in the folder
// Dir unless Name is absolute. Messages say Name, so they read as typed.
type File struct {
	Dir, Name string
}

// Path is the file's absolute path, the key of its checkout record.
func (f File) Path() string {
	if filepath.IsAbs(f.Name) {
		return filepath.Clean(f.Name)
	}
	return filepath.Join(f.Dir, f.Name)
}

// Syncer pulls a page into a file and pushes a file as a new revision,
// remembering in its Checkouts what each file was based on.
type Syncer struct{ checkouts *Checkouts }

// NewSyncer returns a Syncer that records checkouts in c.
func NewSyncer(c *Checkouts) *Syncer { return &Syncer{checkouts: c} }

// Pull writes revision rev of ref's page (0: the newest) to f and records
// it; an empty f.Name is the default name of the page's format (wiki.md,
// wiki.mdx). An existing file is replaced only if it is what loadout last
// wrote or pushed, or holds the page's text already, unless force. It
// returns the revision and the file written.
func (s *Syncer) Pull(ctx context.Context, r Remote, ref Ref, f File, rev int, force bool) (Revision, File, error) {
	page, err := r.Page(ctx, ref.Namespace, rev)
	if err != nil {
		return Revision{}, f, err
	}
	if f.Name == "" {
		f.Name = page.Doc.Format().File()
	}
	text := []byte(page.Doc.Text())
	if !force {
		if err := s.checkReplace(f, text); err != nil {
			return Revision{}, f, err
		}
	} else if fi, err := os.Lstat(f.Path()); err == nil && fi.Mode()&fs.ModeSymlink != 0 {
		return Revision{}, f, symlinkError(f)
	}
	if err := atomicfile.Write(f.Path(), text, 0o644); err != nil {
		return Revision{}, f, fmt.Errorf("write %s: %w", f.Name, err)
	}
	if err := s.checkouts.Put(f.Path(), Checkout{Ref: ref, Revision: page.Seq, Digest: Digest(text)}); err != nil {
		return Revision{}, f, err
	}
	return page, f, nil
}

// checkReplace refuses to replace f if it holds anything the registry
// might not have.
func (s *Syncer) checkReplace(f File, text []byte) error {
	data, err := read(f, -1)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if string(data) == string(text) {
		return nil
	}
	co, ok, err := s.checkouts.Get(f.Path())
	if err != nil {
		return err
	}
	if !ok || co.Edited(data) {
		return fmt.Errorf("%s %w. Push them first, pull into another file, or pass --force to replace them", f.Name, ErrEdited)
	}
	return nil
}

// Push saves f as the revision after its base: base when given, else the
// revision f was pulled or pushed at for this page, else 0 (accepted only
// while the page is empty). The format comes from the file's name. The
// file is checked before any request, and left as it is when the save
// fails. It reports whether a revision was written.
func (s *Syncer) Push(ctx context.Context, r Remote, ref Ref, f File, base *int, note string) (Info, bool, error) {
	format, err := FormatOf(f.Name)
	if err != nil {
		return Info{}, false, err
	}
	// CRLF line endings at most double a page.
	data, err := read(f, 2*MaxSize)
	if err != nil {
		return Info{}, false, err
	}
	doc, err := NewDoc(format, string(data))
	if err != nil {
		return Info{}, false, fmt.Errorf("%s: %w", f.Name, err)
	}
	from := 0
	if base != nil {
		from = *base
	} else if co, ok, err := s.checkouts.Get(f.Path()); err != nil {
		return Info{}, false, err
	} else if ok && co.Ref == ref {
		from = co.Revision
	}
	info, saved, err := r.Save(ctx, ref.Namespace, from, doc, note)
	if err != nil {
		return Info{}, false, err
	}
	// The digest is of the file as it is on disk, so a file with CRLF
	// line endings does not look edited after its push.
	if err := s.checkouts.Put(f.Path(), Checkout{Ref: ref, Revision: info.Seq, Digest: Digest(data)}); err != nil {
		return Info{}, false, err
	}
	return info, saved, nil
}

// read reads a page file, refusing a symlink, and a file over limit bytes
// (unless limit is negative) before reading it.
func read(f File, limit int64) ([]byte, error) {
	fi, err := os.Lstat(f.Path())
	if errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("%s does not exist: %w", f.Name, fs.ErrNotExist)
	}
	if err != nil {
		return nil, err
	}
	switch {
	case fi.Mode()&fs.ModeSymlink != 0:
		return nil, symlinkError(f)
	case !fi.Mode().IsRegular():
		return nil, fmt.Errorf("%s is not a file", f.Name)
	case limit >= 0 && fi.Size() > limit:
		return nil, fmt.Errorf("%s: %w: it is %d bytes and the limit is %d (256 KB); split it or shorten it", f.Name, ErrTooLarge, fi.Size(), MaxSize)
	}
	return os.ReadFile(f.Path())
}

func symlinkError(f File) error {
	return fmt.Errorf("%s is a symbolic link; loadout reads and writes wiki pages only as plain files, so name the file it points to", f.Name)
}
