package deploy_test

import (
	"io/fs"
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/petervlad/loadout/deploy"
	"github.com/petervlad/loadout/internal/config"
)

// FS holds every deploy file and nothing else: a file added to this folder
// must be embedded (or named here as not meant to be), so the skills that
// ship these files never miss one.
func TestFSHoldsTheDeployFiles(t *testing.T) {
	notShipped := []string{"README.md", "files.go", "deploy_test.go", "downloads_test.go", "install_test.go", "files_test.go", "hostfacts_test.go", "site", "container"}
	embedded, err := fs.Glob(deploy.FS, "*")
	if err != nil {
		t.Fatal(err)
	}
	inContainer, err := fs.Glob(deploy.FS, "container/*")
	if err != nil {
		t.Fatal(err)
	}
	embedded = append(embedded, inContainer...)

	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if slices.Contains(notShipped, e.Name()) {
			continue
		}
		if !slices.Contains(embedded, e.Name()) {
			t.Errorf("%s is in deploy/ but not in deploy.FS: embed it in files.go, or name it as not shipped in this test", e.Name())
		}
	}
	for _, name := range embedded {
		data, err := fs.ReadFile(deploy.FS, name)
		if err != nil && !strings.HasPrefix(err.Error(), "read "+name) { // a folder
			t.Errorf("read %s: %v", name, err)
		}
		if err == nil && len(data) == 0 {
			t.Errorf("%s is empty", name)
		}
		if err == nil {
			onDisk, err := os.ReadFile(name)
			if err != nil || string(onDisk) != string(data) {
				t.Errorf("%s in deploy.FS differs from the file on disk", name)
			}
		}
	}
}

var exampleLine = regexp.MustCompile(`^#\s?(LOADOUT_[A-Z0-9_]+)=`)

// loadout.env.example documents every variable a registry host reads, and
// only variables loadout knows, so a rename cannot leave a stale line.
func TestEnvExampleMatchesWhatLoadoutReads(t *testing.T) {
	data, err := fs.ReadFile(deploy.FS, "loadout.env.example")
	if err != nil {
		t.Fatal(err)
	}
	listed := map[string]string{}
	var uncommented []string
	for line := range strings.SplitSeq(string(data), "\n") {
		if m := exampleLine.FindStringSubmatch(line); m != nil {
			listed[m[1]] = line
			uncommented = append(uncommented, strings.TrimPrefix(strings.TrimPrefix(line, "#"), " "))
		} else if strings.HasPrefix(line, "LOADOUT_") {
			t.Errorf("an active setting in the template: %q (every line must be commented out)", line)
		}
	}
	// Every variable in the file is one loadout reads, and the file parses
	// when everything is turned on.
	env, err := config.ParseEnvFile(strings.NewReader(strings.Join(uncommented, "\n")))
	if err != nil {
		t.Fatalf("the template does not parse with every line on: %v", err)
	}
	if unknown := config.Unknown(env); len(unknown) > 0 {
		t.Errorf("the template names variables loadout does not read: %v", unknown)
	}
	// Not for a registry host's file: the CLI's own, set by the unit or by
	// tests and previews.
	notForTheFile := []string{
		"LOADOUT_STATE_DIR", "LOADOUT_LOG", "LOADOUT_PUSHOVER_TOKEN", "LOADOUT_PUSHOVER_USER", "LOADOUT_UPDATE_NOTICE",
		"LOADOUT_TOKEN", "LOADOUT_DATA", "LOADOUT_BACKUP_DIR", "LOADOUT_BACKUP_KEEP_DAYS", "LOADOUT_HETZNER_API",
	}
	for _, k := range config.Known() {
		if _, ok := listed[k]; !ok && !slices.Contains(notForTheFile, k) {
			t.Errorf("%s is read by loadout but missing from loadout.env.example", k)
		}
	}
	// SMTP first, as the people who set up mail are the most.
	if i, j := strings.Index(string(data), "LOADOUT_SMTP_HOST"), strings.Index(string(data), "LOADOUT_GOOGLE_CLIENT_ID"); i < 0 || j < i {
		t.Error("the SMTP lines should come before the Google ones")
	}
}
