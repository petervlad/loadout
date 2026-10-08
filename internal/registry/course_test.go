package registry_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/petervlad/loadout/internal/pack"
	"github.com/petervlad/loadout/internal/registry"
	"github.com/petervlad/loadout/internal/registry/registrytest"
)

// putKind stores a release of team/name of kind k.
func putKind(t *testing.T, store *registrytest.MemStore, team, name, version string, k pack.Kind) pack.ID {
	t.Helper()
	rel := registrytest.Release(t, team, name, version)
	rel.Kind = k
	if _, err := store.PutRelease(t.Context(), rel, []byte("b")); err != nil {
		t.Fatal(err)
	}
	return rel.ID
}

func TestSetCourse(t *testing.T) {
	store := registrytest.NewMemStore()
	s := newServiceOn(t, store)
	tree(t, s)
	own := putKind(t, store, "lime", "lime-course", "1.0.0", pack.KindKB)
	above := putKind(t, store, "acme", "onboarding", "1.0.0", pack.KindKB)
	sibling := putKind(t, store, "koala", "notes", "1.0.0", pack.KindKB)
	agent := putKind(t, store, "lime", "planner", "1.0.0", pack.KindAgent)
	skill := putKind(t, store, "lime", "style", "1.0.0", pack.KindSkill)
	putKind(t, store, "lime", "changed", "1.0.0", pack.KindKB)
	changed := putKind(t, store, "lime", "changed", "2.0.0", pack.KindSkill)
	unpublished, _ := pack.NewID("lime", "later")

	for _, id := range []pack.ID{own, above} {
		ns, err := s.SetCourse(t.Context(), "lime", id)
		if err != nil || ns.Course != id {
			t.Errorf("SetCourse(%v) = %+v, %v", id, ns, err)
		}
	}
	if ns, err := s.SetCourse(t.Context(), "lime", pack.ID{}); err != nil || !ns.Course.IsZero() {
		t.Errorf("clearing = %+v, %v", ns, err)
	}

	refused := []struct {
		ns   string
		id   pack.ID
		want string
	}{
		{"lime", sibling, "koala/notes is not in lime or a project above it, so not everyone in lime can read it"},
		{"acme", own, "lime/lime-course is not in acme"},
		{"lime", agent, "lime/planner is an agent; a course is a knowledge base (kind: kb)"},
		{"lime", skill, "lime/style is a skill; a course is a knowledge base"},
		{"lime", changed, "lime/changed is a skill"},
		{"lime", unpublished, "lime/later has no release yet; publish it first"},
	}
	for _, tt := range refused {
		_, err := s.SetCourse(t.Context(), tt.ns, tt.id)
		if !errors.Is(err, registry.ErrInvalid) || !strings.Contains(err.Error(), tt.want) {
			t.Errorf("SetCourse(%s, %v) error = %v, want ErrInvalid saying %q", tt.ns, tt.id, err, tt.want)
		}
	}
	if ns, _ := s.Namespace(t.Context(), "lime"); !ns.Course.IsZero() {
		t.Errorf("a refusal changed the course to %v", ns.Course)
	}
	for _, id := range []pack.ID{own, {}} {
		if _, err := s.SetCourse(t.Context(), "ghost", id); !errors.Is(err, registry.ErrNotFound) {
			t.Errorf("SetCourse(ghost, %v) error = %v, want ErrNotFound", id, err)
		}
	}
}

func TestCourseOf(t *testing.T) {
	limeCourse, _ := pack.NewID("lime", "lime-course")
	acmeCourse, _ := pack.NewID("acme", "onboarding")
	ml := registry.Namespace{Name: "lime-ml"}
	lime := registry.Namespace{Name: "lime"}
	acme := registry.Namespace{Name: "acme"}
	withCourse := func(ns registry.Namespace, id pack.ID) registry.Namespace { ns.Course = id; return ns }
	tests := []struct {
		name  string
		chain []registry.Namespace
		want  registry.Course
		ok    bool
	}{
		{"own wins", []registry.Namespace{withCourse(lime, limeCourse), withCourse(acme, acmeCourse)}, registry.Course{Package: limeCourse, From: "lime"}, true},
		{"the project's", []registry.Namespace{lime, withCourse(acme, acmeCourse)}, registry.Course{Package: acmeCourse, From: "acme"}, true},
		{"none", []registry.Namespace{lime, acme}, registry.Course{}, false},
		{"three layers", []registry.Namespace{ml, withCourse(lime, limeCourse), withCourse(acme, acmeCourse)}, registry.Course{Package: limeCourse, From: "lime"}, true},
		{"empty chain", nil, registry.Course{}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := registry.CourseOf(tt.chain)
			if got != tt.want || ok != tt.ok {
				t.Errorf("CourseOf() = %+v, %v; want %+v, %v", got, ok, tt.want, tt.ok)
			}
		})
	}
}
