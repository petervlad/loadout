// Package pipeline defines the pipeline format: a template of slots and the
// edges between them, written once by a project, and a binding that fills each
// slot with one of a team's agents. A bound pipeline is what the generated
// conductor agent follows.
//
// The package is pure values: no files, no network, no AI tool. Rendering the
// conductor for a tool belongs to internal/target, which supplies the few
// words that differ per tool through Starter.
//
// On disk a pipeline is one pipeline.yaml in one of two shapes. A template:
//
//	start: plan
//	slots:
//	  plan:
//	    brief: Turn the ticket into plan.md.
//	    writes: plan.md
//	    reads: [ticket]
//	    verdicts: [READY]
//	  dev: ...
//	edges:
//	  - {from: plan, on: READY, to: dev, via: gate}
//
// A binding:
//
//	uses: acme/dev-loop@1
//	bind: {plan: lime-planner, dev: lime-dev}
//	work_folder: explorations/
package pipeline

import (
	"errors"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/petervlad/loadout/internal/agent"
	"github.com/petervlad/loadout/internal/ident"
	"github.com/petervlad/loadout/internal/strictyaml"
)

// File is the main file of a pipeline package.
const File = "pipeline.yaml"

// End is the edge target that finishes a run. It is not a slot name.
const End = "end"

// Questions is the verdict any slot may return to ask the person something.
// It follows no edge: the run stops at a gate until the answer arrives.
const Questions = "QUESTIONS"

// Limits, so a pipeline stays readable on a page and in a prompt.
const (
	MaxSlots     = 12
	MaxVerdicts  = 8
	MaxEdges     = 64
	MaxBrief     = 500
	MaxVerdict   = 32
	MaxRoundsCap = 10
	maxWorkDir   = 200
	maxFileName  = 128
	ticket       = "ticket"
)

var verdictPattern = regexp.MustCompile(`^[A-Z]+( [A-Z]+)*$`)

// Via says whether an edge waits for the person or goes on by itself.
type Via string

// The two kinds of edge.
const (
	Gate Via = "gate"
	Auto Via = "auto"
)

// Valid reports whether v is a known kind of edge.
func (v Via) Valid() bool { return v == Gate || v == Auto }

// Slot is a named stage with its contract. It names no agent.
type Slot struct {
	Name  string
	Brief string
	// Writes is the file the slot appends a round to, ending in a Verdict line.
	Writes string
	// Also lists extra outputs, never checked for a verdict.
	Also []string
	// Reads lists files of other slots, or the word "ticket".
	Reads    []string
	Verdicts []string
}

func (s Slot) clone() Slot {
	s.Also = slices.Clone(s.Also)
	s.Reads = slices.Clone(s.Reads)
	s.Verdicts = slices.Clone(s.Verdicts)
	return s
}

// Allows reports whether the slot may return verdict. Questions is always
// allowed.
func (s Slot) Allows(verdict string) bool {
	return verdict == Questions || slices.Contains(s.Verdicts, verdict)
}

// Edge is the move from one slot to another on one verdict.
type Edge struct {
	From string
	On   string
	// To is a slot name or End.
	To  string
	Via Via
	// MaxRounds caps how often a back edge is followed, and is 0 on other edges.
	MaxRounds int
}

// Ref names a template release line: acme/dev-loop at major 1.
type Ref struct {
	Layer string
	Name  string
	Major int
}

// String formats r as it is written in a binding: acme/dev-loop@1.
func (r Ref) String() string { return fmt.Sprintf("%s/%s@%d", r.Layer, r.Name, r.Major) }

// Needs is what must exist for a pipeline package to work.
type Needs struct {
	// Templates are the pipelines a binding uses. A template needs none.
	Templates []Ref
	// Agents are the agent names a binding binds, without duplicates.
	Agents []string
}

// Package is what pipeline.yaml holds: a *Template or a *Binding.
type Package interface {
	// Needs is what must exist for this package to work.
	Needs() Needs
	pipeline()
}

// Template is a project's pipeline: slots and edges, no agents. Immutable.
type Template struct {
	start string
	slots []Slot
	edges []Edge
}

// Binding is a team's choice of agents for the slots of one template. The
// zero value is not usable: get one from Parse. Immutable.
type Binding struct {
	uses       Ref
	bind       map[string]string
	workFolder string
	model      agent.Model
	gates      []GateRef
}

// GateRef names an edge by its origin and verdict. A binding lists the auto
// edges it wants as gates. There is no way to make a gate automatic.
type GateRef struct {
	From string
	On   string
}

func (*Template) pipeline() {}
func (*Binding) pipeline()  {}

// Needs implements Package: a template needs nothing.
func (*Template) Needs() Needs { return Needs{} }

// Needs implements Package.
func (b *Binding) Needs() Needs {
	var agents []string
	for _, a := range b.bind {
		if a != "" && !slices.Contains(agents, a) {
			agents = append(agents, a)
		}
	}
	slices.Sort(agents)
	return Needs{Templates: []Ref{b.uses}, Agents: agents}
}

// Start returns the name of the first slot.
func (t *Template) Start() string { return t.start }

// Slots returns the slots in the order they are written.
func (t *Template) Slots() []Slot {
	out := make([]Slot, len(t.slots))
	for i, s := range t.slots {
		out[i] = s.clone()
	}
	return out
}

// Slot returns the slot called name.
func (t *Template) Slot(name string) (Slot, bool) {
	if i := t.index(name); i >= 0 {
		return t.slots[i].clone(), true
	}
	return Slot{}, false
}

// Edges returns the edges in the order they are written.
func (t *Template) Edges() []Edge { return slices.Clone(t.edges) }

// Edge returns the edge taken from slot from on verdict.
func (t *Template) Edge(from, on string) (Edge, bool) {
	for _, e := range t.edges {
		if e.From == from && e.On == on {
			return e, true
		}
	}
	return Edge{}, false
}

func (t *Template) index(name string) int {
	return slices.IndexFunc(t.slots, func(s Slot) bool { return s.Name == name })
}

// IsBack reports whether e returns to the slot it left or to an earlier one.
func (t *Template) IsBack(e Edge) bool {
	to := t.index(e.To)
	return to >= 0 && to <= t.index(e.From)
}

// Uses returns the template this binding fills.
func (b *Binding) Uses() Ref { return b.uses }

// Agent returns the agent bound to slot, or "" when it is unbound.
func (b *Binding) Agent(slot string) string { return b.bind[slot] }

// Slots returns the slot names the binding mentions, sorted.
func (b *Binding) Slots() []string {
	names := make([]string, 0, len(b.bind))
	for n := range b.bind {
		names = append(names, n)
	}
	slices.Sort(names)
	return names
}

// WorkFolder returns where runs live, resolved by the conductor against the
// folder the session starts in.
func (b *Binding) WorkFolder() string { return b.workFolder }

// Model returns the conductor's model hint, or ModelUnset.
func (b *Binding) Model() agent.Model { return b.model }

// Gates returns the auto edges the binding turns into gates.
func (b *Binding) Gates() []GateRef { return slices.Clone(b.gates) }

// file is the on-disk shape: the union of both kinds, told apart by Parse.
type file struct {
	Start      string      `yaml:"start"`
	Slots      slotList    `yaml:"slots"`
	Edges      []rawEdge   `yaml:"edges"`
	Uses       string      `yaml:"uses"`
	Bind       rawBind     `yaml:"bind"`
	WorkFolder string      `yaml:"work_folder"`
	Model      agent.Model `yaml:"model"`
	Gate       []rawGate   `yaml:"gate"`
}

type rawSlot struct {
	Brief    string   `yaml:"brief"`
	Writes   string   `yaml:"writes"`
	Also     []string `yaml:"also"`
	Reads    []string `yaml:"reads"`
	Verdicts []string `yaml:"verdicts"`
}

type rawEdge struct {
	From      string `yaml:"from"`
	On        string `yaml:"on"`
	To        string `yaml:"to"`
	Via       Via    `yaml:"via"`
	MaxRounds int    `yaml:"max_rounds"`
}

type rawGate struct {
	From string `yaml:"from"`
	On   string `yaml:"on"`
}

// rawBind keeps an empty value ("plan:") as an empty string, so a scaffolded
// binding parses and the missing agent is reported by Bind, not by the YAML.
type rawBind map[string]string

type slotEntry struct {
	name string
	slot rawSlot
}

// slotList decodes a mapping while keeping the order slots are written in,
// which decides what a back edge is.
type slotList []slotEntry

func (l *slotList) UnmarshalYAML(n *yaml.Node) error {
	if n.Kind != yaml.MappingNode {
		return errors.New("slots must be a mapping from slot name to its contract")
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		name := n.Content[i].Value
		if slices.ContainsFunc(*l, func(e slotEntry) bool { return e.name == name }) {
			return fmt.Errorf("slot %s is listed twice", name)
		}
		// Decoding the node directly would drop strictness for the fields
		// inside, so it goes through strictyaml again.
		raw, err := yaml.Marshal(n.Content[i+1])
		if err != nil {
			return fmt.Errorf("slot %s: %w", name, err)
		}
		var rs rawSlot
		if err := strictyaml.Decode(raw, &rs); err != nil {
			return fmt.Errorf("slot %s: %w", name, err)
		}
		*l = append(*l, slotEntry{name: name, slot: rs})
	}
	return nil
}

// Parse reads a pipeline.yaml and validates it. It returns a *Template when
// the file has slots and edges, a *Binding when it has uses and bind, and an
// error naming every problem when it has both, neither, or breaks a rule.
func Parse(data []byte) (Package, error) {
	var f file
	if err := strictyaml.Decode(data, &f); err != nil {
		return nil, fmt.Errorf("parse %s: %w", File, err)
	}
	isTemplate := f.Start != "" || len(f.Slots) > 0 || len(f.Edges) > 0
	isBinding := f.Uses != "" || len(f.Bind) > 0 || f.WorkFolder != "" || f.Model != "" || len(f.Gate) > 0
	switch {
	case isTemplate && isBinding:
		return nil, fmt.Errorf("%s has both slots and uses: a template has slots and edges, a binding has uses and bind", File)
	case isTemplate:
		return parseTemplate(f)
	case isBinding:
		return parseBinding(f)
	}
	return nil, fmt.Errorf("%s is empty: a template has slots and edges, a binding has uses and bind", File)
}

func parseTemplate(f file) (*Template, error) {
	t := &Template{start: f.Start}
	for _, e := range f.Slots {
		t.slots = append(t.slots, Slot{
			Name:     e.name,
			Brief:    strings.TrimSpace(e.slot.Brief),
			Writes:   e.slot.Writes,
			Also:     slices.Clone(e.slot.Also),
			Reads:    slices.Clone(e.slot.Reads),
			Verdicts: slices.Clone(e.slot.Verdicts),
		})
	}
	for _, e := range f.Edges {
		t.edges = append(t.edges, Edge(e))
	}
	if err := t.validate(); err != nil {
		return nil, fmt.Errorf("%s: %w", File, err)
	}
	return t, nil
}

func (t *Template) validate() error {
	var errs []error
	add := func(format string, args ...any) { errs = append(errs, fmt.Errorf(format, args...)) }

	if len(t.slots) == 0 {
		add("no slots")
	}
	if len(t.slots) > MaxSlots {
		add("%d slots, the limit is %d", len(t.slots), MaxSlots)
	}
	if len(t.edges) > MaxEdges {
		add("%d edges, the limit is %d", len(t.edges), MaxEdges)
	}
	if t.start == "" {
		add("start is empty: name the first slot")
	} else if t.index(t.start) < 0 {
		add("start names %q, which is not a slot", t.start)
	}

	files := map[string]string{} // file name to the slot that writes it
	for _, s := range t.slots {
		errs = append(errs, t.checkSlot(s, files)...)
	}
	for _, s := range t.slots {
		for _, r := range s.Reads {
			if r == ticket {
				continue
			}
			if owner, ok := files[r]; !ok {
				add("slot %s reads %q, which no slot writes (use a file of another slot, or %q)", s.Name, r, ticket)
			} else if owner == s.Name {
				add("slot %s reads %q, its own file", s.Name, r)
			}
		}
	}
	errs = append(errs, t.checkEdges()...)
	return errors.Join(errs...)
}

func (t *Template) checkSlot(s Slot, files map[string]string) []error {
	var errs []error
	add := func(format string, args ...any) {
		errs = append(errs, fmt.Errorf("slot %s: %w", s.Name, fmt.Errorf(format, args...)))
	}

	if err := ident.Check("slot", s.Name); err != nil {
		errs = append(errs, err)
	}
	if s.Name == End {
		add("%q is reserved", End)
	}
	switch {
	case s.Brief == "":
		add("brief is empty")
	case len(s.Brief) > MaxBrief:
		add("brief is %d bytes, the limit is %d", len(s.Brief), MaxBrief)
	case strings.ContainsFunc(s.Brief, func(r rune) bool { return r < ' ' || r == 0x7f }):
		add("brief holds a newline or control character: write it as one line")
	}
	for _, f := range append([]string{s.Writes}, s.Also...) {
		if err := checkFileName(f); err != nil {
			add("%v", err)
		} else if owner, dup := files[f]; dup {
			add("file %s is also written by slot %s", f, owner)
		} else if clash := caseClash(files, f); clash != "" {
			// macOS and Windows would write both names to one file.
			add("file %s differs from %s only in case", f, clash)
		} else if f != "" {
			files[f] = s.Name
		}
	}
	if s.Writes == "" {
		add("writes is empty: name the file the slot appends its rounds to")
	}
	if slices.Contains(s.Reads, s.Writes) {
		add("reads its own file %s", s.Writes)
	}
	switch {
	case len(s.Verdicts) == 0:
		add("verdicts is empty")
	case len(s.Verdicts) > MaxVerdicts:
		add("%d verdicts, the limit is %d", len(s.Verdicts), MaxVerdicts)
	}
	for i, v := range s.Verdicts {
		switch {
		case !verdictPattern.MatchString(v) || len(v) > MaxVerdict:
			add("verdict %q must be upper-case words separated by single spaces, at most %d bytes", v, MaxVerdict)
		case v == Questions:
			add("verdict %s is reserved: every slot may return it without listing it", Questions)
		case slices.Contains(s.Verdicts[:i], v):
			add("verdict %s is listed twice", v)
		}
	}
	return errs
}

func (t *Template) checkEdges() []error {
	var errs []error
	add := func(format string, args ...any) { errs = append(errs, fmt.Errorf(format, args...)) }

	type key struct{ from, on string }
	seen := map[key]bool{}
	for _, e := range t.edges {
		at := fmt.Sprintf("edge %s on %s", e.From, e.On)
		from := t.index(e.From)
		switch {
		case from < 0:
			add("%s: %q is not a slot", at, e.From)
			continue
		case !slices.Contains(t.slots[from].Verdicts, e.On):
			add("%s: slot %s has no verdict %q (QUESTIONS follows no edge)", at, e.From, e.On)
			continue
		case seen[key{e.From, e.On}]:
			add("%s is listed twice: each verdict has exactly one edge", at)
			continue
		}
		seen[key{e.From, e.On}] = true

		if e.To != End && t.index(e.To) < 0 {
			add("%s: to %q is not a slot (use %s to finish)", at, e.To, End)
		}
		if !e.Via.Valid() {
			add("%s: via %q must be gate or auto", at, e.Via)
		}
		if e.MaxRounds != 0 {
			switch {
			case e.MaxRounds < 1 || e.MaxRounds > MaxRoundsCap:
				add("%s: max_rounds %d must be between 1 and %d", at, e.MaxRounds, MaxRoundsCap)
			case !t.IsBack(e):
				add("%s: max_rounds is only allowed on a back edge, one that returns to the same slot or an earlier one", at)
			}
		}
	}
	for _, s := range t.slots {
		for _, v := range s.Verdicts {
			if !seen[key{s.Name, v}] {
				add("slot %s: verdict %s has no edge", s.Name, v)
			}
		}
	}
	return errs
}

// checkFileName accepts one path segment of letters, digits, '.', '_' and
// '-', the same alphabet pack.CheckPath allows, without a leading dot.
func checkFileName(f string) error {
	switch {
	case f == "":
		return nil // the caller reports an empty writes
	case len(f) > maxFileName:
		return fmt.Errorf("file name %q is longer than %d characters", f, maxFileName)
	case strings.HasPrefix(f, "."):
		return fmt.Errorf("file name %q must not start with a dot", f)
	case strings.IndexFunc(f, func(r rune) bool {
		return (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && (r < '0' || r > '9') && r != '.' && r != '_' && r != '-'
	}) >= 0:
		return fmt.Errorf("file name %q: only letters, digits, '.', '_' and '-' may be used, in one path segment", f)
	case f == ticket:
		return fmt.Errorf("file name %q is reserved for the ticket", f)
	}
	return nil
}

func parseBinding(f file) (*Binding, error) {
	var errs []error
	add := func(format string, args ...any) { errs = append(errs, fmt.Errorf(format, args...)) }

	b := &Binding{bind: map[string]string{}, workFolder: f.WorkFolder, model: f.Model}
	ref, err := ParseRef(f.Uses)
	if err != nil {
		errs = append(errs, err)
	}
	b.uses = ref
	if len(f.Bind) == 0 {
		add("bind is empty: list every slot of the template with its agent")
	}
	for _, slot := range slices.Sorted(maps.Keys(f.Bind)) {
		a := f.Bind[slot]
		if err := ident.Check("slot", slot); err != nil {
			errs = append(errs, err)
			continue
		}
		if a != "" {
			if err := ident.Check("agent", a); err != nil {
				add("slot %s: %v", slot, err)
				continue
			}
		}
		b.bind[slot] = a
	}
	if err := checkWorkFolder(f.WorkFolder); err != nil {
		errs = append(errs, err)
	}
	if !f.Model.Valid() {
		add("unknown model hint %q: want fast, balanced or deep", f.Model)
	}
	for _, g := range f.Gate {
		if g.From == "" || g.On == "" {
			add("gate entries need from and on")
			continue
		}
		b.gates = append(b.gates, GateRef(g))
	}
	if err := errors.Join(errs...); err != nil {
		return nil, fmt.Errorf("%s: %w", File, err)
	}
	return b, nil
}

// ParseRef reads a template reference such as "acme/dev-loop@1". Only a major
// is accepted: a binding follows every release inside it.
func ParseRef(s string) (Ref, error) {
	id, major, ok := strings.Cut(s, "@")
	layer, name, slash := strings.Cut(id, "/")
	if !ok || !slash {
		return Ref{}, fmt.Errorf("uses %q: want LAYER/NAME@MAJOR, for example acme/dev-loop@1", s)
	}
	if err := ident.Check("team", layer); err != nil {
		return Ref{}, fmt.Errorf("uses %q: %w", s, err)
	}
	if err := ident.Check("pipeline", name); err != nil {
		return Ref{}, fmt.Errorf("uses %q: %w", s, err)
	}
	n, err := strconv.Atoi(major)
	if err != nil || n < 1 || n > 1<<31-1 || strconv.Itoa(n) != major {
		return Ref{}, fmt.Errorf("uses %q: the major %q must be a whole number from 1, not a full version", s, major)
	}
	return Ref{Layer: layer, Name: name, Major: n}, nil
}

func checkWorkFolder(w string) error {
	switch {
	case w == "":
		return errors.New("work_folder is empty: name the folder the runs live in, for example explorations/")
	case len(w) > maxWorkDir:
		return fmt.Errorf("work_folder is longer than %d bytes", maxWorkDir)
	case strings.ContainsFunc(w, func(r rune) bool { return r < ' ' || r == 0x7f }):
		return errors.New("work_folder holds a control character")
	case strings.Contains(w, "`"):
		return errors.New("work_folder must not contain a backtick")
	case slices.Contains(strings.Split(w, "/"), ".."):
		return fmt.Errorf("work_folder %q must not contain a .. folder", w)
	}
	return nil
}

// caseClash returns a file in files that f differs from only in case.
func caseClash(files map[string]string, f string) string {
	for other := range files {
		if strings.EqualFold(other, f) {
			return other
		}
	}
	return ""
}
