package pack

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/petervlad/loadout/internal/skill"
)

func pipelinePack(t *testing.T, id, version, body string) *Pack {
	t.Helper()
	p, err := fromStrings(t, map[string]string{
		ManifestFile: "name: " + id + "\nkind: pipeline\nversion: " + version + "\ndescription: A pipeline.\n",
		PipelineFile: body,
	})
	if err != nil {
		t.Fatalf("FromFiles() error: %v", err)
	}
	return p
}

func pipelineTestdata(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "pipeline", "testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestPipelineTemplate(t *testing.T) {
	p := pipelinePack(t, "acme/dev-loop", "1.2.0", pipelineTestdata(t, "dev-loop.yaml"))
	if p.Kind() != KindPipeline {
		t.Errorf("Kind() = %q", p.Kind())
	}
	if _, ok := p.Pipeline(); !ok {
		t.Error("Pipeline() = _, false")
	}
	if diff := cmp.Diff(Needs{}, p.Needs()); diff != "" {
		t.Errorf("Needs() of a template mismatch (-want +got):\n%s", diff)
	}
	if ok, why := p.Standalone(); ok || !strings.Contains(why, "installs through a team's binding") {
		t.Errorf("Standalone() = %v, %q; want false and a reason", ok, why)
	}
	if main, _, err := p.Main(); err != nil || main != PipelineFile {
		t.Errorf("Main() = %q, %v", main, err)
	}
}

func TestPipelineBinding(t *testing.T) {
	p := pipelinePack(t, "lime/dev-loop", "1.0.0", pipelineTestdata(t, "lime-binding.yaml"))
	id, _ := NewID("acme", "dev-loop")
	want := Needs{
		Packages: []Requirement{{ID: id, Major: 1}},
		Agents:   []string{"lime-code-review", "lime-dev", "lime-planner", "lime-test"},
	}
	if diff := cmp.Diff(want, p.Needs(), cmp.AllowUnexported(ID{})); diff != "" {
		t.Errorf("Needs() mismatch (-want +got):\n%s", diff)
	}
	if got := p.Needs().Packages[0].String(); got != "acme/dev-loop@1" {
		t.Errorf("Requirement.String() = %q", got)
	}
	if ok, why := p.Standalone(); !ok || why != "" {
		t.Errorf("Standalone() = %v, %q; want true", ok, why)
	}
}

func TestNeedsAndStandaloneOfOtherKinds(t *testing.T) {
	p, err := fromStrings(t, map[string]string{ManifestFile: skillManifest, skill.File: skillMain})
	if err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff(Needs{}, p.Needs()); diff != "" {
		t.Errorf("Needs() mismatch (-want +got):\n%s", diff)
	}
	if ok, _ := p.Standalone(); !ok {
		t.Error("Standalone() = false for a skill")
	}
}

func TestPipelineFiles(t *testing.T) {
	body := pipelineTestdata(t, "dev-loop.yaml")
	tests := []struct {
		name    string
		files   map[string]string
		wantErr string
	}{
		{"extra file", map[string]string{PipelineFile: body, "notes.md": "x"}, `unexpected file "notes.md"`},
		{"missing pipeline.yaml", map[string]string{}, "missing pipeline.yaml"},
		{"invalid pipeline", map[string]string{PipelineFile: "slots: {}\n"}, "pipeline.yaml:"},
		{"too big", map[string]string{PipelineFile: body + "# " + strings.Repeat("x", 70<<10) + "\n"}, "too large"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tc.files[ManifestFile] = "name: acme/dev-loop\nkind: pipeline\nversion: 1.0.0\ndescription: A pipeline.\n"
			_, err := fromStrings(t, tc.files)
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("FromFiles() error = %v, want %q", err, tc.wantErr)
			}
		})
	}
}

func TestCompatible(t *testing.T) {
	body := pipelineTestdata(t, "dev-loop.yaml")
	prev := pipelinePack(t, "acme/dev-loop", "1.0.0", body)
	tests := []struct {
		name    string
		next    *Pack
		wantErr string
	}{
		{"brief changed", pipelinePack(t, "acme/dev-loop", "1.1.0", strings.Replace(body, "Implement the requirement on a branch.", "Build it.", 1)), ""},
		{"verdict removed", pipelinePack(t, "acme/dev-loop", "1.1.0", strings.Replace(strings.Replace(body, ", BLOCKED]", "]", 1), "  - {from: review, on: BLOCKED,           to: end,    via: gate}\n", "", 1)), "BLOCKED"},
		{"binding instead of template", pipelinePack(t, "acme/dev-loop", "1.1.0", pipelineTestdata(t, "lime-binding.yaml")), "template and a binding"},
		{"another kind", func() *Pack {
			p, err := fromStrings(t, map[string]string{ManifestFile: strings.Replace(skillManifest, "lime/release-notes", "acme/dev-loop", 1), skill.File: strings.Replace(skillMain, "release-notes", "dev-loop", 1)})
			if err != nil {
				t.Fatal(err)
			}
			return p
		}(), "changes kind"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := Compatible(prev, tc.next)
			switch {
			case tc.wantErr == "" && err != nil:
				t.Errorf("Compatible() error = %v, want nil", err)
			case tc.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tc.wantErr)):
				t.Errorf("Compatible() error = %v, want %q", err, tc.wantErr)
			}
		})
	}
}

func TestCompatibleOtherKindsAcceptAnyChange(t *testing.T) {
	a, _ := fromStrings(t, map[string]string{ManifestFile: skillManifest, skill.File: skillMain})
	b, _ := fromStrings(t, map[string]string{ManifestFile: skillManifest, skill.File: strings.Replace(skillMain, "Collect", "Gather", 1)})
	if err := Compatible(a, b); err != nil {
		t.Errorf("Compatible() of two skills = %v, want nil", err)
	}
}
