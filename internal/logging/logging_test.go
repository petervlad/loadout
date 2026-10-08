package logging

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTerminalLevelAndFileAlwaysDebug(t *testing.T) {
	var stderr bytes.Buffer
	path := filepath.Join(t.TempDir(), "state", "loadout.log")
	l, _, closer := New(&stderr, slog.LevelWarn, path)
	l.Debug("debug detail", "k", "v")
	l.Warn("a warning")
	if err := closer.Close(); err != nil {
		t.Fatal(err)
	}

	if strings.Contains(stderr.String(), "debug detail") {
		t.Error("terminal shows debug at warn level")
	}
	if !strings.Contains(stderr.String(), "a warning") {
		t.Error("terminal is missing the warning")
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 2 {
		t.Fatalf("log file has %d lines, want 2:\n%s", len(lines), data)
	}
	var rec map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &rec); err != nil {
		t.Fatalf("log file line is not JSON: %v", err)
	}
	if rec["msg"] != "debug detail" || rec["k"] != "v" {
		t.Errorf("first record = %v, want the debug message with k=v", rec)
	}
}

func TestLevelCanBeRaisedLater(t *testing.T) {
	var stderr bytes.Buffer
	l, level, closer := New(&stderr, slog.LevelWarn, filepath.Join(t.TempDir(), "loadout.log"))
	defer closer.Close()
	level.Set(slog.LevelDebug)
	l.Debug("now visible")
	if !strings.Contains(stderr.String(), "now visible") {
		t.Error("raising Level did not show debug on the terminal")
	}
}

func TestRotation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "loadout.log")
	if err := os.WriteFile(path, bytes.Repeat([]byte("x"), maxFileSize+1), 0o600); err != nil {
		t.Fatal(err)
	}
	l, _, closer := New(&bytes.Buffer{}, slog.LevelWarn, path)
	l.Info("fresh")
	if err := closer.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path + ".1"); err != nil {
		t.Errorf("rotated file missing: %v", err)
	}
	if info, err := os.Stat(path); err != nil || info.Size() > 1024 {
		t.Errorf("new log file not fresh: %v, %v", info, err)
	}
}

func TestUnwritableFileFallsBackToTerminal(t *testing.T) {
	dir := t.TempDir()
	blocker := filepath.Join(dir, "file")
	if err := os.WriteFile(blocker, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	l, _, _ := New(&stderr, slog.LevelWarn, filepath.Join(blocker, "loadout.log")) // parent is a file
	l.Warn("still works")
	if !strings.Contains(stderr.String(), "terminal only") || !strings.Contains(stderr.String(), "still works") {
		t.Errorf("stderr = %q, want the fallback notice and the warning", stderr.String())
	}
}
