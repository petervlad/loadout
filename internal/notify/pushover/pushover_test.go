package pushover

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/petervlad/loadout/internal/pack"
	"github.com/petervlad/loadout/internal/semver"
	"github.com/petervlad/loadout/internal/update"
)

func notification(t *testing.T) update.Notification {
	t.Helper()
	id, err := pack.NewID("lime", "planner")
	if err != nil {
		t.Fatal(err)
	}
	return update.Notification{ID: id, Current: semver.New(1, 0, 0), Latest: semver.New(1, 1, 0), Remote: "office"}
}

func TestNotifySendsTheMessage(t *testing.T) {
	var got http.Header
	var form map[string]string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header
		if err := r.ParseForm(); err != nil {
			t.Error(err)
		}
		form = map[string]string{}
		for k := range r.PostForm {
			form[k] = r.PostForm.Get(k)
		}
		_, _ = w.Write([]byte(`{"status":1,"request":"abc"}`))
	}))
	defer srv.Close()

	n, err := New("app-token", "user-key", WithEndpoint(srv.URL))
	if err != nil {
		t.Fatal(err)
	}
	if err := n.Notify(t.Context(), notification(t)); err != nil {
		t.Fatalf("Notify() error: %v", err)
	}
	if got.Get("Content-Type") != "application/x-www-form-urlencoded" {
		t.Errorf("Content-Type = %q", got.Get("Content-Type"))
	}
	if form["token"] != "app-token" || form["user"] != "user-key" {
		t.Errorf("credentials not sent: %v", form)
	}
	if !strings.Contains(form["title"], "lime/planner 1.1.0") || !strings.Contains(form["message"], "1.0.0") {
		t.Errorf("title %q message %q, want versions", form["title"], form["message"])
	}
}

func TestNotifyModelsChanged(t *testing.T) {
	var form map[string]string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Error(err)
		}
		form = map[string]string{"title": r.PostForm.Get("title"), "message": r.PostForm.Get("message")}
		_, _ = w.Write([]byte(`{"status":1,"request":"abc"}`))
	}))
	defer srv.Close()
	n, err := New("app-token", "user-key", WithEndpoint(srv.URL))
	if err != nil {
		t.Fatal(err)
	}
	x := notification(t)
	x.Latest, x.ModelsChanged = x.Current, true
	if err := n.Notify(t.Context(), x); err != nil {
		t.Fatal(err)
	}
	if form["title"] != "lime/planner: model settings changed" || !strings.Contains(form["message"], "loadout install lime/planner") {
		t.Errorf("title %q message %q", form["title"], form["message"])
	}
}

func TestNotifyErrors(t *testing.T) {
	tests := []struct {
		name    string
		status  int
		body    string
		wantErr string
	}{
		{"invalid user", http.StatusBadRequest, `{"status":0,"errors":["user identifier is invalid"]}`, "user identifier is invalid"},
		{"not json", http.StatusBadGateway, `<html>`, "unreadable"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer srv.Close()
			n, err := New("app-token", "user-key", WithEndpoint(srv.URL))
			if err != nil {
				t.Fatal(err)
			}
			err = n.Notify(t.Context(), notification(t))
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("Notify() error = %v, want %q", err, tc.wantErr)
			}
			if err != nil && strings.Contains(err.Error(), "app-token") {
				t.Error("error message contains the app token")
			}
		})
	}
}

func TestNewNeedsBothKeys(t *testing.T) {
	if _, err := New("", "user"); err == nil {
		t.Error("New without app token succeeded")
	}
	if _, err := New("app", ""); err == nil {
		t.Error("New without user key succeeded")
	}
}

func TestText(t *testing.T) {
	base := notification(t)
	held := base
	held.Held = []string{".claude/agents/planner.md"}
	heldModels := held
	heldModels.Latest, heldModels.ModelsChanged, heldModels.Held = base.Current, true, []string{"a.md", "b.md"}
	tests := []struct {
		name         string
		x            update.Notification
		title, start string
	}{
		{"available", base, "lime/planner 1.1.0 available", "You have 1.0.0. Update with: loadout install lime/planner"},
		{"held back", held, "lime/planner 1.1.0 held back", "You edited 1 file, so loadout kept 1.0.0. Keep your edits and update with: loadout install lime/planner --merge"},
		{"model settings held back", heldModels, "lime/planner: model settings held back", "Your team changed which model lime/planner runs, and you edited 2 files"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			title, message := text(tc.x)
			if title != tc.title || !strings.HasPrefix(message, tc.start) {
				t.Errorf("text() = %q, %q; want %q, %q...", title, message, tc.title, tc.start)
			}
			if strings.Contains(message, ".md") {
				t.Errorf("message %q names a file; only a count may leave the machine", message)
			}
		})
	}
}

func TestPushSendsTitleAndMessage(t *testing.T) {
	var form map[string]string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Error(err)
		}
		form = map[string]string{"title": r.PostForm.Get("title"), "message": r.PostForm.Get("message"), "user": r.PostForm.Get("user")}
		_, _ = w.Write([]byte(`{"status":1}`))
	}))
	defer srv.Close()
	n, err := New("app-token", "user-key", WithEndpoint(srv.URL))
	if err != nil {
		t.Fatal(err)
	}
	if err := n.Push(t.Context(), "loadout 0.5.0 is out", "You have 0.4.0."); err != nil {
		t.Fatal(err)
	}
	if form["title"] != "loadout 0.5.0 is out" || form["message"] != "You have 0.4.0." || form["user"] != "user-key" {
		t.Errorf("sent %v", form)
	}
}
