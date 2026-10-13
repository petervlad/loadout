package main

import (
	"bytes"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"

	"github.com/petervlad/loadout/internal/config"
	"github.com/petervlad/loadout/internal/store/sqlitestore"
)

func TestServerStats(t *testing.T) {
	dir := t.TempDir()
	store, err := sqlitestore.Open(t.Context(), filepath.Join(dir, dbFile), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	var logs bytes.Buffer
	log := slog.New(slog.NewTextHandler(&logs, nil))

	c, err := serverStats(log, config.Stats{}, "", dir, "test", store, nil)
	if err != nil {
		t.Fatal(err)
	}
	r := c.Report(t.Context())
	if r.Hetzner.Configured || r.Mail.On || !r.Backups.Off || len(r.Disk.DBs) != 2 || r.Disk.DBs[0].DBBytes == 0 || r.Counts.Err != "" {
		t.Errorf("Report() without settings = %+v", r)
	}

	const token = "hz-secret-token-value"
	if _, err := serverStats(log, config.Stats{HetznerToken: token, HetznerAPI: "https://elsewhere.example/v1"}, "", dir, "test", store, nil); err == nil {
		t.Error("serverStats() with an API URL off this machine = nil error, want a refusal")
	} else if strings.Contains(err.Error(), token) {
		t.Errorf("error %q shows the token", err)
	}
	if _, err := serverStats(log, config.Stats{HetznerToken: token, HetznerServer: "167857226"}, "", dir, "test", store, nil); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(logs.String(), token) {
		t.Errorf("the log shows the token:\n%s", logs.String())
	}
	if !strings.Contains(logs.String(), "hetzner=true") {
		t.Errorf("the log does not say Hetzner is on:\n%s", logs.String())
	}
}

func TestServerStatsHost(t *testing.T) {
	dir := t.TempDir()
	store, err := sqlitestore.Open(t.Context(), filepath.Join(dir, dbFile), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	var logs bytes.Buffer
	log := slog.New(slog.NewTextHandler(&logs, nil))

	const token = "hz-secret-token-value"
	st := config.Stats{
		HetznerToken: token, HetznerServer: "old",
		Host: config.Host{Provider: "Example Cloud", Region: "eu-1", Size: "2 vCPU", Contact: "ops", Console: "https://console.example.com"},
	}
	c, err := serverStats(log, st, "https://registry.example.com", dir, "test", store, nil)
	if err != nil {
		t.Fatal(err)
	}
	h := c.Report(t.Context()).Host
	if h.Stated.Provider != "Example Cloud" || h.Stated.Console != "https://console.example.com" || h.PublicURL != "https://registry.example.com" {
		t.Errorf("Host = %+v, want the stated facts and the address", h)
	}
	out := logs.String()
	if !strings.Contains(out, "host_provider=\"Example Cloud\"") || !strings.Contains(out, "level=WARN") {
		t.Errorf("the log lacks the provider or the warning:\n%s", out)
	}
	if strings.Contains(out, token) {
		t.Errorf("the log shows the token:\n%s", out)
	}

	logs.Reset()
	st.Host.Provider = "Hetzner Cloud"
	if _, err := serverStats(log, st, "", dir, "test", store, nil); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(logs.String(), "level=WARN") {
		t.Errorf("warning for a Hetzner host:\n%s", logs.String())
	}
}

// A typo in a host variable stops serve at startup with a sentence that
// names it.
func TestServeRefusesBadHostConsole(t *testing.T) {
	alice := &person{session: newSession(t)}
	var stdout bytes.Buffer
	stderr := &syncBuffer{}
	env := map[string]string{"HOME": alice.home, "LOADOUT_HOST_CONSOLE": "http://console.example.com"}
	code := run(t.Context(), []string{"serve", "--addr", "127.0.0.1:0"}, strings.NewReader(""), &stdout, stderr, func(k string) string { return env[k] }, alice.wd)
	if code == 0 || !strings.Contains(stderr.String(), "LOADOUT_HOST_CONSOLE") {
		t.Errorf("serve = %d, stderr %q, want a failure naming LOADOUT_HOST_CONSOLE", code, stderr.String())
	}
}
