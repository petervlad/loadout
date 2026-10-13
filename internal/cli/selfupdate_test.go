package cli

import (
	"bytes"
	"context"
	"io"
	"path/filepath"
	"strings"
	"testing"

	"github.com/petervlad/loadout/internal/buildinfo"
	"github.com/petervlad/loadout/internal/client"
	"github.com/petervlad/loadout/internal/cliupdate"
	"github.com/petervlad/loadout/internal/remote"
)

func TestUpdateCommand(t *testing.T) {
	const registryURL = "https://203.0.113.7"
	registry := buildinfo.Method{Name: "registry", Command: "curl -fsSL " + buildinfo.RegistryPlaceholder + "/install.sh | sh"}
	source := buildinfo.Method{Name: "source", Command: "git pull && go install ./cmd/loadout", InFolder: true}
	// The public ways to install loadout, as the method table picks them.
	release := buildinfo.Current("v0.4.0", nil, false)
	homebrew := buildinfo.MethodOf(buildinfo.Install{Build: release, Executable: "/opt/homebrew/Caskroom/loadout/0.4.0/loadout"})
	download := buildinfo.MethodOf(buildinfo.Install{Build: release, Executable: "/home/ada/.local/bin/loadout", Packaged: true})

	tests := []struct {
		name    string
		method  buildinfo.Method
		build   string
		latest  string
		args    []string
		wantRun string
		wantOut string
		wantErr string
	}{
		{
			name: "registry install runs install.sh", method: registry, build: "v0.4.0", latest: "0.5.0",
			wantRun: "curl -fsSL https://203.0.113.7/install.sh | sh", wantOut: "Running: curl",
		},
		{
			name: "check runs nothing", method: registry, build: "v0.4.0", latest: "0.5.0", args: []string{"--check"},
			wantOut: "This would run: curl -fsSL https://203.0.113.7/install.sh | sh",
		},
		{
			name: "already newest", method: registry, build: "v0.5.0", latest: "0.5.0",
			wantOut: "anything newer than your build",
		},
		{
			name: "force runs anyway", method: registry, build: "v0.5.0", latest: "0.5.0", args: []string{"--force"},
			wantRun: "curl -fsSL https://203.0.113.7/install.sh | sh",
		},
		{
			name: "homebrew runs brew upgrade", method: homebrew, build: "v0.4.0", latest: "0.5.0",
			wantRun: "brew upgrade --cask loadout", wantOut: "Running: brew upgrade --cask loadout",
		},
		{
			name: "a release download only says where to get the new one", method: download, build: "v0.4.0", latest: "0.5.0",
			wantOut: "To update: download the new release",
		},
		{
			name: "source build only says how", method: source, build: "v0.4.0", latest: "0.5.0",
			wantOut: "To update, in your loadout folder: git pull && go install ./cmd/loadout",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			svc := cliupdate.New(cliupdate.Options{
				Store:  cliupdate.NewStore(filepath.Join(t.TempDir(), "cliupdate.yaml")),
				Build:  buildinfo.Current(tc.build, nil, false),
				Method: tc.method,
			})
			if tc.latest != "" {
				svc.Saw(registryURL, client.CLIAdvice{Latest: tc.latest})
				if _, err := svc.Finish(context.Background(), cliupdate.Script); err != nil {
					t.Fatal(err)
				}
			}
			var ran string
			d := Deps{Updates: svc, RunShell: func(_ context.Context, c string, _, _ io.Writer) error { ran = c; return nil }}
			var out bytes.Buffer
			cmd := newUpdateCmd(d)
			cmd.SetOut(&out)
			cmd.SetErr(&out)
			cmd.SetArgs(tc.args)
			err := cmd.Execute()
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("err = %v, want %q", err, tc.wantErr)
				}
			} else if err != nil {
				t.Fatal(err)
			}
			if ran != tc.wantRun {
				t.Errorf("ran %q, want %q", ran, tc.wantRun)
			}
			if !strings.Contains(out.String(), tc.wantOut) {
				t.Errorf("output %q does not contain %q", out.String(), tc.wantOut)
			}
		})
	}
}

func TestUpdateWithoutKnownRegistryRefuses(t *testing.T) {
	svc := cliupdate.New(cliupdate.Options{
		Store:  cliupdate.NewStore(filepath.Join(t.TempDir(), "cliupdate.yaml")),
		Build:  buildinfo.Current("v0.4.0", nil, false),
		Method: buildinfo.Method{Name: "registry", Command: "curl -fsSL " + buildinfo.RegistryPlaceholder + "/install.sh | sh"},
	})
	cmd := newUpdateCmd(Deps{Updates: svc, RunShell: func(context.Context, string, io.Writer, io.Writer) error {
		t.Error("ran without a registry")
		return nil
	}})
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SetArgs(nil)
	if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), "does not know which registry") {
		t.Fatalf("err = %v", err)
	}
}

// TestUpdateUsesTheConfiguredRegistry: right after install there is no
// news saved, and the registry the person signed in to is the source.
func TestUpdateUsesTheConfiguredRegistry(t *testing.T) {
	dir := t.TempDir()
	remotes := remote.NewStore(dir, func(string) string { return "" })
	cfg := &remote.Config{}
	if err := cfg.Add(remote.Remote{Name: "office", URL: "https://203.0.113.7"}); err != nil {
		t.Fatal(err)
	}
	if err := remotes.Save(cfg); err != nil {
		t.Fatal(err)
	}
	svc := cliupdate.New(cliupdate.Options{
		Store:  cliupdate.NewStore(filepath.Join(dir, "cliupdate.yaml")),
		Build:  buildinfo.Current("v0.4.0", nil, false),
		Method: buildinfo.Method{Name: "registry", Command: "curl -fsSL " + buildinfo.RegistryPlaceholder + "/install.sh | sh"},
	})
	var ran string
	cmd := newUpdateCmd(Deps{Updates: svc, Remotes: remotes, RunShell: func(_ context.Context, c string, _, _ io.Writer) error { ran = c; return nil }})
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SetArgs(nil)
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if want := "curl -fsSL https://203.0.113.7/install.sh | sh"; ran != want {
		t.Errorf("ran %q, want %q", ran, want)
	}
}
