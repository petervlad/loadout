package server

import (
	"context"
	"fmt"
	"net/http"
	"strconv"

	"github.com/petervlad/loadout/internal/account"
	"github.com/petervlad/loadout/internal/api"
	"github.com/petervlad/loadout/internal/registry"
	"github.com/petervlad/loadout/internal/wiki"
)

// Wiki routes, for users signed in with `loadout login`; the account
// service checks their role. A machine token acts for nobody, so it is
// refused: the history names who saved each revision.
//
//	GET /v1/namespaces/{ns}/wiki?revision=N        WikiPage (the newest without N)
//	PUT /v1/namespaces/{ns}/wiki                   WikiSave in, WikiPage out (409 stale)
//	GET /v1/namespaces/{ns}/wiki/revisions         WikiRevisions

// maxWikiBody caps a PUT body: a full page whose every byte Go's JSON
// encoder may write as a six-byte escape (<, > and &), plus the rest of
// the fields. The service checks the real size after decoding.
const maxWikiBody = 6*wiki.MaxSize + 16<<10

// Wikis is what the wiki routes need from the account service.
type Wikis interface {
	Wiki(ctx context.Context, actor account.Actor, namespace string, seq int) (wiki.Revision, error)
	WikiHistory(ctx context.Context, actor account.Actor, namespace string) ([]wiki.Info, error)
	SaveWiki(ctx context.Context, actor account.Actor, namespace string, base int, doc wiki.Doc, note string) (wiki.Info, bool, error)
}

var _ Wikis = (*account.Service)(nil)

// WithWiki serves the wiki routes from w. Without it they do not exist.
func WithWiki(w Wikis) Option { return func(s *server) { s.wikis = w } }

func (s *server) routeWiki(mux *http.ServeMux) {
	mux.Handle("GET /v1/namespaces/{ns}/wiki", s.asUser(s.wikiPage))
	mux.Handle("PUT /v1/namespaces/{ns}/wiki", s.asUser(s.saveWiki))
	mux.Handle("GET /v1/namespaces/{ns}/wiki/revisions", s.asUser(s.wikiRevisions))
}

func (s *server) wikiPage(w http.ResponseWriter, r *http.Request, a account.Actor) {
	seq := 0
	if v := r.URL.Query().Get("revision"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			s.writeError(w, r, fmt.Errorf("%w: revision %q is not a number", registry.ErrInvalid, v))
			return
		}
		seq = n
	}
	rev, err := s.wikis.Wiki(r.Context(), a, r.PathValue("ns"), seq)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	s.writeJSON(w, r, http.StatusOK, toAPIWikiPage(r.PathValue("ns"), rev))
}

func (s *server) saveWiki(w http.ResponseWriter, r *http.Request, a account.Actor) {
	var req api.WikiSave
	if err := decodeBodyLimit(w, r, &req, maxWikiBody); err != nil {
		s.writeError(w, r, err)
		return
	}
	doc, err := wiki.NewDoc(wiki.Format(req.Format), req.Text)
	if err != nil {
		s.writeError(w, r, fmt.Errorf("%w: %w", registry.ErrInvalid, err))
		return
	}
	ns := r.PathValue("ns")
	info, saved, err := s.wikis.SaveWiki(r.Context(), a, ns, req.Base, doc, req.Note)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	page := toAPIWikiPage(ns, wiki.Revision{Info: info, Doc: doc})
	page.Saved = saved
	s.writeJSON(w, r, http.StatusOK, page)
}

func (s *server) wikiRevisions(w http.ResponseWriter, r *http.Request, a account.Actor) {
	ns := r.PathValue("ns")
	infos, err := s.wikis.WikiHistory(r.Context(), a, ns)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	out := api.WikiRevisions{Namespace: ns, Revisions: make([]api.WikiRevision, 0, len(infos))}
	for _, in := range infos {
		out.Revisions = append(out.Revisions, toAPIWikiRevision(in))
	}
	s.writeJSON(w, r, http.StatusOK, out)
}

// toAPIWikiPage names ns itself, since the empty page of a namespace has
// no stored revision to carry it.
func toAPIWikiPage(ns string, rev wiki.Revision) api.WikiPage {
	return api.WikiPage{
		Namespace: ns,
		Revision:  rev.Seq,
		Format:    string(rev.Doc.Format()),
		Text:      rev.Doc.Text(),
		Author:    rev.Author,
		Note:      rev.Note,
		CreatedAt: rev.CreatedAt,
	}
}

func toAPIWikiRevision(in wiki.Info) api.WikiRevision {
	return api.WikiRevision{
		Revision:  in.Seq,
		Format:    string(in.Format),
		Size:      in.Size,
		Author:    in.Author,
		Note:      in.Note,
		CreatedAt: in.CreatedAt,
	}
}
