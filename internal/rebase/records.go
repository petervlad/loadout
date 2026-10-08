package rebase

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"go.yaml.in/yaml/v3"

	"github.com/petervlad/loadout/internal/atomicfile"
	"github.com/petervlad/loadout/internal/strictyaml"
)

// Record is what loadout remembers about a folder it rebased, until the
// folder is proposed or published: where it came from, what it was merged
// onto, the proposal it replaces, and the files left with conflicts.
type Record struct {
	// Remote is the registry's URL; a record from another registry is
	// ignored.
	Remote  string `yaml:"remote"`
	Package string `yaml:"package"`
	// Base is the release the folder was made from, Onto the release it
	// was merged onto ("" for none).
	Base string `yaml:"base"`
	Onto string `yaml:"onto"`
	// Replaces is the open proposal the folder updates, if any.
	Replaces string `yaml:"replaces,omitempty"`
	// Labels are the names in the conflict markers written.
	Labels Labels `yaml:"labels"`
	// Conflicts are the files written with conflicts, by package path.
	Conflicts []string `yaml:"conflicts,omitempty"`
	// Undecided are the files one side deleted and the other changed,
	// until the author deletes them or keeps them (MarkResolved).
	Undecided []string  `yaml:"undecided,omitempty"`
	At        time.Time `yaml:"at"`
}

// Labels name the three sides in conflict markers.
type Labels struct {
	Base   string `yaml:"base"`
	Ours   string `yaml:"ours"`
	Theirs string `yaml:"theirs"`
}

// recordsFile is the on-disk shape of rebase.yaml, keyed by absolute path.
type recordsFile struct {
	Folders map[string]Record `yaml:"folders"`
}

// Records keeps a Record per package folder in rebase.yaml in the state
// folder (strict YAML, 0600, written atomically), so loadout's own state
// stays out of the author's repository. Two loadout runs at once may lose
// one record; the folders are never touched.
type Records struct{ path string }

// NewRecords keeps its records in dir/rebase.yaml.
func NewRecords(dir string) *Records {
	return &Records{path: filepath.Join(dir, "rebase.yaml")}
}

// Get returns the record of folder, an absolute path.
func (r *Records) Get(folder string) (Record, bool, error) {
	f, err := r.load()
	if err != nil {
		return Record{}, false, err
	}
	rec, ok := f.Folders[filepath.Clean(folder)]
	return rec, ok, nil
}

// Put records rec for folder, an absolute path.
func (r *Records) Put(folder string, rec Record) error {
	f, err := r.load()
	if err != nil {
		return err
	}
	if f.Folders == nil {
		f.Folders = map[string]Record{}
	}
	f.Folders[filepath.Clean(folder)] = rec
	return r.save(f)
}

// Delete forgets folder's record; none is not an error.
func (r *Records) Delete(folder string) error {
	f, err := r.load()
	if err != nil {
		return err
	}
	if _, ok := f.Folders[filepath.Clean(folder)]; !ok {
		return nil
	}
	delete(f.Folders, filepath.Clean(folder))
	return r.save(f)
}

func (r *Records) save(f recordsFile) error {
	data, err := yaml.Marshal(f)
	if err != nil {
		return fmt.Errorf("encode %s: %w", r.path, err)
	}
	if err := os.MkdirAll(filepath.Dir(r.path), 0o700); err != nil {
		return fmt.Errorf("write %s: %w", r.path, err)
	}
	if err := atomicfile.Write(r.path, data, 0o600); err != nil {
		return fmt.Errorf("write %s: %w", r.path, err)
	}
	return nil
}

func (r *Records) load() (recordsFile, error) {
	var f recordsFile
	data, err := os.ReadFile(r.path)
	if errors.Is(err, fs.ErrNotExist) {
		return f, nil
	}
	if err != nil {
		return f, fmt.Errorf("read %s: %w", r.path, err)
	}
	if err := strictyaml.Decode(data, &f); err != nil && !errors.Is(err, strictyaml.ErrEmpty) {
		return f, fmt.Errorf("parse %s: %w; fix or delete it (it only remembers which folders were rebased onto which release)", r.path, err)
	}
	return f, nil
}
