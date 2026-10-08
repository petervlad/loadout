package cli

import (
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/petervlad/loadout/internal/install"
	"github.com/petervlad/loadout/internal/pack"
)

func TestStatusLines(t *testing.T) {
	const unit = "/h/.claude/skills/kb"
	file := func(target, path string, state install.State) install.FileStatus {
		return install.FileStatus{Target: target, Path: unit + "/" + path, Unit: unit, State: state}
	}
	agent := install.FileStatus{Target: "claude", Path: "/h/.claude/agents/planner.md", State: install.StateOK}
	base, err := pack.ParseID("base/kb")
	if err != nil {
		t.Fatal(err)
	}
	shadowed := func(path string) install.FileStatus {
		f := file("claude", path, install.StateShadowed)
		f.ShadowedBy = base
		return f
	}

	tests := []struct {
		name  string
		files []install.FileStatus
		every bool
		want  []statusLine
	}{
		{
			name:  "agent file",
			files: []install.FileStatus{agent},
			want:  []statusLine{{target: "claude", state: "ok", path: agent.Path}},
		},
		{
			name:  "intact folder",
			files: []install.FileStatus{file("claude", "SKILL.md", install.StateOK), file("claude", "a.md", install.StateOK)},
			want:  []statusLine{{target: "claude", state: "ok", path: unit}},
		},
		{
			name: "problems counted, worst first",
			files: []install.FileStatus{
				file("claude", "SKILL.md", install.StateModified),
				file("claude", "a.md", install.StateMissing),
				file("claude", "b.md", install.StateModified),
				file("claude", "c.md", install.StateOK),
			},
			want: []statusLine{{target: "claude", state: "missing (1 of 4 files), modified (2 of 4 files)", path: unit}},
		},
		{
			name:  "one line per tool",
			files: []install.FileStatus{file("claude", "SKILL.md", install.StateOK), file("copilot", "SKILL.md", install.StateMissing)},
			want: []statusLine{
				{target: "claude", state: "ok", path: unit},
				{target: "copilot", state: "missing (1 of 1 files)", path: unit},
			},
		},
		{
			name:  "shadowed folder",
			files: []install.FileStatus{shadowed("SKILL.md"), shadowed("a.md")},
			want:  []statusLine{{target: "claude", state: "shadowed by base/kb", path: unit}},
		},
		{
			name:  "every file",
			files: []install.FileStatus{file("claude", "SKILL.md", install.StateOK), shadowed("a.md")},
			every: true,
			want: []statusLine{
				{target: "claude", state: "ok", path: unit + "/SKILL.md"},
				{target: "claude", state: "shadowed by base/kb", path: unit + "/a.md"},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := statusLines(tt.files, tt.every)
			for i := range got {
				got[i].total, got[i].counts, got[i].by = 0, nil, ""
			}
			if diff := cmp.Diff(tt.want, got, cmp.AllowUnexported(statusLine{})); diff != "" {
				t.Errorf("statusLines() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}
