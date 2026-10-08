package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/petervlad/loadout/internal/api"
)

// syncBuffer is a bytes.Buffer safe to write from the serving goroutine
// while the test reads it.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// listeningRE finds the address the server logs once it is bound.
var listeningRE = regexp.MustCompile(`msg=listening addr=(\S+)`)

// health calls GET /v1/health, with token when it is not empty.
func health(t *testing.T, base, token string) api.HealthResponse {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, base+"/v1/health", nil)
	if err != nil {
		t.Fatal(err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	c := &http.Client{Timeout: 10 * time.Second}
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /v1/health: status %d, want 200", resp.StatusCode)
	}
	var h api.HealthResponse
	if err := json.NewDecoder(resp.Body).Decode(&h); err != nil {
		t.Fatal(err)
	}
	return h
}

func TestServe(t *testing.T) {
	alice := &person{session: newSession(t)}
	ctx, cancel := context.WithCancel(t.Context())
	var stdout bytes.Buffer
	stderr := &syncBuffer{}
	env := map[string]string{"HOME": alice.home}
	var code int
	exited := make(chan struct{})
	go func() {
		defer close(exited)
		code = run(ctx, []string{"serve", "--addr", "127.0.0.1:0"}, strings.NewReader(""), &stdout, stderr, func(k string) string { return env[k] }, alice.wd)
	}()
	// A failing test must not leave the server running past its temp dirs.
	t.Cleanup(func() {
		cancel()
		<-exited
	})

	// The port is picked by the kernel; serve logs it once bound.
	var addr string
	for deadline := time.Now().Add(10 * time.Second); addr == ""; {
		if m := listeningRE.FindStringSubmatch(stderr.String()); m != nil {
			addr = m[1]
			break
		}
		select {
		case <-exited:
			t.Fatalf("serve exited with %d before listening\nstderr: %s", code, stderr.String())
		case <-time.After(10 * time.Millisecond):
		}
		if time.Now().After(deadline) {
			t.Fatalf("serve did not log its address\nstderr: %s", stderr.String())
		}
	}
	base := "http://" + addr

	tok := createToken(t, alice, "read")
	if h := health(t, base, ""); h.Status != "ok" || h.Version != "" {
		t.Errorf("health without a token = %+v, want status ok and no version", h)
	}
	if h := health(t, base, tok); h.Status != "ok" || h.Version != version {
		t.Errorf("health with a token = %+v, want status ok and version %q", h, version)
	}

	// SQLite removes the WAL file when the last connection closes, so it
	// shows whether serve still holds the database.
	wal := filepath.Join(alice.home, ".loadout", "server", "registry.db-wal")
	if _, err := os.Stat(wal); err != nil {
		t.Fatalf("while serving: %v, want the database open", err)
	}

	cancel()
	select {
	case <-exited:
	case <-time.After(30 * time.Second):
		t.Fatalf("serve did not stop after cancel\nstderr: %s", stderr.String())
	}
	if code != 0 {
		t.Errorf("serve exit code = %d, want 0\nstderr: %s", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "msg=stopped") {
		t.Errorf("serve did not log a clean stop\nstderr: %s", stderr.String())
	}
	if _, err := os.Stat(wal); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("after serve: stat %s = %v, want the database closed", wal, err)
	}
}
