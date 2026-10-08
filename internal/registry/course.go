package registry

import (
	"context"
	"errors"
	"fmt"

	"github.com/petervlad/loadout/internal/pack"
)

// Course is the course a namespace's wiki shows and where it is set.
type Course struct {
	Package pack.ID
	// From is the namespace whose setting it is: the page's own, or one
	// above it.
	From string
}

// CourseOf returns the course of the first namespace in chain (closest
// first) that sets one, and false when none does. A team without a
// course of its own shows its project's, as packages flow down layers.
func CourseOf(chain []Namespace) (Course, bool) {
	for _, ns := range chain {
		if !ns.Course.IsZero() {
			return Course{Package: ns.Course, From: ns.Name}, true
		}
	}
	return Course{}, false
}

// SetCourse checks and stores the course of a namespace: the zero ID
// clears it; otherwise the package must live in the namespace or a
// namespace above it, so everyone who reads the wiki reads the package,
// and its latest release must be a knowledge base. The setting names the
// package, never a version: the course follows its latest release.
// Callers check who may.
func (s *Service) SetCourse(ctx context.Context, name string, course pack.ID) (Namespace, error) {
	if !course.IsZero() {
		if err := s.checkCourse(ctx, name, course); err != nil {
			return Namespace{}, err
		}
	}
	if err := s.namespaces.SetCourse(ctx, name, course); err != nil {
		return Namespace{}, fmt.Errorf("namespace %s: course: %w", name, err)
	}
	s.log.InfoContext(ctx, "namespace course set", "namespace", name, "course", courseName(course))
	return s.Namespace(ctx, name)
}

// checkCourse refuses a course that some reader of name's wiki could not
// read, or that is not a knowledge base.
func (s *Service) checkCourse(ctx context.Context, name string, course pack.ID) error {
	chain, err := s.chain(ctx, name)
	if err != nil {
		return fmt.Errorf("namespace %s: %w", name, err)
	}
	inChain := false
	for _, l := range chain {
		inChain = inChain || l.Namespace == course.Team()
	}
	if !inChain {
		return fmt.Errorf("%w: %s is not in %s or a project above it, so not everyone in %s can read it", ErrInvalid, course, name, name)
	}
	rels, err := s.store.Releases(ctx, course)
	if errors.Is(err, ErrNotFound) || (err == nil && len(rels) == 0) {
		return fmt.Errorf("%w: %s has no release yet; publish it first", ErrInvalid, course)
	}
	if err != nil {
		return fmt.Errorf("course %s: %w", course, err)
	}
	if k := rels[len(rels)-1].Kind; k != pack.KindKB {
		return fmt.Errorf("%w: %s is %s; a course is a knowledge base (kind: kb)", ErrInvalid, course, k.WithArticle())
	}
	return nil
}

// courseName is a course for the log: "none" for the zero ID.
func courseName(id pack.ID) string {
	if id.IsZero() {
		return "none"
	}
	return id.String()
}
