package cli

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/petervlad/loadout/internal/api"
	"github.com/petervlad/loadout/internal/client"
	"github.com/petervlad/loadout/internal/remote"
	"github.com/petervlad/loadout/internal/wiki"
)

// TestWikiAdapterExplains: the registry's refusals become what to do next.
func TestWikiAdapterExplains(t *testing.T) {
	tests := []struct {
		name   string
		status int
		body   any // nil: a plain 404 of a registry without wiki routes
		want   []string
		stale  int
	}{
		{"old registry", http.StatusNotFound, nil, []string{"has no wiki yet; it needs a newer loadout on the registry"}, 0},
		{
			"wiki off", http.StatusNotFound,
			api.ErrorBody{Code: api.CodeNotFound, Message: "not found: lime has no wiki; an owner turns it on in Settings"},
			[]string{"lime has no wiki; an owner turns it on in Settings"},
			0,
		},
		{
			"older sign-in", http.StatusForbidden,
			api.ErrorBody{Code: api.CodeForbidden, Message: "forbidden: this token only reads lime"},
			[]string{"this token only reads lime. If you have a role in lime, sign in again with loadout login --remote office"},
			0,
		},
		{
			"stale", http.StatusConflict,
			api.ErrorBody{Code: api.CodeStale, Message: "changed", Newest: &api.WikiRevision{Revision: 14, Author: "dana@example.com"}},
			[]string{"revision 14 by dana@example.com"},
			14,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				if tt.body == nil {
					http.NotFound(w, nil)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tt.status)
				_ = json.NewEncoder(w).Encode(api.ErrorResponse{Error: tt.body.(api.ErrorBody)})
			}))
			defer srv.Close()
			c, err := client.New(srv.URL, "tok")
			if err != nil {
				t.Fatal(err)
			}
			a := wikiAdapter{c: c, r: remote.Remote{Name: "office", URL: srv.URL}}
			doc, _ := wiki.NewDoc(wiki.Markdown, "text")
			_, _, err = a.Save(t.Context(), "lime", 12, doc, "")
			for _, w := range tt.want {
				if err == nil || !strings.Contains(err.Error(), w) {
					t.Errorf("Save = %v, want it to say %q", err, w)
				}
			}
			stale, ok := errors.AsType[*wiki.StaleError](err)
			if ok != (tt.stale > 0) || (ok && (stale.Newest.Seq != tt.stale || stale.Base != 12)) {
				t.Errorf("Save = %#v, want a stale error naming %d: %v", err, tt.stale, ok)
			}
		})
	}
}

func TestStaleHint(t *testing.T) {
	e := &wiki.StaleError{Base: 12, Newest: wiki.Info{Namespace: "lime", Seq: 14, Author: "dana@example.com"}}
	got := staleHint(e, &wikiPlace{team: "lime", remote: "office"}, "notes/page.mdx").Error()
	want := "Run loadout wiki pull theirs.mdx --team lime --remote office, merge it into notes/page.mdx, then loadout wiki push notes/page.mdx --team lime --remote office --base 14. notes/page.mdx is left as it is"
	if !strings.HasSuffix(got, want) {
		t.Errorf("hint = %q\nwant suffix %q", got, want)
	}
}

// A negative --revision or --base is refused before any request, not read
// as the newest revision or an empty page.
func TestWikiNegativeRevision(t *testing.T) {
	tests := [][]string{
		{"wiki", "show", "--team", "lime", "--revision", "-1"},
		{"wiki", "pull", "--team", "lime", "--revision", "-2"},
		{"wiki", "push", "wiki.md", "--team", "lime", "--base", "-1"},
	}
	for _, args := range tests {
		t.Run(strings.Join(args[:2], " "), func(t *testing.T) {
			d, _ := testDeps(t)
			_, err := runCmd(t, d, "", args...)
			if err == nil || !strings.Contains(err.Error(), "want 0 or more") {
				t.Errorf("error = %v, want a refusal of the negative number", err)
			}
		})
	}
}
