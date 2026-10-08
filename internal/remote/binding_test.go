package remote

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
)

func twoRemotes(t *testing.T) *Config {
	t.Helper()
	c := &Config{}
	for _, r := range []Remote{{Name: "a", URL: "http://127.0.0.1:1"}, {Name: "b", URL: "https://b.example.com"}} {
		if err := c.Add(r); err != nil {
			t.Fatal(err)
		}
	}
	return c
}

func TestResolveFor(t *testing.T) {
	bound := twoRemotes(t)
	if err := bound.Bind("north", "b", RoleRead); err != nil {
		t.Fatal(err)
	}
	legacy := twoRemotes(t)
	single := &Config{}
	if err := single.Add(Remote{Name: "a", URL: "http://127.0.0.1:1"}); err != nil {
		t.Fatal(err)
	}
	if err := single.Bind("lime", "a", ""); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name                          string
		cfg                           *Config
		explicit, namespace, recorded string
		want                          string
		unbound                       bool
	}{
		{name: "explicit wins over binding", cfg: bound, explicit: "a", namespace: "north", want: "a"},
		{name: "binding wins over recorded", cfg: bound, namespace: "north", recorded: "a", want: "b"},
		{name: "recorded when unbound", cfg: bound, namespace: "lime", recorded: "a", want: "a"},
		{name: "unbound with several remotes", cfg: bound, namespace: "lime", unbound: true},
		{name: "no namespace uses default", cfg: bound, want: "a"},
		{name: "legacy config uses default", cfg: legacy, namespace: "lime", want: "a"},
		{name: "single remote uses default", cfg: single, namespace: "other", want: "a"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r, err := tc.cfg.ResolveFor(tc.explicit, tc.namespace, tc.recorded)
			if tc.unbound {
				if _, ok := errors.AsType[*UnboundError](err); !ok {
					t.Fatalf("ResolveFor(%q, %q, %q) error = %v, want *UnboundError", tc.explicit, tc.namespace, tc.recorded, err)
				}
				return
			}
			if err != nil || r.Name != tc.want {
				t.Errorf("ResolveFor(%q, %q, %q) = %q, %v, want %q", tc.explicit, tc.namespace, tc.recorded, r.Name, err, tc.want)
			}
		})
	}
}

func TestResolveForDanglingBinding(t *testing.T) {
	c := twoRemotes(t)
	c.Bindings = []Binding{{Namespace: "lime", Remote: "gone"}}
	if _, err := c.ResolveFor("", "lime", ""); err == nil {
		t.Error("ResolveFor with a binding to a missing remote succeeded")
	}
}

func TestBindAncestorsAndUnbind(t *testing.T) {
	c := twoRemotes(t)
	mustBind := func(ns, r string) {
		t.Helper()
		if err := c.Bind(ns, r, RolePublish); err != nil {
			t.Fatal(err)
		}
	}
	mustAncestor := func(ns, r, via string, want bool) {
		t.Helper()
		ok, err := c.BindAncestor(ns, r, via)
		if err != nil || ok != want {
			t.Fatalf("BindAncestor(%s, %s, %s) = %v, %v, want %v", ns, r, via, ok, err, want)
		}
	}

	mustBind("lime", "a")
	mustAncestor("bro", "a", "lime", true)
	mustBind("mango", "a")
	mustAncestor("bro", "a", "mango", true)
	mustBind("north", "b")
	mustAncestor("bro", "b", "north", false) // never moved to another remote
	mustAncestor("lime", "a", "mango", true) // a direct binding stays direct

	if b, _ := c.Binding("bro"); !cmp.Equal(b.Via, []string{"lime", "mango"}) || b.Role != RoleRead {
		t.Errorf("bro binding = %+v, want read via lime and mango", b)
	}
	if b, _ := c.Binding("lime"); !b.Direct() {
		t.Errorf("lime binding = %+v, want direct", b)
	}

	removed, err := c.Unbind("lime")
	if err != nil {
		t.Fatal(err)
	}
	if len(removed) != 1 || removed[0].Namespace != "lime" {
		t.Errorf("Unbind(lime) removed %+v, want only lime (mango still needs bro)", removed)
	}
	removed, err = c.Unbind("mango")
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, b := range removed {
		names = append(names, b.Namespace)
	}
	if !cmp.Equal(names, []string{"bro", "mango"}, cmpopts.SortSlices(func(a, b string) bool { return a < b })) {
		t.Errorf("Unbind(mango) removed %v, want bro and mango", names)
	}
	if c.Bound("a") || !c.Bound("b") {
		t.Errorf("Bound(a), Bound(b) = %v, %v, want false, true", c.Bound("a"), c.Bound("b"))
	}
	if _, err := c.Unbind("mango"); err == nil {
		t.Error("Unbind of an unbound namespace succeeded")
	}
}

func TestBindValidates(t *testing.T) {
	c := twoRemotes(t)
	tests := map[string]struct {
		ns, remote string
		role       Role
	}{
		"bad namespace":  {"Lime", "a", ""},
		"missing remote": {"lime", "zz", ""},
		"bad role":       {"lime", "a", "admin"},
	}
	for name, tc := range tests {
		if err := c.Bind(tc.ns, tc.remote, tc.role); err == nil {
			t.Errorf("%s: Bind(%q, %q, %q) succeeded", name, tc.ns, tc.remote, tc.role)
		}
	}
}

func TestRemoveDropsBindings(t *testing.T) {
	c := twoRemotes(t)
	if err := c.Bind("lime", "a", ""); err != nil {
		t.Fatal(err)
	}
	if err := c.Bind("north", "b", ""); err != nil {
		t.Fatal(err)
	}
	unbound, err := c.Remove("a")
	if err != nil {
		t.Fatal(err)
	}
	if len(unbound) != 1 || unbound[0].Namespace != "lime" {
		t.Errorf("Remove(a) unbound %+v, want lime", unbound)
	}
	if _, ok := c.Binding("lime"); ok {
		t.Error("lime is still bound to a removed remote")
	}
}

func TestNameFor(t *testing.T) {
	c := &Config{}
	if err := c.Add(Remote{Name: "registry-example-com", URL: "https://registry.example.com"}); err != nil {
		t.Fatal(err)
	}
	tests := map[string]string{
		"https://registry.example.com":     "registry-example-com-2",
		"http://10.0.4.12:7420":            "10-0-4-12",
		"http://127.0.0.1:7420":            "127-0-0-1",
		"https://Reg_Istry..Example.COM/x": "reg-istry-example-com",
		"http://[::1]:7420":                "1",
		"not a url":                        "registry",
		"https://":                         "registry",
	}
	for u, want := range tests {
		got := c.NameFor(u)
		if got != want {
			t.Errorf("NameFor(%q) = %q, want %q", u, got, want)
		}
		if err := (Remote{Name: got, URL: "http://x"}).Validate(); err != nil {
			t.Errorf("NameFor(%q) = %q is not a valid name: %v", u, got, err)
		}
	}
}

func TestByURL(t *testing.T) {
	c := twoRemotes(t)
	if got := c.ByURL("https://b.example.com/"); len(got) != 1 || got[0].Name != "b" {
		t.Errorf("ByURL = %+v, want b", got)
	}
	if got := c.ByURL("https://c.example.com"); len(got) != 0 {
		t.Errorf("ByURL(unknown) = %+v, want none", got)
	}
}

func TestLegacyConfigLoadsAndBindingsPersist(t *testing.T) {
	dir := t.TempDir()
	legacy := "default: office\nremotes:\n  - name: office\n    url: http://10.0.4.12:7420\n"
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}
	s := NewStore(dir, func(string) string { return "" })
	c, err := s.Load()
	if err != nil {
		t.Fatalf("Load(legacy) error: %v", err)
	}
	if r, err := c.ResolveFor("", "lime", ""); err != nil || r.Name != "office" {
		t.Errorf("legacy ResolveFor = %v, %v, want office", r, err)
	}
	if err := c.Bind("lime", "office", RoleRead); err != nil {
		t.Fatal(err)
	}
	if err := s.Save(c); err != nil {
		t.Fatal(err)
	}
	again, err := s.Load()
	if err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff(c, again); diff != "" {
		t.Errorf("bindings did not survive a save (-want +got):\n%s", diff)
	}
}
