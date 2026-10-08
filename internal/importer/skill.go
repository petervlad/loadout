package importer

import (
	"errors"
	"fmt"
	"maps"
	"path/filepath"
	"strings"

	"github.com/petervlad/loadout/internal/pack"
	"github.com/petervlad/loadout/internal/skill"
)

// KBOptions are the settings of a knowledge base import.
type KBOptions struct {
	// Options.Name is the package name; empty means the folder's name.
	Options Options
	// Description says what the documents cover; tools read it to decide
	// when to look in them.
	Description string
}

// Skills converts skill folders (SKILL.md and supporting files) into
// skill package folders in opts.Out. SKILL.md is copied as it is, so
// fields loadout does not read stay. Nothing is written unless every
// folder converts.
func Skills(dirs []string, opts Options) ([]Result, error) {
	if err := opts.checkName(len(dirs)); err != nil {
		return nil, err
	}
	plans := make([]plan, 0, len(dirs))
	var errs []error
	for _, dir := range dirs {
		p, err := planSkill(dir, opts)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", dir, err))
			continue
		}
		plans = append(plans, p)
	}
	if err := errors.Join(errs...); err != nil {
		return nil, err
	}
	return write(plans)
}

func planSkill(dir string, opts Options) (plan, error) {
	files, err := readSource(dir)
	if err != nil {
		return plan{}, err
	}
	main, ok := files[skill.File]
	if !ok {
		return plan{}, fmt.Errorf("no %s: a skill folder has its instructions in %s", skill.File, skill.File)
	}
	main, notices, err := quoteFields(main)
	if err != nil {
		return plan{}, fmt.Errorf("%s: parse frontmatter: %w", skill.File, err)
	}
	rest := maps.Clone(files)
	delete(rest, skill.File)
	s, err := skill.Parse(main, rest)
	if err != nil {
		return plan{}, fmt.Errorf("%s: %w", skill.File, err)
	}
	// The package name is the skill's name, so another name is written
	// into the copy's SKILL.md.
	if opts.Name != "" && opts.Name != s.Name {
		renamed, err := s.Renamed(opts.Name)
		if err != nil {
			return plan{}, err
		}
		if main, err = renamed.Marshal(""); err != nil {
			return plan{}, err
		}
		notices = append(notices, fmt.Sprintf("renamed the skill from %s to %s in %s", s.Name, renamed.Name, skill.File))
		s = renamed
	}
	files[skill.File] = main
	if extra := s.Extra(); len(extra) > 0 {
		notices = append(notices, fmt.Sprintf("kept fields %s as they are; loadout does not read them", strings.Join(extra, ", ")))
	}
	return opts.plan(dir, s.Name, pack.KindSkill, summary(s.Description), files, notices)
}

// KB converts a folder of documents into a knowledge base package folder
// in opts.Options.Out.
func KB(dir string, opts KBOptions) (Result, error) {
	fail := func(err error) (Result, error) { return Result{}, fmt.Errorf("%s: %w", dir, err) }
	files, err := readSource(dir)
	if err != nil {
		return fail(err)
	}
	name := opts.Options.Name
	if name == "" {
		name = filepath.Base(filepath.Clean(dir))
	}
	p, err := opts.Options.plan(dir, name, pack.KindKB, strings.TrimSpace(opts.Description), files, nil)
	if err != nil {
		return fail(err)
	}
	results, err := write([]plan{p})
	if err != nil {
		return Result{}, err
	}
	return results[0], nil
}

// readSource reads a folder to import the way a package folder is read:
// hidden files and folders are skipped, symbolic links refused.
func readSource(dir string) (map[string][]byte, error) {
	files, err := pack.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	if _, ok := files[pack.ManifestFile]; ok {
		return nil, fmt.Errorf("already a loadout package (it has %s); publish it as it is", pack.ManifestFile)
	}
	return files, nil
}
