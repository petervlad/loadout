package account

import (
	"context"
	"fmt"

	"github.com/petervlad/loadout/internal/pack"
	"github.com/petervlad/loadout/internal/registry"
	"github.com/petervlad/loadout/internal/wiki"
)

// Wikis is what the account service needs from the wiki service.
type Wikis interface {
	Revision(ctx context.Context, namespace string, seq int) (wiki.Revision, error)
	Revisions(ctx context.Context, namespace string) ([]wiki.Info, error)
	Save(ctx context.Context, namespace string, e registry.WikiEdit) (wiki.Info, bool, error)
}

var _ Wikis = (*registry.WikiService)(nil)

// operatorAuthor names the registry host in a wiki's history.
const operatorAuthor = "the registry host"

// CanEditWiki reports whether the user may save the namespace's wiki: a
// role of their own there, ownership above, or admin. A member of a team
// below only reads it.
func (a Access) CanEditWiki() bool { return a.Role.AtLeast(RoleMember) && !a.Below }

// CanSetWiki reports whether the user may turn the wiki on or off: those
// who govern the namespace.
func (a Access) CanSetWiki() bool { return a.Governs }

// Wiki returns revision seq of the namespace's wiki page (0: the newest)
// for anyone who reads the namespace.
func (s *Service) Wiki(ctx context.Context, actor Actor, namespace string, seq int) (wiki.Revision, error) {
	if err := s.canReadWiki(ctx, actor, namespace); err != nil {
		return wiki.Revision{}, err
	}
	return s.wiki.Revision(ctx, namespace, seq)
}

// WikiHistory lists the kept revisions of the namespace's wiki page,
// newest first, for anyone who reads the namespace.
func (s *Service) WikiHistory(ctx context.Context, actor Actor, namespace string) ([]wiki.Info, error) {
	if err := s.canReadWiki(ctx, actor, namespace); err != nil {
		return nil, err
	}
	return s.wiki.Revisions(ctx, namespace)
}

// SaveWiki saves doc as the revision after base and reports whether it
// wrote one. Members, maintainers and owners of the namespace, owners of a
// project above and admins may. The history keeps the author's email.
func (s *Service) SaveWiki(ctx context.Context, actor Actor, namespace string, base int, doc wiki.Doc, note string) (wiki.Info, bool, error) {
	a, err := s.wikiAccess(ctx, actor, namespace)
	if err != nil {
		return wiki.Info{}, false, err
	}
	if !a.CanEditWiki() {
		return wiki.Info{}, false, forbidden("editing the wiki of " + namespace + " needs a role in " + namespace + "; members of a team below only read it")
	}
	author := operatorAuthor
	if !actor.Operator {
		u, err := s.store.UserByID(ctx, actor.UserID)
		if err != nil {
			return wiki.Info{}, false, fmt.Errorf("save the wiki of %s: %w", namespace, err)
		}
		author = u.Email
	}
	return s.wiki.Save(ctx, namespace, registry.WikiEdit{Base: base, Doc: doc, Note: note, AuthorUserID: actor.UserID, Author: author})
}

// SetWiki turns the namespace's wiki on or off; owners (of it or a
// project above it) and admins may. Turning it off keeps the history.
func (s *Service) SetWiki(ctx context.Context, actor Actor, namespace string, on bool) (registry.Namespace, error) {
	a, err := s.wikiAccess(ctx, actor, namespace)
	if err != nil {
		return registry.Namespace{}, err
	}
	if !a.CanSetWiki() {
		return registry.Namespace{}, forbidden("turning the wiki of " + namespace + " on or off needs an owner (or an admin)")
	}
	ns, err := s.reg.SetWiki(ctx, namespace, on)
	if err != nil {
		return registry.Namespace{}, err
	}
	s.log.InfoContext(ctx, "wiki switched on request", "namespace", namespace, "wiki", on, "by", actorLog(actor))
	return ns, nil
}

// SetCourse sets the knowledge base the namespace's wiki shows as its
// course (the zero ID: none of its own); the same people who turn the
// wiki on and off may. The registry checks the package.
func (s *Service) SetCourse(ctx context.Context, actor Actor, namespace string, course pack.ID) (registry.Namespace, error) {
	a, err := s.wikiAccess(ctx, actor, namespace)
	if err != nil {
		return registry.Namespace{}, err
	}
	if !a.CanSetWiki() {
		return registry.Namespace{}, forbidden("choosing the course of " + namespace + " needs an owner (or an admin)")
	}
	ns, err := s.reg.SetCourse(ctx, namespace, course)
	if err != nil {
		return registry.Namespace{}, err
	}
	set := "none"
	if !ns.Course.IsZero() {
		set = ns.Course.String()
	}
	s.log.InfoContext(ctx, "course set", "namespace", namespace, "course", set, "by", actorLog(actor))
	return ns, nil
}

// CourseAbove is the course the namespace's wiki shows when it sets none
// of its own: that of the closest namespace above it that sets one, for
// anyone who reads the namespace. false when none above does.
func (s *Service) CourseAbove(ctx context.Context, actor Actor, namespace string) (registry.Course, bool, error) {
	if err := s.canReadWiki(ctx, actor, namespace); err != nil {
		return registry.Course{}, false, err
	}
	ns, err := s.namespace(ctx, namespace)
	if err != nil {
		return registry.Course{}, false, err
	}
	var above []registry.Namespace
	for cur := ns.Parent; cur != "" && len(above) < registry.MaxLayers; {
		p, err := s.reg.Namespace(ctx, cur)
		if err != nil {
			return registry.Course{}, false, fmt.Errorf("course above %s: %w", namespace, err)
		}
		above = append(above, p)
		cur = p.Parent
	}
	c, ok := registry.CourseOf(above)
	return c, ok, nil
}

func (s *Service) canReadWiki(ctx context.Context, actor Actor, namespace string) error {
	a, err := s.wikiAccess(ctx, actor, namespace)
	if err != nil {
		return err
	}
	if !a.CanRead() {
		return forbidden("the wiki of " + namespace + " is for its members")
	}
	return nil
}

// wikiAccess is what actor may do in namespace, once the registry has a
// wiki and the namespace exists.
func (s *Service) wikiAccess(ctx context.Context, actor Actor, namespace string) (Access, error) {
	if s.wiki == nil {
		return Access{}, fmt.Errorf("%w: this registry has no wiki", registry.ErrNotFound)
	}
	if _, err := s.namespace(ctx, namespace); err != nil {
		return Access{}, err
	}
	return s.access(ctx, actor, namespace)
}
