package mcpmemory_test

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/petervlad/loadout/internal/memory"
	"github.com/petervlad/loadout/internal/store/sqlitestore"
)

// marker is shaped like an AWS access key id and is not on the allowlist. It
// is built from parts so no scanner reads this file as a leak.
const marker = "AKIA" + "ZMARKERQWERTY123"

// holdsMarker reports every place in the store that holds the marker: any
// field of any entry or any version.
func (r *rig) holdsMarker(t *testing.T) []string {
	t.Helper()
	var found []string
	es, err := r.store.Recent(t.Context(), []string{"lime", "acme"}, time.Time{}, 1000)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range es {
		for _, field := range []string{e.Name, e.Description, e.Body, e.AuthorLabel} {
			if strings.Contains(field, marker) {
				found = append(found, "entry")
			}
		}
		vs, err := r.store.History(t.Context(), e.ID)
		if err != nil {
			t.Fatal(err)
		}
		for _, v := range vs {
			if strings.Contains(v.Name+v.Description+v.Body+v.Note, marker) {
				found = append(found, "version")
			}
		}
	}
	return found
}

func TestSecretRefusal(t *testing.T) {
	r := newRig(t)
	s := r.connect(t, secDana, "lime")
	res := s.call(t, "save", map[string]any{
		"type": "reference", "name": "Deploy key", "description": "how to deploy", "body": "line one\nkey is " + marker + " ok",
	})
	if !res.IsError {
		t.Fatal("a secret was accepted")
	}
	got := text(t, res)
	if !strings.Contains(got, "aws-access-key-id") || !strings.Contains(got, "line 2") || !strings.Contains(got, "column 8") {
		t.Errorf("refusal does not name the rule and position: %s", got)
	}
	if strings.Contains(got, marker) || strings.Contains(got, "AKIA") {
		t.Errorf("refusal repeats the secret: %s", got)
	}
	if res.StructuredContent != nil && strings.Contains(string(mustJSON(t, res.StructuredContent)), "AKIA") {
		t.Error("structured content repeats the secret")
	}
}

func TestSecretStoredNowhere(t *testing.T) {
	storedNowhere(t, newRig(t))
}

// TestSecretStoredNowhereOnDisk runs the same attempts against the real
// SQLite store, then reads the raw bytes of memory.db and its log, and the
// tables behind the search index, for the marker.
func TestSecretStoredNowhereOnDisk(t *testing.T) {
	path := filepath.Join(t.TempDir(), "memory.db")
	mem, err := sqlitestore.OpenMemory(t.Context(), path, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = mem.Close() })
	r := newRigOn(t, mem, mem.Switches())
	storedNowhere(t, r)

	// A control: a clean write with its own marker is found by the same
	// reading, so a miss above means the secret is not there.
	const control = "CONTROLMARK-4417"
	if res := r.connect(t, secDana, "lime").call(t, "save", map[string]any{
		"type": "project", "name": "Control", "description": "d", "body": control,
	}); res.IsError {
		t.Fatalf("control save: %s", text(t, res))
	}

	raw, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	// Move what the log holds into the file, then read both: the secret must
	// be in neither.
	if _, err := raw.ExecContext(t.Context(), `PRAGMA wal_checkpoint(FULL)`); err != nil {
		t.Fatal(err)
	}
	held := map[string]string{}
	for _, f := range []string{path, path + "-wal"} {
		b, err := os.ReadFile(f)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			t.Fatal(err)
		}
		held[filepath.Base(f)] = string(b)
	}
	// The shadow tables of the full-text index, read as the bytes they hold.
	for _, table := range []string{"entries_fts_data", "entries_fts_idx", "entries_fts_docsize", "entries_fts_config"} {
		held[table] = shadowTable(t, raw, table)
	}
	for where, content := range held {
		if strings.Contains(content, marker) {
			t.Errorf("%s holds the secret", where)
		}
	}
	if !strings.Contains(held["memory.db"]+held["memory.db-wal"]+held["entries_fts_data"], control) {
		t.Error("the control text is in none of the files, so reading them proves nothing")
	}
}

func storedNowhere(t *testing.T, r *rig) {
	t.Helper()
	s := r.connect(t, secDana, "lime")
	ok := r.seed(t, "lime", "Plain fact", "nothing secret", "body", dana)

	attempts := []struct {
		tool string
		args map[string]any
	}{
		{"save", map[string]any{"type": "project", "name": "In body", "description": "d", "body": "k " + marker}},
		{"save", map[string]any{"type": "project", "name": "Name " + marker, "description": "d", "body": "b"}},
		{"save", map[string]any{"type": "project", "name": "n", "description": "key " + marker, "body": "b"}},
		{"update", map[string]any{"id": ok.ID, "body": "now " + marker}},
		{"update", map[string]any{"id": ok.ID, "confirm": true, "note": marker}},
		{"retract", map[string]any{"id": ok.ID, "reason": marker}},
	}
	for _, a := range attempts {
		res := s.call(t, a.tool, a.args)
		if !res.IsError {
			t.Errorf("%s %v accepted a secret", a.tool, a.args)
		}
	}

	if places := r.holdsMarker(t); len(places) > 0 {
		t.Errorf("the store holds the secret in %v", places)
	}
	if e, _ := r.store.Get(t.Context(), ok.ID); e.Version != 1 || e.Status != memory.StatusActive {
		t.Errorf("a refused write changed the entry: %+v", e)
	}
	if strings.Contains(s.wire.everything(), marker) {
		t.Error("a response holds the secret")
	}
	if strings.Contains(r.logs.String(), marker) {
		t.Errorf("the log holds the secret:\n%s", r.logs.String())
	}
	// And a clean write afterwards still works.
	if res := s.call(t, "update", map[string]any{"id": ok.ID, "body": "clean"}); res.IsError {
		t.Errorf("clean update refused: %s", text(t, res))
	}
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// goList runs go list in this package and returns its output lines.
func goList(t *testing.T, args ...string) []string {
	t.Helper()
	goBin, err := exec.LookPath("go")
	if err != nil {
		t.Skipf("go is not on PATH: %v", err)
	}
	out, err := exec.CommandContext(t.Context(), goBin, append([]string{"list"}, args...)...).Output()
	if err != nil {
		t.Skipf("go list is not available: %v", err)
	}
	return strings.Fields(string(out))
}

// aiSDKs are parts of import paths that belong to a model vendor's SDK or to
// a framework that calls models.
var aiSDKs = []string{
	"anthropic", "openai", "genai", "generative-ai", "generativeai", "langchain", "ollama", "cohere", "mistral",
	"vertexai", "bedrock", "aws-sdk", "huggingface", "llama", "gemini", "tiktoken",
}

// modelSDK returns the part of aiSDKs that import path d holds, or "". The
// packages of this module are not looked at: the adapter for Claude Code is
// named after the tool, and calls no model.
func modelSDK(d string) string {
	if strings.HasPrefix(d, "github.com/petervlad/loadout/") {
		return ""
	}
	for _, banned := range aiSDKs {
		if strings.Contains(strings.ToLower(d), banned) {
			return banned
		}
	}
	return ""
}

// TestWholeBinaryHasNoModelSDK: the rule holds for everything loadout builds,
// not only the memory server. Nothing in the dependencies of cmd/loadout is an
// AI SDK, since loadout never runs a model and never holds an AI key.
func TestWholeBinaryHasNoModelSDK(t *testing.T) {
	deps := goList(t, "-deps", "../../cmd/loadout")
	if len(deps) < 100 {
		t.Fatalf("go list -deps ../../cmd/loadout found %d packages, the check proves nothing", len(deps))
	}
	for _, d := range deps {
		if banned := modelSDK(d); banned != "" {
			t.Errorf("the loadout binary depends on %s (matches %q)", d, banned)
		}
	}
}

func TestModelSDKNames(t *testing.T) {
	for path, want := range map[string]string{
		"github.com/anthropics/anthropic-sdk-go":                  "anthropic",
		"github.com/sashabaranov/go-openai":                       "openai",
		"github.com/google/generative-ai-go/genai":                "genai",
		"cloud.google.com/go/vertexai/genai":                      "genai",
		"github.com/aws/aws-sdk-go-v2/service/bedrockruntime":     "bedrock",
		"github.com/petervlad/loadout/internal/target/claudecode": "",
		"github.com/modelcontextprotocol/go-sdk/mcp":              "",
		"modernc.org/sqlite":                                      "",
	} {
		if got := modelSDK(path); got != want {
			t.Errorf("modelSDK(%q) = %q, want %q", path, got, want)
		}
	}
}

// TestNoModelImports holds the one rule: the memory server calls no model and
// holds no AI key. No package it builds on is an AI SDK, and it imports
// nothing that talks to a mail, cloud or registry server of its own.
func TestNoModelImports(t *testing.T) {
	deps := goList(t, "-deps", ".")
	for _, d := range deps {
		if banned := modelSDK(d); banned != "" {
			t.Errorf("depends on %s", d)
		}
	}
	imports := goList(t, "-f", "{{join .Imports \"\\n\"}}", ".")
	for _, imp := range imports {
		for _, banned := range []string{
			"/internal/mail", "/internal/hetzner", "/internal/client", "/internal/remote", "/internal/review",
			"golang.org/x/oauth2", "go-oidc", "net/smtp",
		} {
			if strings.Contains(imp, banned) {
				t.Errorf("imports %s, which reaches out to another server", imp)
			}
		}
	}
}

// shadowTable returns every value of a table behind the search index as text,
// blobs as their bytes.
func shadowTable(t *testing.T, raw *sql.DB, table string) string {
	t.Helper()
	rows, err := raw.QueryContext(t.Context(), `SELECT * FROM `+table) //nolint:gosec // fixed table names of the index
	if err != nil {
		t.Fatalf("read %s: %v", table, err)
	}
	defer rows.Close()
	cols, _ := rows.Columns()
	var all strings.Builder
	for rows.Next() {
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			t.Fatal(err)
		}
		for _, v := range vals {
			fmt.Fprint(&all, v, " ")
			if b, ok := v.([]byte); ok {
				all.Write(b)
			}
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return all.String()
}
