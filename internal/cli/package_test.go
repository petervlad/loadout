package cli

import (
	"bytes"
	"testing"

	"github.com/petervlad/loadout/internal/pack"
	"github.com/petervlad/loadout/internal/registry"
	"github.com/petervlad/loadout/internal/semver"
)

func TestWriteFootprint(t *testing.T) {
	id, err := pack.NewID("lime", "lime-app-dev")
	if err != nil {
		t.Fatal(err)
	}
	vs := func(s ...string) []semver.Version {
		var out []semver.Version
		for _, v := range s {
			p, err := semver.Parse(v)
			if err != nil {
				t.Fatal(err)
			}
			out = append(out, p)
		}
		return out
	}
	for _, tt := range []struct {
		name string
		fp   registry.PackageFootprint
		want string
	}{
		{
			"everything",
			registry.PackageFootprint{ID: id, Kind: pack.KindAgent, Releases: vs("0.1.0", "0.2.0", "1.0.0"), Proposals: 2, Open: []string{"p_7f3a"}, Reviews: 1, Prompts: 4},
			`lime/lime-app-dev (agent) would be deleted:
  releases   3: 0.1.0, 0.2.0, 1.0.0
  proposals  2, 1 open: p_7f3a
  reviews    1
  prompts    4
  course of  none
Nothing was deleted. Run it again with --yes to delete it. Back up the database first.
`,
		},
		{
			"proposals only, two courses, two open",
			registry.PackageFootprint{ID: id, Proposals: 2, Open: []string{"p_1", "p_2"}, Prompts: 4, Courses: []string{"acme", "lime"}},
			`lime/lime-app-dev would be deleted:
  releases   0
  proposals  2, 2 open: p_1, p_2
  reviews    0
  prompts    4
  course of  acme, lime
Nothing was deleted. Run it again with --yes to delete it. Back up the database first.
`,
		},
		{
			"releases only",
			registry.PackageFootprint{ID: id, Kind: pack.KindKB, Releases: vs("1.0.0")},
			`lime/lime-app-dev (kb) would be deleted:
  releases   1: 1.0.0
  proposals  0, 0 open
  reviews    0
  prompts    0
  course of  none
Nothing was deleted. Run it again with --yes to delete it. Back up the database first.
`,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var out bytes.Buffer
			writeFootprint(&out, tt.fp)
			if out.String() != tt.want {
				t.Errorf("writeFootprint() =\n%s\nwant\n%s", out.String(), tt.want)
			}
		})
	}
}

func TestWriteDeleted(t *testing.T) {
	id, err := pack.NewID("lime", "lime-app-dev")
	if err != nil {
		t.Fatal(err)
	}
	v, _ := semver.Parse("1.0.0")
	for _, tt := range []struct {
		name string
		fp   registry.PackageFootprint
		want string
	}{
		{
			"plurals",
			registry.PackageFootprint{ID: id, Releases: []semver.Version{v, v, v}, Proposals: 2, Reviews: 1, Prompts: 4},
			"deleted lime/lime-app-dev: 3 releases, 2 proposals, 1 review, 4 prompts; cleared it as the course of 0 namespaces\n",
		},
		{
			"singulars",
			registry.PackageFootprint{ID: id, Releases: []semver.Version{v}, Proposals: 1, Courses: []string{"acme"}},
			"deleted lime/lime-app-dev: 1 release, 1 proposal, 0 reviews, 0 prompts; cleared it as the course of 1 namespace\n",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var out bytes.Buffer
			writeDeleted(&out, tt.fp)
			if out.String() != tt.want {
				t.Errorf("writeDeleted() = %q, want %q", out.String(), tt.want)
			}
		})
	}
}
