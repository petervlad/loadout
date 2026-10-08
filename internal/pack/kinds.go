package pack

import (
	"fmt"
	"maps"
	"slices"

	"github.com/petervlad/loadout/internal/agent"
	"github.com/petervlad/loadout/internal/kb"
	"github.com/petervlad/loadout/internal/skill"
)

// Kind is what a package contains.
type Kind string

// The package kinds.
const (
	// KindAgent is one agent: agent.md.
	KindAgent Kind = "agent"
	// KindSkill is one skill: SKILL.md and optional supporting files.
	KindSkill Kind = "skill"
	// KindKB is a knowledge base: documents, installed as a skill whose
	// SKILL.md loadout generates.
	KindKB Kind = "kb"
)

// kindSpec is everything that differs between kinds. Adding a kind is one
// entry here, its content type and a case in target.Render.
type kindSpec struct {
	// main is the file an author edits first and a reviewer reads first;
	// empty when loadout generates it (a knowledge base's index).
	main string
	// what names the kind in sentences; article goes before it.
	what, article string
	// parse validates the content files (every file but the manifest) and
	// stores the parsed content in p.
	parse func(p *Pack, content map[string][]byte) error
	// scaffold returns the content files of a new package.
	scaffold func(id ID) (map[string][]byte, error)
	// limits bound the package, manifest included.
	limits Limits
}

// An agent is one prompt, so it stays small. Skills and knowledge bases
// hold reference material; a team's mirror of a wiki space runs to
// thousands of documents.
var (
	agentLimits   = Limits{MaxFiles: 16, MaxFileSize: 512 << 10, MaxTotal: 1 << 20}
	contentLimits = Limits{MaxFiles: 5000, MaxFileSize: 2 << 20, MaxTotal: 64 << 20}
)

var kinds = map[Kind]kindSpec{
	KindAgent: {main: AgentFile, what: "agent", article: "an", parse: parseAgent, scaffold: scaffoldAgent, limits: agentLimits},
	KindSkill: {main: skill.File, what: "skill", article: "a", parse: parseSkill, scaffold: scaffoldSkill, limits: contentLimits},
	KindKB:    {what: "KB", article: "a", parse: parseKB, scaffold: scaffoldKB, limits: contentLimits},
}

// Kinds returns every kind, in a stable order.
func Kinds() []Kind { return []Kind{KindAgent, KindSkill, KindKB} }

// Valid reports whether k is a known kind.
func (k Kind) Valid() bool {
	_, ok := kinds[k]
	return ok
}

// MainFile returns the file an author edits first, or "" for a kind whose
// main file loadout generates.
func (k Kind) MainFile() string { return kinds[k].main }

// Describe names the kind in a sentence: "agent", "knowledge base".
func (k Kind) Describe() string {
	if s, ok := kinds[k]; ok {
		return s.what
	}
	return string(k)
}

// WithArticle names the kind in a sentence with its article: "an agent",
// "a skill".
func (k Kind) WithArticle() string {
	if s, ok := kinds[k]; ok {
		return s.article + " " + s.what
	}
	return "a " + string(k)
}

// Limits returns the size limits of a package of kind k.
func (k Kind) Limits() Limits { return kinds[k].limits }

func parseAgent(p *Pack, content map[string][]byte) error {
	for _, name := range slices.Sorted(maps.Keys(content)) {
		if name != AgentFile {
			return fmt.Errorf("unexpected file %q: an agent package contains only %s and %s", name, ManifestFile, AgentFile)
		}
	}
	data, ok := content[AgentFile]
	if !ok {
		return fmt.Errorf("missing %s", AgentFile)
	}
	def, err := agent.Parse(data)
	if err != nil {
		return fmt.Errorf("%s: %w", AgentFile, err)
	}
	if def.Name != p.manifest.ID.Name() {
		return fmt.Errorf("%s names the agent %q but %s names the package %q; they must match", AgentFile, def.Name, ManifestFile, p.manifest.ID.Name())
	}
	p.agent = &def
	return nil
}

func parseSkill(p *Pack, content map[string][]byte) error {
	main, ok := content[skill.File]
	if !ok {
		return fmt.Errorf("missing %s", skill.File)
	}
	rest := maps.Clone(content)
	delete(rest, skill.File)
	s, err := skill.Parse(main, rest)
	if err != nil {
		return fmt.Errorf("%s: %w", skill.File, err)
	}
	if s.Name != p.manifest.ID.Name() {
		return fmt.Errorf("%s names the skill %q but %s names the package %q; they must match", skill.File, s.Name, ManifestFile, p.manifest.ID.Name())
	}
	p.skill = s
	return nil
}

func parseKB(p *Pack, content map[string][]byte) error {
	b, err := kb.Parse(p.manifest.ID.Name(), p.manifest.Description, content)
	if err != nil {
		return err
	}
	s, err := b.Skill()
	if err != nil {
		return err
	}
	p.kb, p.skill = b, s
	return nil
}

func scaffoldAgent(id ID) (map[string][]byte, error) {
	data, err := agent.Definition{
		Name:        id.Name(),
		Description: "What this agent does. Use when the user says ...",
		Model:       agent.ModelBalanced,
		Tools:       []agent.Tool{agent.ToolRead, agent.ToolSearch},
		Prompt:      "You are the team's " + id.Name() + ". Explain the role, the steps to follow and what to return.",
	}.Marshal()
	if err != nil {
		return nil, err
	}
	return map[string][]byte{AgentFile: data}, nil
}

func scaffoldSkill(id ID) (map[string][]byte, error) {
	s, err := skill.New(id.Name(),
		"What this skill does, and when to use it: Use when the user asks to ...",
		"",
		"# "+id.Name()+"\n\nThe steps to follow, in order. Put long reference material in references/ and link it from here, so it loads only when needed.",
		map[string][]byte{"references/README.md": []byte("# References\n\nDetails the skill reads only when a step needs them.\n")})
	if err != nil {
		return nil, err
	}
	main, err := s.Marshal("")
	if err != nil {
		return nil, err
	}
	files := s.Files()
	files[skill.File] = main
	return files, nil
}

func scaffoldKB(id ID) (map[string][]byte, error) {
	return map[string][]byte{
		"README.md": []byte("# " + id.Name() + "\n\nWhat this knowledge base covers. Add Markdown or text documents next to this one, in folders if you like.\n"),
	}, nil
}
