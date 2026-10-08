package builtin

import (
	"path"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// recipeKind is one folder of recipe files that share an outline: a person
// adds a provider with one file and one row in the skill's table, and these
// tests are what keeps every file in the same shape.
type recipeKind struct {
	skill string
	// dir is the folder under the skill, such as references/providers.
	dir string
	// outline is the level 2 headings every file has, in this order.
	outline []string
	// shape is set when each file must name a shape file that exists.
	shape bool
}

var recipeKinds = []recipeKind{
	{
		skill: "loadout-host-provision", dir: "references/providers", shape: true,
		outline: []string{"Before you start", "Create the machine", "Firewall", "Name", "Deploy", "Off-host backups", "Cost", "Tear down"},
	},
	{
		skill: "loadout-host-setup", dir: "references/email",
		outline: []string{"Account", "Sender domain and DNS", "SMTP credentials", "Values for loadout.env", "Limits and sandbox", "Cost"},
	},
}

var (
	verifiedLine = regexp.MustCompile(`(?m)^Verified how: .*\b20\d\d-\d\d-\d\d\b`)
	sourcesLine  = regexp.MustCompile(`(?m)^Sources:\n(- .*\n)*- https?://`)
	shapeLine    = regexp.MustCompile(`(?m)^Shape: \[(\w+)\]\((\.\./shapes/[a-z-]+\.md)\)`)
)

// recipeFiles returns the markdown files of the kind's folder, by path
// inside the skill.
func recipeFiles(t *testing.T, k recipeKind) map[string]string {
	t.Helper()
	files, err := host.Files(k.skill)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for p, data := range files {
		if path.Dir(p) == k.dir && strings.HasSuffix(p, ".md") {
			out[p] = string(data)
		}
	}
	if len(out) == 0 {
		t.Fatalf("%s has no files in %s", k.skill, k.dir)
	}
	return out
}

// headings returns the level 2 headings of markdown, ignoring code blocks.
func headings(md string) []string {
	var out []string
	inFence := false
	for line := range strings.Lines(md) {
		line = strings.TrimRight(line, "\n")
		switch {
		case strings.HasPrefix(line, "```"):
			inFence = !inFence
		case !inFence && strings.HasPrefix(line, "## "):
			out = append(out, strings.TrimPrefix(line, "## "))
		}
	}
	return out
}

// TestRecipeFilesHaveTheOutline: every recipe has the same headings in the
// same order, says how it was verified and when, and cites its sources.
func TestRecipeFilesHaveTheOutline(t *testing.T) {
	for _, k := range recipeKinds {
		files, err := host.Files(k.skill)
		if err != nil {
			t.Fatal(err)
		}
		for p, text := range recipeFiles(t, k) {
			if got := headings(text); !slices.Equal(got, k.outline) {
				t.Errorf("%s/%s: headings %q, want %q", k.skill, p, got, k.outline)
			}
			if !verifiedLine.MatchString(text) {
				t.Errorf("%s/%s: no \"Verified how:\" line with a date", k.skill, p)
			}
			if !sourcesLine.MatchString(text) {
				t.Errorf("%s/%s: no \"Sources:\" list with a link", k.skill, p)
			}
			if !k.shape {
				continue
			}
			m := shapeLine.FindStringSubmatch(text)
			if m == nil {
				t.Errorf("%s/%s: no \"Shape: [VM](../shapes/vm.md)\" line", k.skill, p)
				continue
			}
			if _, ok := files[path.Join(path.Dir(p), m[2])]; !ok {
				t.Errorf("%s/%s: names shape %s, which does not exist", k.skill, p, m[2])
			}
		}
	}
}

// tablePaths returns the backticked paths under dirs that appear in the
// table rows of markdown, in order.
func tablePaths(md string, prefixes ...string) []string {
	var out []string
	for line := range strings.Lines(md) {
		if line = strings.TrimSpace(line); !strings.HasPrefix(line, "|") {
			continue
		}
		for _, m := range regexp.MustCompile("`([^`]+)`").FindAllStringSubmatch(line, -1) {
			if slices.ContainsFunc(prefixes, func(p string) bool { return strings.HasPrefix(m[1], p) }) {
				out = append(out, m[1])
			}
		}
	}
	return out
}

// TestSkillTablesListEveryFileOnce: the tables in a host skill's SKILL.md
// name every reference and asset file exactly once, and nothing else.
func TestSkillTablesListEveryFileOnce(t *testing.T) {
	for _, skill := range linted {
		files, err := host.Files(skill)
		if err != nil {
			t.Fatal(err)
		}
		var want []string
		for p := range files {
			if strings.HasPrefix(p, "references/") || strings.HasPrefix(p, "assets/") {
				want = append(want, p)
			}
		}
		got := tablePaths(string(files["SKILL.md"]), "references/", "assets/")
		slices.Sort(want)
		slices.Sort(got)
		if !slices.Equal(want, got) {
			t.Errorf("%s: SKILL.md tables list %q, but the folder has %q", skill, got, want)
		}
	}
}

// TestProviderTableMatchesTheFiles: the shape in each provider row is the
// one its file names.
func TestProviderTableMatchesTheFiles(t *testing.T) {
	files, err := host.Files("loadout-host-provision")
	if err != nil {
		t.Fatal(err)
	}
	rows := 0
	for line := range strings.Lines(string(files["SKILL.md"])) {
		cells := strings.Split(line, "|")
		if len(cells) < 5 || !strings.Contains(line, "`references/providers/") {
			continue
		}
		rows++
		shape, file := strings.TrimSpace(cells[2]), strings.Trim(strings.TrimSpace(cells[3]), "`")
		m := shapeLine.FindStringSubmatch(string(files[file]))
		if m == nil || !strings.EqualFold(m[1], shape) {
			t.Errorf("%s: the table says shape %q, the file says %v", file, shape, m)
		}
	}
	if rows < 6 {
		t.Errorf("the provider table has %d rows, want at least 6 (hetzner, aws, digitalocean, fly, vps, home)", rows)
	}
}

// guardrails are what every host skill must say, so none can lose them in
// an edit.
var guardrails = []string{
	"never calls a model",
	"Never ask for, read, print or paste a secret",
	"Never open `loadout.env`",
	"loadout serve --check",
	"Never `set -x`",
	"--data /var/lib/loadout",
	"Ask before",
}

func TestHostSkillsCarryTheGuardrails(t *testing.T) {
	for _, name := range linted {
		files, err := host.Files(name)
		if err != nil {
			t.Fatal(err)
		}
		body := string(files["SKILL.md"])
		for _, g := range guardrails {
			if !strings.Contains(body, g) {
				t.Errorf("%s does not say %q", name, g)
			}
		}
	}
}

// printers read a file to the terminal, which would put a secret in the
// conversation.
var printers = regexp.MustCompile(`(^|[\s|;&(])(cat|less|more|head|tail|grep|sed|awk|bat|type|source|\.)\s+[^\n|;&]*\bloadout\.env\b`)

// TestHostSkillsNeverPrintTheEnvFile: no command in a host skill reads the
// settings file out loud.
func TestHostSkillsNeverPrintTheEnvFile(t *testing.T) {
	for _, name := range linted {
		files, err := host.Files(name)
		if err != nil {
			t.Fatal(err)
		}
		for p, data := range files {
			if !strings.HasSuffix(p, ".md") {
				continue
			}
			for _, code := range codeLines(string(data)) {
				if printers.MatchString(code) {
					t.Errorf("%s/%s runs %q, which prints the settings file", name, p, code)
				}
			}
		}
	}
}

// codeLines returns the lines of fenced code blocks and the inline code
// spans of markdown.
func codeLines(md string) []string {
	var out []string
	inFence := false
	for line := range strings.Lines(md) {
		line = strings.TrimRight(line, "\n")
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			inFence = !inFence
			continue
		}
		if inFence {
			out = append(out, line)
			continue
		}
		for _, m := range regexp.MustCompile("`([^`]+)`").FindAllStringSubmatch(line, -1) {
			out = append(out, m[1])
		}
	}
	return out
}

// linted are the host skills the text checks below apply to.
var linted = []string{"loadout-host-setup", "loadout-host-provision"}
