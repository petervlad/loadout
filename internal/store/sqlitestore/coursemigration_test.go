package sqlitestore_test

import "testing"

// TestCourseMigration: namespaces from before courses read as having
// none of their own, so their wikis show what they showed before.
func TestCourseMigration(t *testing.T) {
	s := openAt(t, 14,
		stmt{q: `INSERT INTO namespaces (name, parent, kind, created_at, wiki) VALUES ('acme', NULL, 'project', ?, 1)`, args: []any{t0}},
		stmt{q: `INSERT INTO namespaces (name, parent, kind, created_at) VALUES ('lime', 'acme', 'team', ?)`, args: []any{t0}},
	)
	nss, err := s.Namespaces(t.Context())
	if err != nil || len(nss) != 2 {
		t.Fatalf("Namespaces() = %+v, %v", nss, err)
	}
	for _, ns := range nss {
		if !ns.Course.IsZero() {
			t.Errorf("%s has course %v after the migration, want none", ns.Name, ns.Course)
		}
	}
}

// TestCourseUnreadable: a course edited by hand into something that is no
// package id reads as none rather than breaking the namespace's pages.
func TestCourseUnreadable(t *testing.T) {
	s := openAt(t, 15,
		stmt{q: `INSERT INTO namespaces (name, parent, kind, created_at, course) VALUES ('lime', NULL, 'team', ?, 'not an id')`, args: []any{t0}},
	)
	ns, err := s.Namespace(t.Context(), "lime")
	if err != nil || !ns.Course.IsZero() {
		t.Errorf("Namespace() = %+v, %v; want lime without a course", ns, err)
	}
}
