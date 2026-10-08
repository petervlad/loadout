package api_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/petervlad/loadout/internal/api"
)

func TestWireNames(t *testing.T) {
	got, err := json.Marshal(api.Release{ID: "a/b", PublishedAt: time.Unix(0, 0).UTC()})
	if err != nil {
		t.Fatal(err)
	}
	want := `{"id":"a/b","version":"","digest":"","description":"","size":0,"published_at":"1970-01-01T00:00:00Z","seq":0}`
	if string(got) != want {
		t.Errorf("json = %s, want %s", got, want)
	}
}

func TestErrorWire(t *testing.T) {
	got, err := json.Marshal(api.ErrorResponse{Error: api.ErrorBody{Code: api.CodeNotFound, Message: "m"}})
	if err != nil {
		t.Fatal(err)
	}
	if want := `{"error":{"code":"not_found","message":"m"}}`; string(got) != want {
		t.Errorf("json = %s, want %s", got, want)
	}
}

func TestProposalBehind(t *testing.T) {
	tests := []struct {
		name                    string
		p                       api.Proposal
		wantBehind, wantBlocked bool
	}{
		{"older registry", api.Proposal{Status: api.StatusOpen, Version: "3.1.0", Base: ""}, false, false},
		{"up to date", api.Proposal{Status: api.StatusOpen, Version: "3.1.0", Base: "2.0.0", Latest: "2.0.0"}, false, false},
		{"behind", api.Proposal{Status: api.StatusOpen, Version: "3.1.0", Base: "2.0.0", Latest: "3.0.0"}, true, false},
		{"first release came out since", api.Proposal{Status: api.StatusOpen, Version: "0.2.0", Latest: "0.1.0"}, true, false},
		{"its version was released", api.Proposal{Status: api.StatusOpen, Version: "3.1.0", Base: "2.0.0", Latest: "3.1.0"}, true, true},
		{"a higher version was released", api.Proposal{Status: api.StatusOpen, Version: "3.1.0", Base: "2.0.0", Latest: "4.0.0"}, true, true},
		{"approved is behind its own release", api.Proposal{Status: api.StatusApproved, Version: "3.1.0", Base: "2.0.0", Latest: "3.1.0"}, false, false},
		{"withdrawn", api.Proposal{Status: api.StatusWithdrawn, Version: "3.1.0", Base: "2.0.0", Latest: "3.0.0"}, false, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.p.Behind(); got != tc.wantBehind {
				t.Errorf("Behind() = %v, want %v", got, tc.wantBehind)
			}
			if got := tc.p.Blocked(); got != tc.wantBlocked {
				t.Errorf("Blocked() = %v, want %v", got, tc.wantBlocked)
			}
		})
	}
}
