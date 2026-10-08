package registry_test

import (
	"testing"

	"github.com/petervlad/loadout/internal/registry"
	"github.com/petervlad/loadout/internal/registry/registrytest"
	"github.com/petervlad/loadout/internal/semver"
)

func TestParseBase(t *testing.T) {
	tests := []struct {
		in      string
		want    string
		wantSet bool
		wantErr bool
	}{
		{"", "", false, false},
		{"2.0.0", "2.0.0", true, false},
		{"v2.0.0", "2.0.0", true, false},
		{"latest", "", false, true},
		{"2.0", "", false, true},
	}
	for _, tc := range tests {
		t.Run(tc.in, func(t *testing.T) {
			b, err := registry.ParseBase(tc.in)
			if (err != nil) != tc.wantErr {
				t.Fatalf("ParseBase(%q) error = %v, want error %v", tc.in, err, tc.wantErr)
			}
			if _, set := b.Version(); b.String() != tc.want || set != tc.wantSet || b.IsZero() == tc.wantSet {
				t.Errorf("ParseBase(%q) = %q (set %v), want %q (set %v)", tc.in, b, set, tc.want, tc.wantSet)
			}
		})
	}
}

func TestStanding(t *testing.T) {
	none := registry.Base{}
	at := func(v string) registry.Base { return registrytest.BaseAt(t, v) }
	tests := []struct {
		name       string
		s          registry.Standing
		version    string
		wantBehind bool
		wantBlocks bool
	}{
		{"no release before or since", registry.Standing{Base: none, Latest: none}, "0.1.0", false, false},
		{"first release came out since", registry.Standing{Base: none, Latest: at("0.1.0")}, "0.2.0", true, false},
		{"up to date", registry.Standing{Base: at("2.0.0"), Latest: at("2.0.0")}, "3.1.0", false, false},
		{"behind", registry.Standing{Base: at("2.0.0"), Latest: at("3.0.0")}, "3.1.0", true, false},
		{"released at the proposed version", registry.Standing{Base: at("2.0.0"), Latest: at("3.1.0")}, "3.1.0", true, true},
		{"released above the proposed version", registry.Standing{Base: at("2.0.0"), Latest: at("4.0.0")}, "3.1.0", true, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			v, err := semver.Parse(tc.version)
			if err != nil {
				t.Fatal(err)
			}
			if got := tc.s.Behind(); got != tc.wantBehind {
				t.Errorf("%+v.Behind() = %v, want %v", tc.s, got, tc.wantBehind)
			}
			if got := tc.s.Blocks(v); got != tc.wantBlocks {
				t.Errorf("%+v.Blocks(%s) = %v, want %v", tc.s, v, got, tc.wantBlocks)
			}
		})
	}
}
