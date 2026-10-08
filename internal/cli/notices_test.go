package cli

import (
	"context"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/petervlad/loadout/internal/notice"
	"github.com/petervlad/loadout/internal/pack"
	"github.com/petervlad/loadout/internal/semver"
)

func TestWriteNotices(t *testing.T) {
	planner, err := pack.ParseID("payments/planner")
	if err != nil {
		t.Fatal(err)
	}
	runbooks, err := pack.ParseID("payments/runbooks")
	if err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 10, 4, 9, 0, 0, 0, time.Local)
	ns := []notice.Notice{
		{Kind: notice.Held, ID: planner, Scope: userScope, Latest: semver.New(1, 3, 0), Edits: true, Files: []string{"planner.md (claude)"}, Checked: at.Add(-time.Hour)},
		{Kind: notice.Available, ID: runbooks, Scope: projectScope, Latest: semver.New(2, 1, 0), Checked: at},
	}
	var out strings.Builder
	if err := writeNotices(&out, ns); err != nil {
		t.Fatal(err)
	}
	want := "\nAs of the last sync (4 Oct 09:00):\n" +
		"  payments/planner                          1.3.0 held back: you edited planner.md (claude)\n" +
		"  payments/runbooks (project /src/billing)  2.1.0 available: loadout install payments/runbooks --scope project\n"
	if out.String() != want {
		t.Errorf("writeNotices() =\n%q\nwant\n%q", out.String(), want)
	}
	out.Reset()
	if err := writeNotices(&out, nil); err != nil || out.Len() != 0 {
		t.Errorf("writeNotices(nothing) = %q, %v; want nothing", out.String(), err)
	}
}

// blockingReader never returns, like a tool that keeps stdin open.
type blockingReader struct{ done <-chan struct{} }

func (r blockingReader) Read([]byte) (int, error) { <-r.done; return 0, io.EOF }

func TestReadHookInput(t *testing.T) {
	in, err := readHookInput(t.Context(), strings.NewReader(`{"cwd":"/src/billing","source":"startup","session_id":"x"}`))
	if err != nil || in.Cwd != "/src/billing" || in.Source != "startup" {
		t.Errorf("readHookInput() = %+v, %v", in, err)
	}
	if in, err := readHookInput(t.Context(), strings.NewReader("")); err != nil || in != (hookInput{}) {
		t.Errorf("empty input = %+v, %v; want the zero input", in, err)
	}
	done := make(chan struct{})
	defer close(done)
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()
	if _, err := readHookInput(ctx, blockingReader{done}); err == nil {
		t.Error("readHookInput waited for a stdin that never closes")
	}
}
