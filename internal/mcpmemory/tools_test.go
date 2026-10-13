package mcpmemory_test

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/petervlad/loadout/internal/memory"
)

var update = flag.Bool("update", false, "rewrite golden files")

func TestToolsList(t *testing.T) {
	r := newRig(t)
	s := r.connect(t, secDana, "lime")
	got, err := s.ListTools(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, tool := range got.Tools {
		names = append(names, tool.Name)
		sch, ok := tool.InputSchema.(map[string]any)
		if !ok {
			t.Fatalf("%s: schema is %T", tool.Name, tool.InputSchema)
		}
		if sch["type"] != "object" || sch["additionalProperties"] != false {
			t.Errorf("%s: schema is not a closed object: %v", tool.Name, sch)
		}
	}
	slices.Sort(names) // the SDK lists tools by name
	want := []string{"get", "recent", "retract", "save", "search", "update"}
	if !slices.Equal(names, want) {
		t.Errorf("tools = %v, want %v", names, want)
	}
	if in := s.InitializeResult().Instructions; !strings.HasPrefix(in, "Team memory for lime (and acme above it)") {
		t.Errorf("instructions start with %q", in[:min(len(in), 60)])
	}
}

// TestHandshakeGolden pins the exact JSON of initialize and tools/list, so an
// upgrade of the SDK that changes the wire shape shows up as a diff. Run with
// -update to rewrite it.
func TestHandshakeGolden(t *testing.T) {
	r := newRig(t)
	out := map[string]json.RawMessage{}
	for i, m := range []struct{ method, params string }{{"initialize", initParams}, {"tools/list", ""}} {
		status, _, body := r.post(t, secDana, "lime", rpc(i+1, m.method, m.params))
		if status != 200 {
			t.Fatalf("%s: status %d: %s", m.method, status, body)
		}
		out[m.method] = json.RawMessage(strings.TrimSpace(body))
	}
	var pretty bytes.Buffer
	raw, err := json.Marshal(out) // sorted keys
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Indent(&pretty, raw, "", "  "); err != nil {
		t.Fatal(err)
	}
	pretty.WriteByte('\n')
	const path = "testdata/handshake.golden.json"
	if *update {
		if err := os.WriteFile(path, pretty.Bytes(), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(pretty.Bytes(), want) {
		t.Errorf("handshake changed, run go test -update and read the diff.\ngot:\n%s", pretty.String())
	}
}

func TestSaveGoesToTeam(t *testing.T) {
	r := newRig(t)
	s := r.connect(t, secDana, "lime")
	res := s.call(t, "save", map[string]any{
		"type": "reference", "name": "TRK Tracker host", "description": "TRK tickets live on tracker.example", "body": "never atc",
	})
	if res.IsError {
		t.Fatalf("save failed: %s", text(t, res))
	}
	es, err := r.store.Recent(t.Context(), []string{"lime", "acme"}, r.clock.now().AddDate(0, 0, -1), 10)
	if err != nil || len(es) != 1 {
		t.Fatalf("recent = %v, %v", es, err)
	}
	if es[0].Namespace != "lime" || es[0].AuthorID != "dana" || es[0].AuthorLabel != "dana@example.com" {
		t.Errorf("saved as %+v", es[0])
	}
}

func TestSearchCoversProject(t *testing.T) {
	r := newRig(t)
	r.seed(t, "lime", "Release label", "Release PRs need the release label", "", dana)
	r.seed(t, "acme", "Release freeze", "Release freeze starts on Monday", "", dana)
	s := r.connect(t, secDana, "lime")

	res := s.call(t, "search", map[string]any{"query": "release"})
	got := text(t, res)
	for _, ns := range []string{`namespace="lime"`, `namespace="acme"`} {
		if !strings.Contains(got, ns) {
			t.Errorf("search result lacks %s:\n%s", ns, got)
		}
	}
	only := text(t, s.call(t, "search", map[string]any{"query": "release", "namespace": "acme"}))
	if strings.Contains(only, `namespace="lime"`) || !strings.Contains(only, `namespace="acme"`) {
		t.Errorf("namespace filter ignored:\n%s", only)
	}
	other := s.call(t, "search", map[string]any{"query": "release", "namespace": "elsewhere"})
	if !other.IsError {
		t.Error("a namespace outside the layers was accepted")
	}
	typed := text(t, s.call(t, "search", map[string]any{"query": "release", "type": "feedback"}))
	if strings.Contains(typed, "<memory") {
		t.Errorf("type filter ignored:\n%s", typed)
	}
}

func TestWriteNeedsWriteRight(t *testing.T) {
	r := newRig(t)
	args := map[string]any{"type": "project", "name": "Freeze", "description": "d", "body": "b"}
	res := r.connect(t, secOtherSub, "lime").call(t, "save", args)
	if !res.IsError || !strings.Contains(text(t, res), "may not") {
		t.Errorf("a reader saved or got no reason: %s", text(t, res))
	}
	// A project member writes to the project endpoint, and only there.
	if res := r.connect(t, secPM, "acme").call(t, "save", args); res.IsError {
		t.Fatalf("project member could not save: %s", text(t, res))
	}
	if es, _ := r.store.Recent(t.Context(), []string{"lime"}, r.clock.now().AddDate(0, 0, -1), 10); len(es) != 0 {
		t.Error("a project save landed in the team")
	}
}

func TestGetShowsLinksAndCountsUse(t *testing.T) {
	r := newRig(t)
	a := r.seed(t, "lime", "Alpha fact", "first", "body a", dana)
	d, err := memory.NewDraft("lime", memory.TypeProject, "Beta fact", "second", "body b", []int64{a.ID})
	if err != nil {
		t.Fatal(err)
	}
	b, err := r.store.Insert(t.Context(), d, dana, r.clock.now())
	if err != nil {
		t.Fatal(err)
	}
	s := r.connect(t, secLee, "lime")
	res := s.call(t, "get", map[string]any{"id": b.ID})
	got := text(t, res)
	if !strings.Contains(got, "body b") || !strings.Contains(got, "[1] Alpha fact") {
		t.Errorf("get lacks body or link:\n%s", got)
	}
	m := res.StructuredContent.(map[string]any)["memory"].(map[string]any)
	if m["kind"] != "memory" || m["version"] != float64(1) {
		t.Errorf("structured = %v", m)
	}
	if e, _ := r.store.Get(t.Context(), b.ID); e.Uses != 1 {
		t.Errorf("uses = %d, want 1", e.Uses)
	}
	if miss := s.call(t, "get", map[string]any{"id": 999}); !miss.IsError {
		t.Error("get of a missing id succeeded")
	}
}

func TestUpdateConfirmAndRetract(t *testing.T) {
	r := newRig(t)
	e := r.seed(t, "lime", "Old fact", "was true", "body", dana)
	r.clock.advance(200 * 24 * time.Hour)
	s := r.connect(t, secLee, "lime")

	if got := text(t, s.call(t, "search", map[string]any{"query": "fact"})); !strings.Contains(got, "unconfirmed_since=") {
		t.Errorf("an old memory is not marked:\n%s", got)
	}
	res := s.call(t, "update", map[string]any{"id": e.ID, "confirm": true})
	if res.IsError {
		t.Fatalf("confirm failed: %s", text(t, res))
	}
	if got := text(t, res); strings.Contains(got, "unconfirmed_since=") {
		t.Errorf("a confirmed memory is still marked:\n%s", got)
	}
	if res := s.call(t, "update", map[string]any{"id": e.ID}); !res.IsError {
		t.Error("an update that changes nothing was accepted")
	}
	// lee did not write it and does not moderate: refused.
	if res := s.call(t, "retract", map[string]any{"id": e.ID, "reason": "wrong"}); !res.IsError {
		t.Error("a member retracted another member's memory")
	}
	if res := r.connect(t, secBoss, "lime").call(t, "retract", map[string]any{"id": e.ID, "reason": "wrong"}); res.IsError {
		t.Errorf("a maintainer could not retract: %s", text(t, res))
	}
	if got := text(t, s.call(t, "search", map[string]any{"query": "fact"})); strings.Contains(got, "<memory") {
		t.Errorf("a retracted memory is still found:\n%s", got)
	}
	if got := text(t, s.call(t, "recent", nil)); strings.Contains(got, "<memory") {
		t.Errorf("a retracted memory is in recent:\n%s", got)
	}
}

func TestRecent(t *testing.T) {
	r := newRig(t)
	r.seed(t, "lime", "Fresh", "just now", "", dana)
	s := r.connect(t, secDana, "lime")
	if got := text(t, s.call(t, "recent", map[string]any{"days": 1})); !strings.Contains(got, "Fresh") {
		t.Errorf("recent lacks the new memory:\n%s", got)
	}
	r.clock.advance(10 * 24 * time.Hour)
	if got := text(t, s.call(t, "recent", nil)); strings.Contains(got, "<memory") {
		t.Errorf("recent shows a 10 day old memory for 7 days:\n%s", got)
	}
}

func TestSaveReturnsDuplicates(t *testing.T) {
	r := newRig(t)
	r.seed(t, "lime", "TRK Tracker host", "tickets live on tracker.example", "", dana)
	s := r.connect(t, secLee, "lime")
	args := map[string]any{"type": "reference", "name": "Tracker host for TRK", "description": "tickets are on tracker.example", "body": "x"}

	res := s.call(t, "save", args)
	if res.IsError {
		t.Fatalf("duplicate answer is an error: %s", text(t, res))
	}
	got := text(t, res)
	if !strings.Contains(got, "Nothing was saved") || !strings.Contains(got, "new: true") || !strings.Contains(got, `id="1"`) {
		t.Errorf("duplicates answer:\n%s", got)
	}
	sc := res.StructuredContent.(map[string]any)
	if v, present := sc["saved"]; !present || v != nil {
		t.Errorf("saved = %v, want null", v)
	}
	if d, _ := sc["duplicates"].([]any); len(d) != 1 {
		t.Errorf("duplicates = %v", sc["duplicates"])
	}
	if es, _ := r.store.Recent(t.Context(), []string{"lime"}, r.clock.now().AddDate(0, 0, -1), 10); len(es) != 1 {
		t.Errorf("a duplicate answer saved something: %d entries", len(es))
	}

	args["new"] = true
	if res := s.call(t, "save", args); res.IsError || res.StructuredContent.(map[string]any)["saved"] == nil {
		t.Fatalf("save with new: true: %s", text(t, res))
	}
	if es, _ := r.store.Recent(t.Context(), []string{"lime"}, r.clock.now().AddDate(0, 0, -1), 10); len(es) != 2 {
		t.Errorf("entries = %d, want 2", len(es))
	}
}

func TestToolErrorsNeverEchoInput(t *testing.T) {
	r := newRig(t)
	s := r.connect(t, secDana, "lime")
	const marker = "ZEBRAMARKER99"
	for name, tc := range map[string]struct {
		tool string
		args any
	}{
		"unknown field":  {"get", map[string]any{"id": 1, marker: 1}},
		"wrong type":     {"get", map[string]any{"id": marker}},
		"bad enum":       {"search", map[string]any{"query": "x", "type": marker}},
		"bad namespace":  {"search", map[string]any{"query": "x", "namespace": marker}},
		"limit range":    {"search", map[string]any{"query": "x", "limit": 99}},
		"days range":     {"recent", map[string]any{"days": 99}},
		"bad save type":  {"save", map[string]any{"type": marker, "name": "n", "description": "d", "body": "b"}},
		"long name":      {"save", map[string]any{"type": "project", "name": strings.Repeat(marker, 10), "description": "d", "body": "b"}},
		"empty reason":   {"retract", map[string]any{"id": 1, "reason": " "}},
		"bad link":       {"save", map[string]any{"type": "project", "name": "n", "description": "d", "body": marker, "links": []int{4242}}},
		"missing id":     {"update", map[string]any{"note": marker}},
		"stuff in query": {"search", map[string]any{"query": "   "}},
	} {
		t.Run(name, func(t *testing.T) {
			res := s.call(t, tc.tool, tc.args)
			if !res.IsError {
				t.Fatalf("not an error: %s", text(t, res))
			}
			if got := text(t, res); strings.Contains(got, marker) || got == "" {
				t.Errorf("error %q echoes the input or is empty", got)
			}
		})
	}
	if strings.Contains(r.logs.String(), marker) {
		t.Error("the log holds request text")
	}
}

func TestRawArgumentErrors(t *testing.T) {
	r := newRig(t)
	for name, args := range map[string]string{
		"not json":     `"x"`,
		"two values":   `{} {}`,
		"array":        `[1]`,
		"broken":       `{"id": 1`,
		"unknown":      `{"zzz": 1}`,
		"fractional":   `{"id": 1.5}`,
		"nested wrong": `{"links": "a"}`,
	} {
		t.Run(name, func(t *testing.T) {
			body := rpc(1, "tools/call", `{"name":"get","arguments":`+args+`}`)
			status, _, resp := r.post(t, secDana, "lime", body)
			if status != 200 && status != 400 {
				t.Fatalf("status %d: %s", status, resp)
			}
			if status == 200 && strings.Contains(resp, "invalid character") {
				t.Errorf("a decoder message reached the client: %s", resp)
			}
		})
	}
}
