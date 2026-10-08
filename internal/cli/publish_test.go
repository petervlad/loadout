package cli

import (
	"slices"
	"testing"
	"time"

	"github.com/petervlad/loadout/internal/api"
)

func TestFilterReleases(t *testing.T) {
	at := func(h int) time.Time { return time.Date(2026, 9, 29, h, 0, 0, 0, time.UTC) }
	rels := []api.Release{
		{ID: "lime/zeta", Kind: "skill", PublishedAt: at(1)},
		{ID: "lime/alpha", Kind: "", PublishedAt: at(3)}, // an older registry: an agent
		{ID: "bro/docs", Kind: "kb", PublishedAt: at(2)},
		{ID: "bro/beta", Kind: "skill", PublishedAt: at(4)},
	}
	ids := func(rs []api.Release) []string {
		out := make([]string, len(rs))
		for i, r := range rs {
			out[i] = r.ID
		}
		return out
	}
	for _, tc := range []struct {
		name string
		opts listOptions
		want []string
	}{
		{"default keeps the registry order", listOptions{sort: "layer"}, []string{"lime/zeta", "lime/alpha", "bro/docs", "bro/beta"}},
		{"only skills", listOptions{kind: "skill", sort: "layer"}, []string{"lime/zeta", "bro/beta"}},
		{"an empty kind is an agent", listOptions{kind: "agent", sort: "layer"}, []string{"lime/alpha"}},
		{"only the team's layer", listOptions{layer: "lime", sort: "layer"}, []string{"lime/zeta", "lime/alpha"}},
		{"layer and kind", listOptions{layer: "bro", kind: "skill", sort: "layer"}, []string{"bro/beta"}},
		{"by name, ignoring the namespace", listOptions{sort: "name"}, []string{"lime/alpha", "bro/beta", "bro/docs", "lime/zeta"}},
		{"by kind, stable", listOptions{sort: "kind"}, []string{"lime/alpha", "lime/zeta", "bro/beta", "bro/docs"}},
		{"newest first", listOptions{sort: "updated"}, []string{"bro/beta", "lime/alpha", "bro/docs", "lime/zeta"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := ids(filterReleases(rels, tc.opts))
			if !slices.Equal(got, tc.want) {
				t.Errorf("filterReleases() = %v, want %v", got, tc.want)
			}
		})
	}
	if rels[0].ID != "lime/zeta" {
		t.Error("filterReleases changed its input")
	}
}

func TestShort(t *testing.T) {
	for _, tc := range []struct {
		in   string
		n    int
		want string
	}{
		{"fits", 10, "fits"},
		{"exactly ten", 11, "exactly ten"},
		{"cut here please", 9, "cut here…"},
		{"über lange Beschreibung", 6, "über…"},
	} {
		if got := short(tc.in, tc.n); got != tc.want {
			t.Errorf("short(%q, %d) = %q, want %q", tc.in, tc.n, got, tc.want)
		}
	}
}
