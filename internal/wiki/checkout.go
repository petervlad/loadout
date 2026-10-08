package wiki

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"go.yaml.in/yaml/v3"

	"github.com/petervlad/loadout/internal/atomicfile"
	"github.com/petervlad/loadout/internal/strictyaml"
)

// Ref names a page: the registry's URL and the namespace.
type Ref struct {
	Remote    string `yaml:"remote"`
	Namespace string `yaml:"namespace"`
}

// Checkout is what loadout knows about one local file: the page it holds,
// the revision it was pulled or pushed at, and the digest of the file as
// it was then, so an edit since shows.
type Checkout struct {
	Ref      `yaml:",inline"`
	Revision int    `yaml:"revision"`
	Digest   string `yaml:"digest"`
}

// Edited reports whether data differs from what was recorded.
func (c Checkout) Edited(data []byte) bool { return Digest(data) != c.Digest }

// Digest is the digest a checkout records of a file's bytes.
func Digest(data []byte) string {
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// checkoutsFile is the on-disk shape of wiki.yaml, keyed by absolute path.
type checkoutsFile struct {
	Files map[string]Checkout `yaml:"files"`
}

// Checkouts records, per local file, which page and revision it was pulled
// or pushed at and the digest of what loadout wrote, in wiki.yaml in the
// state folder (strict YAML, 0600, written atomically). Two loadout runs
// at once may lose one record; the file it is about is never touched.
type Checkouts struct{ path string }

// NewCheckouts keeps its records in dir/wiki.yaml.
func NewCheckouts(dir string) *Checkouts {
	return &Checkouts{path: filepath.Join(dir, "wiki.yaml")}
}

// Get returns the record of file, an absolute path.
func (c *Checkouts) Get(file string) (Checkout, bool, error) {
	f, err := c.load()
	if err != nil {
		return Checkout{}, false, err
	}
	co, ok := f.Files[filepath.Clean(file)]
	return co, ok, nil
}

// Put records co for file, an absolute path.
func (c *Checkouts) Put(file string, co Checkout) error {
	f, err := c.load()
	if err != nil {
		return err
	}
	if f.Files == nil {
		f.Files = map[string]Checkout{}
	}
	f.Files[filepath.Clean(file)] = co
	data, err := yaml.Marshal(f)
	if err != nil {
		return fmt.Errorf("encode %s: %w", c.path, err)
	}
	if err := os.MkdirAll(filepath.Dir(c.path), 0o700); err != nil {
		return fmt.Errorf("write %s: %w", c.path, err)
	}
	if err := atomicfile.Write(c.path, data, 0o600); err != nil {
		return fmt.Errorf("write %s: %w", c.path, err)
	}
	return nil
}

func (c *Checkouts) load() (checkoutsFile, error) {
	var f checkoutsFile
	data, err := os.ReadFile(c.path)
	if errors.Is(err, fs.ErrNotExist) {
		return f, nil
	}
	if err != nil {
		return f, fmt.Errorf("read %s: %w", c.path, err)
	}
	if err := strictyaml.Decode(data, &f); err != nil && !errors.Is(err, strictyaml.ErrEmpty) {
		return f, fmt.Errorf("parse %s: %w; fix or delete it (it only remembers which revision each wiki file was pulled at)", c.path, err)
	}
	return f, nil
}
