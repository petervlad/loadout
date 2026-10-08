package registry

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/petervlad/loadout/internal/wiki"
)

// ErrStale means a change was based on a version that is no longer the
// newest: someone saved in between.
var ErrStale = errors.New("changed since it was read")

// StaleError is the ErrStale a save gets from WikiService: it names the
// revision the save started from and the one that is the newest now, so
// the CLI and the web page can say who saved in between.
type StaleError struct {
	Base   int
	Newest wiki.Info
}

func (e *StaleError) Error() string {
	n := e.Newest
	return fmt.Sprintf("the wiki of %s changed since revision %d: revision %d by %s (%s) is the newest; pull it, merge your changes and save again",
		n.Namespace, e.Base, n.Seq, n.Author, n.CreatedAt.UTC().Format("2006-01-02 15:04 UTC"))
}

func (e *StaleError) Unwrap() error { return ErrStale }

// WikiStore keeps the revisions of each namespace's wiki page.
// Implementations must be safe for concurrent use.
type WikiStore interface {
	// WikiRevision returns revision seq of namespace's page, the newest for
	// seq 0. ErrNotFound when there is no such revision (none yet, pruned,
	// or a missing namespace).
	WikiRevision(ctx context.Context, namespace string, seq int) (wiki.Revision, error)
	// WikiRevisions lists the kept revisions, newest first, without text.
	// ErrNotFound for a missing namespace.
	WikiRevisions(ctx context.Context, namespace string) ([]wiki.Info, error)
	// PutWikiRevision stores rev as revision base+1 if base is the newest
	// (0: none yet), then keeps only the newest keep revisions (all for
	// keep 0), in one transaction. It takes the format and size from rev.Doc and ignores
	// rev.Seq. ErrStale when another revision came first, ErrNotFound for
	// a missing namespace.
	PutWikiRevision(ctx context.Context, rev wiki.Revision, base, keep int) (wiki.Info, error)
}

// WikiEdit is one save: the revision it started from, the new page, who
// saves and why.
type WikiEdit struct {
	Base         int
	Doc          wiki.Doc
	Note         string
	AuthorUserID string
	Author       string
}

// WikiService holds the rules of the wiki: it exists only while the
// namespace's switch is on, a page has a size limit, a save must start
// from the newest revision. Callers check who may read and edit.
type WikiService struct {
	wikis      WikiStore
	namespaces NamespaceStore
	now        func() time.Time
	log        *slog.Logger
}

// NewWikiService returns a wiki service. now is time.Now in production.
func NewWikiService(wikis WikiStore, namespaces NamespaceStore, now func() time.Time, log *slog.Logger) *WikiService {
	return &WikiService{wikis: wikis, namespaces: namespaces, now: now, log: log}
}

// Revision returns revision seq (0: newest). An empty page is a Revision
// with Seq 0 and no text. A namespace whose wiki is off is ErrNotFound.
func (s *WikiService) Revision(ctx context.Context, namespace string, seq int) (wiki.Revision, error) {
	if seq < 0 {
		return wiki.Revision{}, fmt.Errorf("%w: revision %d: revisions are numbered from 1", ErrInvalid, seq)
	}
	if err := s.checkOn(ctx, namespace); err != nil {
		return wiki.Revision{}, err
	}
	if seq == 0 {
		return s.newest(ctx, namespace)
	}
	rev, err := s.wikis.WikiRevision(ctx, namespace, seq)
	if errors.Is(err, ErrNotFound) {
		return wiki.Revision{}, fmt.Errorf("%w: the wiki of %s has no revision %d", ErrNotFound, namespace, seq)
	}
	if err != nil {
		return wiki.Revision{}, fmt.Errorf("wiki of %s: revision %d: %w", namespace, seq, err)
	}
	return rev, nil
}

// Revisions lists the kept revisions of the page, newest first, without
// their text.
func (s *WikiService) Revisions(ctx context.Context, namespace string) ([]wiki.Info, error) {
	if err := s.checkOn(ctx, namespace); err != nil {
		return nil, err
	}
	infos, err := s.wikis.WikiRevisions(ctx, namespace)
	if err != nil {
		return nil, fmt.Errorf("wiki of %s: history: %w", namespace, err)
	}
	return infos, nil
}

// Save stores e as the next revision and reports whether it wrote one: a
// save whose text and format equal the base revision writes nothing. A
// stale base is a *StaleError (ErrStale) naming the newest revision.
func (s *WikiService) Save(ctx context.Context, namespace string, e WikiEdit) (wiki.Info, bool, error) {
	note, err := checkWikiNote(e.Note)
	if err != nil {
		return wiki.Info{}, false, err
	}
	if e.Base < 0 {
		return wiki.Info{}, false, fmt.Errorf("%w: base revision %d: revisions are numbered from 1, or 0 for an empty page", ErrInvalid, e.Base)
	}
	if err := s.checkOn(ctx, namespace); err != nil {
		return wiki.Info{}, false, err
	}
	newest, err := s.newest(ctx, namespace)
	if err != nil {
		return wiki.Info{}, false, err
	}
	switch {
	case e.Base > newest.Seq:
		return wiki.Info{}, false, fmt.Errorf("%w: the wiki of %s has no revision %d; the newest is %d", ErrInvalid, namespace, e.Base, newest.Seq)
	case e.Base < newest.Seq:
		return wiki.Info{}, false, &StaleError{Base: e.Base, Newest: newest.Info}
	case e.Doc.Equal(newest.Doc):
		return newest.Info, false, nil
	}
	rev := wiki.Revision{
		Namespace:    namespace,
		AuthorUserID: e.AuthorUserID,
		Author:       e.Author,
		Note:         note,
		CreatedAt:    s.now().UTC(),
		Doc:          e.Doc,
	}
	info, err := s.wikis.PutWikiRevision(ctx, rev, e.Base, wiki.MaxRevisions)
	if errors.Is(err, ErrStale) {
		// Someone saved between the read above and the insert.
		if newest, nerr := s.newest(ctx, namespace); nerr == nil && newest.Seq > e.Base {
			return wiki.Info{}, false, &StaleError{Base: e.Base, Newest: newest.Info}
		}
	}
	if err != nil {
		return wiki.Info{}, false, fmt.Errorf("wiki of %s: save: %w", namespace, err)
	}
	s.log.InfoContext(ctx, "wiki saved", "namespace", namespace, "revision", info.Seq, "user_id", e.AuthorUserID, "size", info.Size)
	return info, true, nil
}

// checkOn returns ErrNotFound for a missing namespace and for one whose
// wiki is off.
func (s *WikiService) checkOn(ctx context.Context, namespace string) error {
	ns, err := s.namespaces.Namespace(ctx, namespace)
	if err != nil {
		return fmt.Errorf("namespace %s: %w", namespace, err)
	}
	if !ns.Wiki {
		return fmt.Errorf("%w: %s has no wiki; an owner turns it on in Settings", ErrNotFound, namespace)
	}
	return nil
}

// newest is the newest revision, or the empty page with Seq 0.
func (s *WikiService) newest(ctx context.Context, namespace string) (wiki.Revision, error) {
	rev, err := s.wikis.WikiRevision(ctx, namespace, 0)
	if errors.Is(err, ErrNotFound) {
		return wiki.Revision{Namespace: namespace, Format: wiki.Markdown}, nil
	}
	if err != nil {
		return wiki.Revision{}, fmt.Errorf("wiki of %s: %w", namespace, err)
	}
	return rev, nil
}

// checkWikiNote trims a save's note and checks it is one line of at most
// wiki.MaxNote characters.
func checkWikiNote(note string) (string, error) {
	note = strings.TrimSpace(note)
	switch {
	case !utf8.ValidString(note):
		return "", fmt.Errorf("%w: the note is not text", ErrInvalid)
	case strings.ContainsAny(note, "\r\n"):
		return "", fmt.Errorf("%w: the note is one line; put the details in the page", ErrInvalid)
	case utf8.RuneCountInString(note) > wiki.MaxNote:
		return "", fmt.Errorf("%w: the note has %d characters and the limit is %d", ErrInvalid, utf8.RuneCountInString(note), wiki.MaxNote)
	}
	return note, nil
}
