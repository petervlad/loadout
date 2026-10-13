package main

import (
	"bytes"
	"log/slog"
	"path/filepath"
	"strconv"
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

	c, err := serverStats(log, config.Stats{}, "", dir, "test", store, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	r := c.Report(t.Context())
	if r.Cloud.Configured || r.Mail.On || !r.Backups.Off || len(r.Disk.DBs) != 2 || r.Disk.DBs[0].DBBytes == 0 || r.Counts.Err != "" {
		t.Errorf("Report() without settings = %+v", r)
	}

	const token = "hz-secret-token-value"
	if _, err := serverStats(log, config.Stats{HetznerToken: token, HetznerAPI: "https://elsewhere.example/v1"}, "", dir, "test", store, nil, nil); err == nil {
		t.Error("serverStats() with an API URL off this machine = nil error, want a refusal")
	} else if strings.Contains(err.Error(), token) {
		t.Errorf("error %q shows the token", err)
	}
	if _, err := serverStats(log, config.Stats{HetznerToken: token, HetznerServer: "167857226"}, "", dir, "test", store, nil, nil); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(logs.String(), token) {
		t.Errorf("the log shows the token:\n%s", logs.String())
	}
	if !strings.Contains(logs.String(), "cloud=Hetzner") {
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
	c, err := serverStats(log, st, "https://registry.example.com", dir, "test", store, nil, nil)
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
	if _, err := serverStats(log, st, "", dir, "test", store, nil, nil); err != nil {
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

// TestServerStatsSiteCertificates: the certificate part needs an https
// address, and it only ever dials this machine.
func TestServerStatsSiteCertificates(t *testing.T) {
	dir := t.TempDir()
	store, err := sqlitestore.Open(t.Context(), filepath.Join(dir, dbFile), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	var logs bytes.Buffer
	log := slog.New(slog.NewTextHandler(&logs, nil))
	tests := []struct {
		name, pub string
		wantOn    bool
	}{
		{"no public address", "", false},
		{"plain http address", "http://localhost:7431", false},
		{"https address", "https://registry.example.com:1", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			logs.Reset()
			c, err := serverStats(log, config.Stats{}, tt.pub, dir, "test", store, nil, nil)
			if err != nil {
				t.Fatal(err)
			}
			r := c.Report(t.Context())
			if r.CertsOn != tt.wantOn {
				t.Errorf("CertsOn = %v, want %v", r.CertsOn, tt.wantOn)
			}
			if tt.wantOn && r.Certs.Served.Err == "" {
				t.Errorf("Certs = %+v, want the failed handshake to say why", r.Certs)
			}
			if !strings.Contains(logs.String(), "certificates="+strconv.FormatBool(tt.wantOn)) {
				t.Errorf("the log does not say whether certificates are checked:\n%s", logs.String())
			}
		})
	}
}
