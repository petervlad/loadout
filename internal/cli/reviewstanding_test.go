package cli

import (
	"strings"
	"testing"

	"github.com/petervlad/loadout/internal/api"
)

func TestStandingWrite(t *testing.T) {
	dana := &api.Actor{TokenID: "t1", Label: "dana@example.com via loadout login"}
	rels := func(versions ...string) []api.Release {
		out := make([]api.Release, len(versions))
		for i, v := range versions {
			out[i] = api.Release{Version: v}
		}
		out[len(out)-1].PublishedBy = dana
		return out
	}
	open := func(base, latest string) api.Proposal {
		return api.Proposal{ID: "abc", Package: "lime/planner", Version: "3.1.0", Status: api.StatusOpen, Base: base, Latest: latest}
	}
	tests := []struct {
		name string
		p    api.Proposal
		rels []api.Release
		want string
	}{
		{"older registry says nothing", open("", ""), rels("2.0.0"), ""},
		{"up to date", open("2.0.0", "2.0.0"), rels("2.0.0"), "\nMade from 2.0.0, proposes 3.1.0.\n"},
		{
			"one release since, its publisher named", open("2.0.0", "3.0.0"), rels("2.0.0", "3.0.0"),
			"The latest release is 3.0.0 by dana@example.com, released after it: approving releases 3.1.0 without 3.0.0's changes.",
		},
		{
			"nobody known: the version alone", open("2.0.0", "3.0.0"),
			[]api.Release{{Version: "2.0.0"}, {Version: "3.0.0"}},
			"The latest release is 3.0.0, released after it",
		},
		{
			"several since, capped", open("1.0.0", "1.6.0"), rels("1.0.0", "1.1.0", "1.2.0", "1.3.0", "1.4.0", "1.5.0", "1.6.0"),
			"Released after it: 1.1.0, 1.2.0, 1.3.0, 1.4.0, 1.5.0 and 1 more. Approving releases 3.1.0 without their changes.",
		},
		{
			"made before the first release", open("", "0.1.0"), rels("0.1.0"),
			"Made before the first release, proposes 3.1.0. The latest release is 0.1.0 by dana@example.com",
		},
		{
			"blocked", open("2.0.0", "3.1.0"), rels("2.0.0", "3.1.0"),
			"3.1.0 by dana@example.com was released after it, so it cannot be approved as 3.1.0",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var b strings.Builder
			newStanding(tc.p, tc.rels).write(&b)
			if got := b.String(); (tc.want == "" && got != "") || !strings.Contains(got, tc.want) {
				t.Errorf("write() = %q, want it to contain %q", got, tc.want)
			}
		})
	}
}

func TestStandingDiffBase(t *testing.T) {
	rels := []api.Release{{Version: "1.0.0"}, {Version: "2.0.0"}}
	tests := []struct {
		name, base, latest, want, title string
	}{
		{"the recorded base", "1.0.0", "2.0.0", "1.0.0", "What the author changed since 1.0.0"},
		{"before the first release", "", "1.0.0", "(none)", "Files of the proposal (there was no release when it was made)"},
		{"older registry: the latest release", "", "", "2.0.0", "Changes from the latest release 2.0.0"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := newStanding(api.Proposal{Status: api.StatusOpen, Version: "3.0.0", Base: tc.base, Latest: tc.latest}, rels)
			if got := s.diffLabel(); got != tc.want {
				t.Errorf("diffLabel() = %q, want %q", got, tc.want)
			}
			if got := s.diffTitle(); got != tc.title {
				t.Errorf("diffTitle() = %q, want %q", got, tc.title)
			}
		})
	}
}
