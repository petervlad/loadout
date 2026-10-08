package rebase_test

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/petervlad/loadout/internal/api"
	"github.com/petervlad/loadout/internal/pack"
	"github.com/petervlad/loadout/internal/rebase"
)

const remoteURL = "https://registry.example"

var runbooks = mustID("lime/runbooks")

func mustID(s string) pack.ID {
	id, err := pack.ParseID(s)
	if err != nil {
		panic(err)
	}
	return id
}

// kbFiles is a knowledge base lime/runbooks at version with files.
func kbFiles(version string, files map[string]string) map[string][]byte {
	out := map[string][]byte{
		pack.ManifestFile: []byte("name: lime/runbooks\nkind: kb\nversion: " + version + "\ndescription: d\n"),
	}
	if len(files) == 0 {
		files = map[string]string{"README.md": "# runbooks\n"}
	}
	for k, v := range files {
		out[k] = []byte(v)
	}
	return out
}

func kbPack(t *testing.T, version string, files map[string]string) *pack.Pack {
	t.Helper()
	p, err := pack.FromFiles(kbFiles(version, files))
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// fakeRegistry serves releases and proposals from memory.
type fakeRegistry struct {
	releases  []*pack.Pack // lowest first
	by        map[string]string
	proposals []api.Proposal
	bundles   map[string]*pack.Pack // by proposal id
}

func (f *fakeRegistry) Releases(_ context.Context, id pack.ID) ([]api.Release, error) {
	var out []api.Release
	for _, p := range f.releases {
		if p.ID() == id {
			r := api.Release{ID: id.String(), Version: p.Version().String()}
			if by, ok := f.by[r.Version]; ok {
				r.PublishedBy = &api.Actor{TokenID: "t", Label: by}
			}
			out = append(out, r)
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("releases: %w", api.ErrNotFound)
	}
	return out, nil
}

func (f *fakeRegistry) Fetch(_ context.Context, id pack.ID, version string) (*pack.Pack, error) {
	for _, p := range f.releases {
		if p.ID() == id && p.Version().String() == version {
			return p, nil
		}
	}
	return nil, fmt.Errorf("fetch %s@%s: %w", id, version, api.ErrNotFound)
}

func (f *fakeRegistry) Proposals(_ context.Context, pkg, status string) ([]api.Proposal, error) {
	var out []api.Proposal
	for _, p := range f.proposals {
		if p.Package == pkg && p.Status == status {
			out = append(out, p)
		}
	}
	return out, nil
}

func (f *fakeRegistry) Proposal(_ context.Context, id string) (api.Proposal, error) {
	for _, p := range f.proposals {
		if p.ID == id {
			return p, nil
		}
	}
	return api.Proposal{}, fmt.Errorf("proposal: %w", api.ErrNotFound)
}

func (f *fakeRegistry) FetchProposal(_ context.Context, p api.Proposal) (*pack.Pack, error) {
	return f.bundles[p.ID], nil
}

var at = time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)

func newService(t *testing.T, reg rebase.Registry) (*rebase.Service, *rebase.Records) {
	t.Helper()
	records := rebase.NewRecords(t.TempDir())
	return rebase.New(reg, records, remoteURL, func() time.Time { return at }), records
}

var yes, no = true, false

func TestLocate(t *testing.T) {
	reg := &fakeRegistry{
		releases: []*pack.Pack{kbPack(t, "1.0.0", nil), kbPack(t, "2.0.0", nil), kbPack(t, "3.0.0", nil)},
		by:       map[string]string{"3.0.0": "dana@example.com via loadout login"},
		proposals: []api.Proposal{
			{ID: "aaaaaaaaaaaa", Package: "lime/runbooks", Version: "3.1.0", Status: api.StatusOpen, Base: "2.0.0", Latest: "3.0.0", Yours: &yes},
			{ID: "bbbbbbbbbbbb", Package: "lime/runbooks", Version: "4.0.0", Status: api.StatusOpen},
			{ID: "cccccccccccc", Package: "lime/runbooks", Version: "5.0.0", Status: api.StatusWithdrawn, Base: "1.0.0", Latest: "3.0.0"},
			{ID: "dddddddddddd", Package: "lime/runbooks", Version: "3.2.0", Status: api.StatusOpen, Base: "1.0.0", Latest: "3.0.0", Yours: &no},
		},
	}
	tests := []struct {
		name         string
		version      string
		opts         rebase.Options
		record       *rebase.Record
		wantBase     string
		wantReplaces string
		wantFrom     rebase.Source
		wantErr      string
	}{
		{"flag latest", "1.0.0", rebase.Options{Base: "latest"}, nil, "3.0.0", "", rebase.SourceFlag, ""},
		{"flag version", "1.0.0", rebase.Options{Base: "2.0.0"}, nil, "2.0.0", "", rebase.SourceFlag, ""},
		{"flag not a release", "1.0.0", rebase.Options{Base: "2.5.0"}, nil, "", "", 0, "--base 2.5.0 is not a release of lime/runbooks; its latest releases are 1.0.0, 2.0.0, 3.0.0"},
		{"record", "3.1.0", rebase.Options{}, &rebase.Record{Remote: remoteURL, Package: "lime/runbooks", Base: "2.0.0", Onto: "3.0.0", Replaces: "aaaaaaaaaaaa"}, "3.0.0", "aaaaaaaaaaaa", rebase.SourceRecord, ""},
		{"record of another registry is ignored", "1.0.0", rebase.Options{}, &rebase.Record{Remote: "https://other", Package: "lime/runbooks", Onto: "3.0.0"}, "1.0.0", "", rebase.SourceRelease, ""},
		{"open proposal at the folder's version", "3.1.0", rebase.Options{}, nil, "2.0.0", "aaaaaaaaaaaa", rebase.SourceProposal, ""},
		{"open proposal from an older registry, whose author is not known: by version", "4.0.0", rebase.Options{}, nil, "3.0.0", "", rebase.SourceGuess, ""},
		{"someone else's open proposal at the folder's version: by version", "3.2.0", rebase.Options{}, nil, "3.0.0", "", rebase.SourceGuess, ""},
		{"someone else's proposal named: its base, not replaced", "9.0.0", rebase.Options{Proposal: "dddddddddddd"}, nil, "1.0.0", "", rebase.SourceProposal, ""},
		{"named proposal", "9.0.0", rebase.Options{Proposal: "aaaaaaaaaaaa"}, nil, "2.0.0", "aaaaaaaaaaaa", rebase.SourceProposal, ""},
		{"named proposal that is closed", "9.0.0", rebase.Options{Proposal: "cccccccccccc"}, nil, "", "", 0, "proposal cccccccccccc is withdrawn"},
		{"flag wins, proposal still replaced", "3.1.0", rebase.Options{Base: "latest"}, nil, "3.0.0", "aaaaaaaaaaaa", rebase.SourceFlag, ""},
		{"release at the folder's version", "2.0.0", rebase.Options{}, nil, "2.0.0", "", rebase.SourceRelease, ""},
		{"guess below", "2.5.0", rebase.Options{}, nil, "2.0.0", "", rebase.SourceGuess, ""},
		{"nothing below", "0.1.0", rebase.Options{}, nil, "", "", rebase.SourceNone, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			svc, records := newService(t, reg)
			dir := filepath.Join(t.TempDir(), "runbooks")
			if tc.record != nil {
				if err := records.Put(dir, *tc.record); err != nil {
					t.Fatal(err)
				}
			}
			o, err := svc.Locate(t.Context(), dir, kbPack(t, tc.version, nil), tc.opts)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("Locate() error = %v, want %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if o.Base != tc.wantBase || o.Replaces != tc.wantReplaces || o.From != tc.wantFrom || o.Latest != "3.0.0" || o.LatestBy != "dana@example.com" {
				t.Errorf("Locate() = %+v, want base %q, replaces %q, from %d, latest 3.0.0 by dana", o, tc.wantBase, tc.wantReplaces, tc.wantFrom)
			}
		})
	}
}

func TestLocateWithoutRelease(t *testing.T) {
	svc, _ := newService(t, &fakeRegistry{})
	o, err := svc.Locate(t.Context(), "", kbPack(t, "0.1.0", nil), rebase.Options{})
	if err != nil || o.Behind() || o.Base != "" || o.Latest != "" {
		t.Errorf("Locate() of a first version = %+v, %v; want nothing behind", o, err)
	}
	if _, err := svc.Locate(t.Context(), "", nil, rebase.Options{}); err == nil || !strings.Contains(err.Error(), "--proposal ID") {
		t.Errorf("Locate() without a folder or proposal error = %v", err)
	}
}

func TestRefusal(t *testing.T) {
	o := rebase.Origin{Package: runbooks, Base: "2.0.0", Latest: "3.0.0", LatestBy: "Dana"}
	propose := o.Refusal("propose", "runbooks").Error()
	publish := o.Refusal("publish", "runbooks").Error()
	want := "runbooks is based on 2.0.0, and 3.0.0 by Dana was released since; its changes are not in your folder. Bring them in with: loadout rebase runbooks. If your folder already has them, propose with --base 3.0.0"
	if propose != want {
		t.Errorf("Refusal(propose) = %q\nwant %q", propose, want)
	}
	if strings.Replace(publish, "publish with", "propose with", 1) != propose {
		t.Errorf("Refusal(publish) = %q differs from propose's beyond the verb", publish)
	}
	if !errors.Is(o.Refusal("publish", ""), rebase.ErrBehind) {
		t.Error("Refusal does not match ErrBehind")
	}
	first := rebase.Origin{Package: runbooks, Latest: "0.1.0"}.Refusal("publish", "").Error()
	if !strings.HasPrefix(first, "runbooks was made before the first release, and 0.1.0 was released since") || !strings.Contains(first, "loadout rebase.") {
		t.Errorf("Refusal of a folder made before the first release = %q", first)
	}
}

// writeFolder writes files as a package folder and loads it.
func writeFolder(t *testing.T, dir string, files map[string][]byte) *pack.Pack {
	t.Helper()
	for name, data := range files {
		p := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, data, 0o644); err != nil { //nolint:gosec // a test folder
			t.Fatal(err)
		}
	}
	p, err := pack.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

const guide = "line1\nline2\nline3\nline4\nline5\n"

// baseFiles is release 1.0.0; theirs is release 1.1.0 after it.
var (
	baseFiles  = map[string]string{"guide.md": guide, "a.md": "a\n", "b.md": "b\n", "c.md": "c\n"}
	theirFiles = map[string]string{"guide.md": "line1\nline2\nline3\nline4\nLINE5\n", "b.md": "b2\n", "c.md": "c\n", "new.md": "n\n"}
)

func TestRebase(t *testing.T) {
	tests := []struct {
		name        string
		version     string
		ours        map[string]string
		wantFiles   map[string]string
		wantChanges map[string]rebase.Change
		wantVersion string
		wantBumped  bool
	}{
		{
			name: "changes on both sides merge", version: "1.2.0",
			ours:        map[string]string{"guide.md": "LINE1\nline2\nline3\nline4\nline5\n", "a.md": "a\n", "b.md": "b\n", "c.md": "c2\n", "mine.md": "m\n"},
			wantFiles:   map[string]string{"guide.md": "LINE1\nline2\nline3\nline4\nLINE5\n", "b.md": "b2\n", "c.md": "c2\n", "new.md": "n\n", "mine.md": "m\n"},
			wantChanges: map[string]rebase.Change{"guide.md": rebase.Merged, "a.md": rebase.Deleted, "b.md": rebase.FromRelease, "c.md": rebase.Yours, "new.md": rebase.FromRelease, "mine.md": rebase.Yours},
			wantVersion: "1.2.0",
		},
		{
			name: "a file you deleted stays deleted", version: "1.2.0",
			ours:        map[string]string{"guide.md": guide, "a.md": "a\n", "b.md": "b\n"},
			wantFiles:   map[string]string{"guide.md": "line1\nline2\nline3\nline4\nLINE5\n", "b.md": "b2\n", "new.md": "n\n"},
			wantChanges: map[string]rebase.Change{"guide.md": rebase.FromRelease, "a.md": rebase.Deleted, "b.md": rebase.FromRelease, "c.md": rebase.YouDeleted, "new.md": rebase.FromRelease},
			wantVersion: "1.2.0",
		},
		{
			name: "a folder not above the release gets its next patch", version: "1.0.0",
			ours:        baseFiles,
			wantFiles:   theirFiles,
			wantChanges: map[string]rebase.Change{"guide.md": rebase.FromRelease, "a.md": rebase.Deleted, "b.md": rebase.FromRelease, "new.md": rebase.FromRelease},
			wantVersion: "1.1.1", wantBumped: true,
		},
		{
			name: "the same change on both sides applies once", version: "1.2.0",
			ours:        map[string]string{"guide.md": "line1\nline2\nline3\nline4\nLINE5\n", "b.md": "b2\n", "c.md": "c\n", "new.md": "n\n"},
			wantFiles:   theirFiles,
			wantChanges: map[string]rebase.Change{},
			wantVersion: "1.2.0",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			reg := &fakeRegistry{releases: []*pack.Pack{kbPack(t, "1.0.0", baseFiles), kbPack(t, "1.1.0", theirFiles)}}
			svc, records := newService(t, reg)
			dir := filepath.Join(t.TempDir(), "runbooks")
			p := writeFolder(t, dir, kbFiles(tc.version, tc.ours))
			hidden := filepath.Join(dir, ".notes")
			if err := os.WriteFile(hidden, []byte("mine"), 0o600); err != nil {
				t.Fatal(err)
			}
			o := rebase.Origin{Package: runbooks, Base: "1.0.0", Latest: "1.1.0"}
			res, err := svc.Rebase(t.Context(), dir, p, o)
			if err != nil {
				t.Fatalf("Rebase() error: %v", err)
			}
			if res.Version.String() != tc.wantVersion || res.Bumped != tc.wantBumped || len(res.Conflicts) != 0 {
				t.Errorf("Rebase() = version %s, bumped %v, conflicts %v; want %s, %v, none", res.Version, res.Bumped, res.Conflicts, tc.wantVersion, tc.wantBumped)
			}
			got := map[string]rebase.Change{}
			for _, f := range res.Files {
				got[f.Path] = f.Change
			}
			if !maps.Equal(got, tc.wantChanges) {
				t.Errorf("Rebase() files = %v, want %v", got, tc.wantChanges)
			}
			after, err := pack.Load(dir)
			if err != nil {
				t.Fatalf("the rebased folder does not load: %v", err)
			}
			want := kbFiles(tc.wantVersion, tc.wantFiles)
			if gotFiles := after.Files(); !maps.EqualFunc(gotFiles, want, func(a, b []byte) bool { return string(a) == string(b) }) {
				t.Errorf("folder after rebase = %q\nwant %q", gotFiles, want)
			}
			if data, err := os.ReadFile(hidden); err != nil || string(data) != "mine" {
				t.Errorf("the hidden file was touched: %q, %v", data, err)
			}
			rec, ok, err := records.Get(dir)
			if err != nil || !ok || rec.Onto != "1.1.0" || rec.Base != "1.0.0" || rec.Remote != remoteURL {
				t.Errorf("record = %+v, %v, %v", rec, ok, err)
			}
			// Proposing next reads the record: no longer behind.
			again, err := svc.Locate(t.Context(), dir, after, rebase.Options{})
			if err != nil || again.Behind() || again.From != rebase.SourceRecord {
				t.Errorf("Locate() after rebase = %+v, %v; want up to date from the record", again, err)
			}
		})
	}
}

func TestRebaseConflicts(t *testing.T) {
	reg := &fakeRegistry{releases: []*pack.Pack{kbPack(t, "1.0.0", baseFiles), kbPack(t, "1.1.0", theirFiles)}}
	svc, _ := newService(t, reg)
	dir := filepath.Join(t.TempDir(), "runbooks")
	ours := map[string]string{"guide.md": "line1\nline2\nline3\nline4\nfive\n", "a.md": "a\n", "c.md": "c\n"} // b.md deleted
	p := writeFolder(t, dir, kbFiles("1.2.0", ours))
	o := rebase.Origin{Package: runbooks, Base: "1.0.0", Latest: "1.1.0", Replaces: "aaaaaaaaaaaa"}
	res, err := svc.Rebase(t.Context(), dir, p, o)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(res.Conflicts, []string{"b.md", "guide.md"}) {
		t.Fatalf("Rebase() conflicts = %v, want b.md and guide.md", res.Conflicts)
	}
	data, err := os.ReadFile(filepath.Join(dir, "guide.md"))
	if err != nil {
		t.Fatal(err)
	}
	want := "line1\nline2\nline3\nline4\n<<<<<<< yours (proposal aaaaaaaaaaaa)\nfive\n||||||| base 1.0.0\nline5\n=======\nLINE5\n>>>>>>> release 1.1.0\n"
	if string(data) != want {
		t.Errorf("guide.md = %q\nwant %q", data, want)
	}
	if data, err := os.ReadFile(filepath.Join(dir, "b.md")); err != nil || string(data) != "b2\n" {
		t.Errorf("b.md, deleted by you and changed in the release = %q, %v; want the release's", data, err)
	}
	if !slices.Equal(res.Undecided, []string{"b.md"}) {
		t.Errorf("Rebase() undecided = %v, want b.md", res.Undecided)
	}
	pending := func(want rebase.Pending) {
		t.Helper()
		got, err := svc.Unresolved(dir)
		if err != nil || !slices.Equal(got.Markers, want.Markers) || !slices.Equal(got.Undecided, want.Undecided) || got.None() != want.None() {
			t.Errorf("Unresolved() = %+v, %v; want %+v", got, err, want)
		}
	}
	pending(rebase.Pending{Markers: []string{"guide.md"}, Undecided: []string{"b.md"}})
	if err := os.WriteFile(filepath.Join(dir, "guide.md"), []byte("line1\nline2\nline3\nline4\nfive and LINE5\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	pending(rebase.Pending{Undecided: []string{"b.md"}})
	if err := svc.MarkResolved(dir, []string{"guide.md"}); err == nil || !strings.Contains(err.Error(), "guide.md has no undecided conflict") {
		t.Errorf("MarkResolved(a file with markers) error = %v", err)
	}
	if err := svc.MarkResolved(dir, []string{"b.md"}); err != nil {
		t.Fatal(err)
	}
	pending(rebase.Pending{})
	if err := svc.Done(dir); err != nil {
		t.Fatal(err)
	}
	pending(rebase.Pending{})
}

// A file the release deleted and the author changed stays undecided until
// the author deletes it.
func TestUndecidedResolvedByDeleting(t *testing.T) {
	reg := &fakeRegistry{releases: []*pack.Pack{
		kbPack(t, "1.0.0", map[string]string{"a.md": "a\n", "old.md": "old\n"}),
		kbPack(t, "1.1.0", map[string]string{"a.md": "a\n"}),
	}}
	svc, _ := newService(t, reg)
	dir := filepath.Join(t.TempDir(), "runbooks")
	p := writeFolder(t, dir, kbFiles("1.2.0", map[string]string{"a.md": "a\n", "old.md": "old, edited\n"}))
	res, err := svc.Rebase(t.Context(), dir, p, rebase.Origin{Package: runbooks, Base: "1.0.0", Latest: "1.1.0"})
	if err != nil || !slices.Equal(res.Undecided, []string{"old.md"}) {
		t.Fatalf("Rebase() = %+v, %v; want old.md undecided", res, err)
	}
	if got, err := svc.Unresolved(dir); err != nil || !slices.Equal(got.Undecided, []string{"old.md"}) {
		t.Errorf("Unresolved() = %+v, %v; want old.md", got, err)
	}
	if err := os.Remove(filepath.Join(dir, "old.md")); err != nil {
		t.Fatal(err)
	}
	if got, err := svc.Unresolved(dir); err != nil || !got.None() {
		t.Errorf("Unresolved() after deleting old.md = %+v, %v; want none", got, err)
	}
}

func TestRebaseRefusals(t *testing.T) {
	agent := func(version string) *pack.Pack {
		p, err := pack.FromFiles(map[string][]byte{
			pack.ManifestFile: []byte("name: lime/runbooks\nkind: agent\nversion: " + version + "\ndescription: d\n"),
			pack.AgentFile:    []byte("---\nname: runbooks\ndescription: d\n---\nprompt\n"),
		})
		if err != nil {
			t.Fatal(err)
		}
		return p
	}
	reg := &fakeRegistry{releases: []*pack.Pack{kbPack(t, "1.0.0", baseFiles), agent("1.1.0")}}
	svc, _ := newService(t, reg)
	dir := filepath.Join(t.TempDir(), "runbooks")
	p := writeFolder(t, dir, kbFiles("1.2.0", baseFiles))
	_, err := svc.Rebase(t.Context(), dir, p, rebase.Origin{Package: runbooks, Base: "1.0.0", Latest: "1.1.0"})
	if err == nil || !strings.Contains(err.Error(), "the kind changed, so update the folder by hand") {
		t.Errorf("Rebase() across a kind change error = %v", err)
	}

	res, err := svc.Rebase(t.Context(), dir, p, rebase.Origin{Package: runbooks, Base: "1.0.0", Latest: "1.0.0"})
	if err != nil || !res.UpToDate {
		t.Errorf("Rebase() of a folder that is up to date = %+v, %v", res, err)
	}
}

func TestRebaseWriteFailureNamesWhatWasWritten(t *testing.T) {
	reg := &fakeRegistry{releases: []*pack.Pack{
		kbPack(t, "1.0.0", map[string]string{"a.md": "a\n", "z/b.md": "b\n"}),
		kbPack(t, "1.1.0", map[string]string{"a.md": "a2\n", "z/b.md": "b2\n"}),
	}}
	svc, _ := newService(t, reg)
	dir := filepath.Join(t.TempDir(), "runbooks")
	p := writeFolder(t, dir, kbFiles("1.2.0", map[string]string{"a.md": "a\n", "z/b.md": "b\n"}))
	// A folder nobody may write to stops the second file.
	if err := os.Chmod(filepath.Join(dir, "z"), 0o500); err != nil { //nolint:gosec // a folder must be searchable to test the write
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(filepath.Join(dir, "z"), 0o700) }) //nolint:gosec // as it was, so the temp folder can be removed
	_, err := svc.Rebase(t.Context(), dir, p, rebase.Origin{Package: runbooks, Base: "1.0.0", Latest: "1.1.0"})
	if err == nil || !strings.Contains(err.Error(), "after writing a.md") || !strings.Contains(err.Error(), "z/b.md") {
		t.Errorf("Rebase() into a read-only folder error = %v; want it to name a.md as written", err)
	}
}

func TestRebaseFromAProposal(t *testing.T) {
	prop := api.Proposal{ID: "aaaaaaaaaaaa", Package: "lime/runbooks", Version: "1.2.0", Status: api.StatusOpen, Base: "1.0.0", Latest: "1.1.0", Yours: &yes}
	reg := &fakeRegistry{
		releases:  []*pack.Pack{kbPack(t, "1.0.0", baseFiles), kbPack(t, "1.1.0", theirFiles)},
		proposals: []api.Proposal{prop},
		bundles:   map[string]*pack.Pack{prop.ID: kbPack(t, "1.2.0", map[string]string{"guide.md": "LINE1\nline2\nline3\nline4\nline5\n", "a.md": "a\n", "b.md": "b\n", "c.md": "c\n"})},
	}
	svc, _ := newService(t, reg)
	dir := filepath.Join(t.TempDir(), "runbooks") // not there yet
	o, err := svc.Locate(t.Context(), dir, nil, rebase.Options{Proposal: prop.ID})
	if err != nil || o.Base != "1.0.0" || o.Replaces != prop.ID || !o.Behind() {
		t.Fatalf("Locate(--proposal) = %+v, %v", o, err)
	}
	res, err := svc.Rebase(t.Context(), dir, nil, o)
	if err != nil || len(res.Conflicts) != 0 {
		t.Fatalf("Rebase(--proposal) = %+v, %v", res, err)
	}
	p, err := pack.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(p.Files()["guide.md"]); got != "LINE1\nline2\nline3\nline4\nLINE5\n" || p.Version().String() != "1.2.0" {
		t.Errorf("rebased proposal = %s with guide.md %q", p.Version(), got)
	}
}

func TestRecordsAreStrictAndPrivate(t *testing.T) {
	dir := t.TempDir()
	records := rebase.NewRecords(dir)
	folder := filepath.Join(dir, "runbooks")
	if err := records.Put(folder, rebase.Record{Remote: remoteURL, Package: "lime/runbooks", Onto: "1.1.0", Conflicts: []string{"guide.md"}, At: at}); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(dir, "rebase.yaml"))
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("rebase.yaml = %v, %v; want mode 0600", info, err)
	}
	rec, ok, err := records.Get(folder)
	if err != nil || !ok || rec.Onto != "1.1.0" || !rec.At.Equal(at) || !slices.Equal(rec.Conflicts, []string{"guide.md"}) {
		t.Errorf("Get() = %+v, %v, %v", rec, ok, err)
	}
	if err := records.Delete(folder); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := records.Get(folder); ok || err != nil {
		t.Errorf("Get() after Delete = %v, %v", ok, err)
	}
	if err := os.WriteFile(filepath.Join(dir, "rebase.yaml"), []byte("folders: {}\nsurprise: 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := records.Get(folder); err == nil || !strings.Contains(err.Error(), "fix or delete it") {
		t.Errorf("Get() of a file with an unknown field error = %v, want refused", err)
	}
}

// A release can land while the author is still resolving the last rebase.
// Rebasing again must not forget the markers and undecided files left by
// the first one, or propose and publish would let them through: either the
// second rebase refuses and leaves the folder alone, or what is unresolved
// is still reported afterwards.
func TestRebaseAgainKeepsUnfinishedConflicts(t *testing.T) {
	newer := maps.Clone(theirFiles)
	newer["c.md"] = "c3\n"
	reg := &fakeRegistry{releases: []*pack.Pack{kbPack(t, "1.0.0", baseFiles), kbPack(t, "1.1.0", theirFiles)}}
	svc, _ := newService(t, reg)
	dir := filepath.Join(t.TempDir(), "runbooks")
	ours := map[string]string{"guide.md": "line1\nline2\nline3\nline4\nfive\n", "a.md": "a\n", "c.md": "c\n"} // b.md deleted
	p := writeFolder(t, dir, kbFiles("1.2.0", ours))
	if _, err := svc.Rebase(t.Context(), dir, p, rebase.Origin{Package: runbooks, Base: "1.0.0", Latest: "1.1.0"}); err != nil {
		t.Fatal(err)
	}
	first, err := svc.Unresolved(dir)
	if err != nil || !slices.Equal(first.Markers, []string{"guide.md"}) || !slices.Equal(first.Undecided, []string{"b.md"}) {
		t.Fatalf("Unresolved() after the first rebase = %+v, %v", first, err)
	}

	// 1.1.1 lands; the author rebases again without resolving anything.
	reg.releases = append(reg.releases, kbPack(t, "1.1.1", newer))
	p, err = pack.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	o, err := svc.Locate(t.Context(), dir, p, rebase.Options{})
	if err != nil || !o.Behind() || o.Base != "1.1.0" {
		t.Fatalf("Locate() = %+v, %v; want behind from 1.1.0", o, err)
	}
	before := p.Files()
	_, err = svc.Rebase(t.Context(), dir, p, o)
	if err != nil {
		if !strings.Contains(err.Error(), "guide.md") {
			t.Errorf("Rebase() refused with %v; want it to name guide.md", err)
		}
		after, lerr := pack.Load(dir)
		if lerr != nil || !maps.EqualFunc(after.Files(), before, func(a, b []byte) bool { return string(a) == string(b) }) {
			t.Errorf("a refused rebase changed the folder (%v)", lerr)
		}
	}
	got, uerr := svc.Unresolved(dir)
	if uerr != nil || !slices.Contains(got.Markers, "guide.md") || !slices.Contains(got.Undecided, "b.md") {
		t.Errorf("Unresolved() after rebasing again = %+v, %v; want guide.md's markers and b.md still reported", got, uerr)
	}
}
