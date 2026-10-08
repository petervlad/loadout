package cli

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/petervlad/loadout/internal/api"
	"github.com/petervlad/loadout/internal/remote"
)

// settingsRegistry answers PATCH /v1/namespaces/{ns} with status, applying
// the settings to its one namespace, and records each body it was sent.
func settingsRegistry(t *testing.T, status int) (string, *[]string) {
	t.Helper()
	var bodies []string
	ns := api.Namespace{Name: "lime", Parent: "acme", Kind: "team"}
	mux := http.NewServeMux()
	mux.HandleFunc("PATCH /v1/namespaces/lime", func(w http.ResponseWriter, r *http.Request) {
		var req api.NamespaceSettings
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Error(err)
		}
		b, _ := json.Marshal(req)
		bodies = append(bodies, string(b))
		w.Header().Set("Content-Type", "application/json")
		if status != http.StatusOK {
			w.WriteHeader(status)
			_, _ = w.Write([]byte(`{"error":{"code":"forbidden","message":"turning the wiki of lime on or off needs an owner (or an admin)"}}`))
			return
		}
		if req.RequireReview != nil {
			ns.RequireReview = *req.RequireReview
		}
		if req.Wiki != nil {
			ns.Wiki = *req.Wiki
		}
		_ = json.NewEncoder(w).Encode(ns)
	})
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return ts.URL, &bodies
}

func TestNamespaceSetWiki(t *testing.T) {
	tests := []struct {
		name       string
		status     int
		args       []string
		wantBodies []string
		wantOut    string
		wantErr    string
	}{
		{
			name: "on", status: http.StatusOK, args: []string{"--wiki", "on"},
			wantBodies: []string{`{"wiki":true}`},
			wantOut:    "lime now has a wiki: open it at URL/t/lime/wiki, or loadout wiki pull --team lime\n",
		},
		{
			name: "off", status: http.StatusOK, args: []string{"--wiki", "off"},
			wantBodies: []string{`{"wiki":false}`},
			wantOut:    "lime's wiki is off; its history is kept\n",
		},
		{
			name: "with review", status: http.StatusOK, args: []string{"--require-review", "on", "--wiki", "on"},
			wantBodies: []string{`{"require_review":true}`, `{"wiki":true}`},
			wantOut:    "lime now requires review: publish is refused, propose instead\nlime now has a wiki",
		},
		{name: "not on or off", status: http.StatusOK, args: []string{"--wiki", "yes"}, wantErr: `--wiki "yes": want on or off`},
		{name: "refused", status: http.StatusForbidden, args: []string{"--wiki", "on"}, wantBodies: []string{`{"wiki":true}`}, wantErr: "needs an owner"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			url, bodies := settingsRegistry(t, tc.status)
			d, store := testDeps(t)
			cfg, err := store.Load()
			if err != nil {
				t.Fatal(err)
			}
			if err := cfg.Add(remote.Remote{Name: "reg", URL: url}); err != nil {
				t.Fatal(err)
			}
			if err := store.Save(cfg); err != nil {
				t.Fatal(err)
			}
			if err := store.SetToken("reg", testToken); err != nil {
				t.Fatal(err)
			}
			out, err := runCmd(t, d, "", append([]string{"namespace", "set", "lime", "--remote", "reg"}, tc.args...)...)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Errorf("error = %v, want %q", err, tc.wantErr)
				}
			} else if err != nil {
				t.Fatalf("namespace set: %v\n%s", err, out)
			}
			if want := strings.ReplaceAll(tc.wantOut, "URL", url); !strings.Contains(out, want) {
				t.Errorf("output = %q, want %q", out, want)
			}
			if !slices.Equal(*bodies, tc.wantBodies) {
				t.Errorf("requests = %q, want %q", *bodies, tc.wantBodies)
			}
		})
	}
}

// On the registry host loadout does not know the site's address, so it
// names the tab instead.
func TestPrintWikiSwitchOnTheHost(t *testing.T) {
	var out bytes.Buffer
	printWikiSwitch(&out, api.Namespace{Name: "lime", Wiki: true}, "")
	if got, want := out.String(), "lime now has a wiki: open its Wiki tab, or loadout wiki pull --team lime\n"; got != want {
		t.Errorf("output = %q, want %q", got, want)
	}
}

// courseRegistry answers PATCH /v1/namespaces/lime as a registry that
// knows courses, or, when old, as one from before them; acme above lime
// has the course acme/onboarding. It records each body it was sent.
func courseRegistry(t *testing.T, old bool) (string, *[]string) {
	t.Helper()
	var bodies []string
	ns := api.Namespace{Name: "lime", Parent: "acme", Kind: "team"}
	mux := http.NewServeMux()
	mux.HandleFunc("PATCH /v1/namespaces/lime", func(w http.ResponseWriter, r *http.Request) {
		var req api.NamespaceSettings
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Error(err)
		}
		b, _ := json.Marshal(req)
		bodies = append(bodies, string(b))
		w.Header().Set("Content-Type", "application/json")
		if old {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":{"code":"invalid","message":"invalid request: json: unknown field \"course\""}}`))
			return
		}
		ns.CourseAbove, ns.CourseFrom = "", ""
		if req.Course != nil {
			ns.Course = *req.Course
			if ns.Course == "" {
				ns.CourseAbove, ns.CourseFrom = "acme/onboarding", "acme"
			}
		}
		if req.Wiki != nil {
			ns.Wiki = *req.Wiki
		}
		_ = json.NewEncoder(w).Encode(ns)
	})
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return ts.URL, &bodies
}

func TestNamespaceSetCourse(t *testing.T) {
	tests := []struct {
		name       string
		old        bool
		args       []string
		wantBodies []string
		wantOut    string
		notOut     string
		wantErr    string
	}{
		{
			name: "set while the wiki is off", args: []string{"--course", "lime/lime-course"},
			wantBodies: []string{`{"course":"lime/lime-course"}`},
			wantOut:    "lime's wiki now shows the course lime/lime-course: open it at URL/t/lime/wiki#course\nIt shows once the wiki is on: loadout namespace set lime --wiki on\n",
		},
		{
			name: "set and the wiki on", args: []string{"--course", "lime/lime-course", "--wiki", "on"},
			wantBodies: []string{`{"course":"lime/lime-course"}`, `{"wiki":true}`},
			wantOut:    "lime's wiki now shows the course lime/lime-course: open it at URL/t/lime/wiki#course\nlime now has a wiki",
			notOut:     "It shows once",
		},
		{
			name: "none", args: []string{"--course", "none"},
			wantBodies: []string{`{"course":""}`},
			wantOut:    "lime has no course of its own now; its wiki shows acme/onboarding, from acme\n",
		},
		{
			name: "an older registry", old: true, args: []string{"--course", "lime/lime-course"},
			wantBodies: []string{`{"course":"lime/lime-course"}`},
			wantErr:    "URL does not know courses yet; it needs a newer loadout on the registry",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			url, bodies := courseRegistry(t, tc.old)
			d, store := testDeps(t)
			cfg, err := store.Load()
			if err != nil {
				t.Fatal(err)
			}
			if err := cfg.Add(remote.Remote{Name: "reg", URL: url}); err != nil {
				t.Fatal(err)
			}
			if err := store.Save(cfg); err != nil {
				t.Fatal(err)
			}
			if err := store.SetToken("reg", testToken); err != nil {
				t.Fatal(err)
			}
			out, err := runCmd(t, d, "", append([]string{"namespace", "set", "lime", "--remote", "reg"}, tc.args...)...)
			if tc.wantErr != "" {
				if want := strings.ReplaceAll(tc.wantErr, "URL", url); err == nil || !strings.Contains(err.Error(), want) {
					t.Errorf("error = %v, want %q", err, want)
				}
			} else if err != nil {
				t.Fatalf("namespace set: %v\n%s", err, out)
			}
			if want := strings.ReplaceAll(tc.wantOut, "URL", url); !strings.Contains(out, want) {
				t.Errorf("output = %q, want %q", out, want)
			}
			if tc.notOut != "" && strings.Contains(out, tc.notOut) {
				t.Errorf("output = %q, must not contain %q", out, tc.notOut)
			}
			if !slices.Equal(*bodies, tc.wantBodies) {
				t.Errorf("requests = %q, want %q", *bodies, tc.wantBodies)
			}
		})
	}
}
