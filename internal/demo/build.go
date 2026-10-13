package demo

import (
	"fmt"
	"strings"
)

// files is a package folder: path to text.
type files map[string][]byte

func manifest(id, kind, version, desc string) []byte {
	return []byte("name: " + id + "\nkind: " + kind + "\nversion: " + version + "\ndescription: " + desc + "\n")
}

// agent is an agent package. rules become the bullets of its prompt, after
// the opening line.
func agent(id, version, model, tools, desc, intro string, rules ...string) files {
	name := id[strings.Index(id, "/")+1:]
	var b strings.Builder
	fmt.Fprintf(&b, "---\nname: %s\ndescription: %s\nmodel: %s\ntools: [%s]\n---\n%s\n", name, desc, model, tools, intro)
	if len(rules) > 0 {
		b.WriteString("\n")
	}
	for _, r := range rules {
		b.WriteString("- " + r + "\n")
	}
	return files{"loadout.yaml": manifest(id, "agent", version, desc), "agent.md": []byte(b.String())}
}

// skill is a skill package with one reference page.
func skill(id, desc string, steps ...string) files {
	name := id[strings.Index(id, "/")+1:]
	var b strings.Builder
	fmt.Fprintf(&b, "---\nname: %s\ndescription: %s Use when asked for it by name.\n---\n# %s\n\n", name, desc, name)
	for i, s := range steps {
		fmt.Fprintf(&b, "%d. %s\n", i+1, s)
	}
	return files{
		"loadout.yaml":         manifest(id, "skill", "1.0.0", desc),
		"SKILL.md":             []byte(b.String()),
		"references/format.md": []byte("# Format\n\nPlain words, one idea per line.\n"),
	}
}

// kb is a knowledge base of documents, path to text.
func kb(id, version, desc string, docs map[string]string) files {
	f := files{"loadout.yaml": manifest(id, "kb", version, desc)}
	for p, t := range docs {
		f[p] = []byte(t)
	}
	return f
}

// pipeline is a template or a binding, whose body is the pipeline.yaml.
func pipeline(id, version, desc, body string) files {
	return files{"loadout.yaml": manifest(id, "pipeline", version, desc), "pipeline.yaml": []byte(body)}
}
