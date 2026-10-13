package cli

import (
	"fmt"
	"log/slog"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/petervlad/loadout/internal/builtin"
	"github.com/petervlad/loadout/internal/install"
)

// TestBuiltinSkillsNameRealCommands keeps loadout's own skills honest:
// every `loadout ...` command in their code spans and code blocks must be
// a command of this CLI, with flags that command has. In the everyday
// skills every --flag anywhere in the text must also exist on some
// command. The host skills show other tools' commands (hcloud, aws, ssh),
// whose flags are not loadout's, so there only the flags of `loadout`
// commands are checked; their files are Markdown, the assets are the
// deploy files that deploy's own tests cover.
func TestBuiltinSkillsNameRealCommands(t *testing.T) {
	root := commandTree()
	// The fewest commands each set must show, so a broken extraction is
	// noticed.
	least := map[string]int{builtin.SetEveryday: 50, builtin.SetHost: 30}
	for _, set := range builtin.Sets() {
		names, err := set.Names()
		if err != nil {
			t.Fatal(err)
		}
		checked := 0
		for _, name := range names {
			files, err := set.Files(name)
			if err != nil {
				t.Fatal(err)
			}
			for path, data := range files {
				if !strings.HasSuffix(path, ".md") {
					continue
				}
				where := name + "/" + path
				for _, line := range commandLines(string(data)) {
					checked++
					if err := checkCommand(root, line); err != nil {
						t.Errorf("%s: %q: %v", where, strings.Join(line, " "), err)
					}
				}
				if set.Name != builtin.SetEveryday {
					continue
				}
				for _, flag := range proseFlags(string(data)) {
					if !anyCommandHas(root, flag) && !otherToolFlag(string(data), flag) {
						t.Errorf("%s mentions --%s, which no loadout command has", where, flag)
					}
				}
			}
		}
		if checked < least[set.Name] {
			t.Errorf("checked only %d commands in the %s skills; is the extraction broken?", checked, set.Name)
		}
	}
}

// TestHonestyCheckCatchesMistakes: the check itself fails on made-up
// commands and flags.
func TestHonestyCheckCatchesMistakes(t *testing.T) {
	root := commandTree()
	text := "Run `loadout frobnicate`.\n\n```sh\nloadout review aprove 3f2a\nloadout install lime/x --yolo\nloadout member add --namespace lime --emial a@b\nloadout review approve|rejekt ID\n```\nThen pass --made-up.\n"
	var failed []string
	for _, line := range commandLines(text) {
		if checkCommand(root, line) != nil {
			failed = append(failed, strings.Join(line, " "))
		}
	}
	if len(failed) != 5 {
		t.Errorf("caught %d of 5 bad commands: %q", len(failed), failed)
	}
	if !otherToolFlag("Run `claude --agent lime-dev-loop`.", "agent") || otherToolFlag("Run `loadout install --agent x`.", "agent") || otherToolFlag("`claude --agent a` and `loadout install --agent x`", "agent") {
		t.Error("--agent must pass after claude or copilot only")
	}
	wrapped := "```sh\nsudo -u loadout loadout admin grant --email a@b --data /d\nssh admin@h sudo -u loadout loadout user pasword --email a@b\nsudo systemd-run --pipe --uid=loadout --property=EnvironmentFile=/etc/loadout/loadout.env /usr/local/bin/loadout mail test --nope x\nfly ssh console --pty -C \"/usr/local/bin/loadout admin grant --emial a@b\"\n```\n"
	lines := commandLines(wrapped)
	var bad []string
	for _, line := range lines {
		if checkCommand(root, line) != nil {
			bad = append(bad, strings.Join(line, " "))
		}
	}
	if len(lines) != 4 || len(bad) != 3 {
		t.Errorf("wrapped commands: found %d, caught %d of 3 bad ones: %q", len(lines), len(bad), bad)
	}
	if flags := proseFlags(text); !slices.Contains(flags, "made-up") || anyCommandHas(root, "made-up") {
		t.Errorf("prose flags = %q", flags)
	}
	good := "```sh\nloadout review lime/planner --safe --from 1.2.0\necho \"$T\" | loadout remote add r https://x --token-stdin\nloadout member list --namespace lime --remote office\nloadout team join < invite.txt\nloadout propose ./planner \\\n  --note \"a -- b\" \\\n  --prompt x\nloadout propose delete lime/planner --reason \"unused\"\nloadout review approve del-3f9a1c2b7d4e --as-owner --note why\n```\n"
	for _, line := range commandLines(good) {
		if err := checkCommand(root, line); err != nil {
			t.Errorf("%q: %v", line, err)
		}
	}
}

// otherToolFlags are flags of the AI tools that a skill tells the person to
// type, such as claude --agent NAME, which no loadout command has.
var otherToolFlags = []string{"agent"}

// otherToolFlag reports whether every use of --flag in md follows a
// claude or copilot command, so a loadout command cannot borrow the list.
func otherToolFlag(md, flag string) bool {
	if !slices.Contains(otherToolFlags, flag) {
		return false
	}
	uses := regexp.MustCompile(`(\S+)\s+--` + flag + `\b`)
	all := uses.FindAllStringSubmatch(md, -1)
	for _, m := range all {
		if !strings.Contains(m[0], "claude") && !strings.Contains(m[0], "copilot") {
			return false
		}
	}
	return len(all) > 0
}

func commandTree() *cobra.Command {
	log := slog.New(slog.DiscardHandler)
	return NewRoot(Deps{Installer: install.New(nil, log), Log: log})
}

var (
	inlineCode = regexp.MustCompile("`([^`]+)`")
	comment    = regexp.MustCompile(`\s#(\s.*)?$`)
	flagInText = regexp.MustCompile("(?:^|[\\s(\\[`])--([a-z][a-z0-9-]*)")
)

// commandLines returns the tokens of every `loadout ...` command in the
// Markdown's code blocks and inline code, starting at "loadout".
func commandLines(md string) [][]string {
	var code []string
	inFence, pending := false, ""
	for line := range strings.Lines(md) {
		line = strings.TrimRight(line, "\n")
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			inFence = !inFence
			continue
		}
		if !inFence {
			for _, m := range inlineCode.FindAllStringSubmatch(line, -1) {
				code = append(code, m[1])
			}
			continue
		}
		line = comment.ReplaceAllString(line, "")
		if cont, ok := strings.CutSuffix(strings.TrimSpace(line), "\\"); ok {
			pending += cont + " "
			continue
		}
		code = append(code, pending+line)
		pending = ""
	}
	var out [][]string
	for _, c := range code {
		var cmd []string
		for _, tok := range append(shellWords(c), ";") {
			if slices.Contains([]string{"|", "||", "&&", ";", "<", ">"}, tok) {
				if cmd = viaWrapper(cmd); len(cmd) > 0 && cmd[0] == "loadout" {
					out = append(out, cmd)
				}
				cmd = nil
				continue
			}
			cmd = append(cmd, tok)
		}
	}
	return out
}

// wrappers are the programs a skill runs loadout through: on the host over
// ssh, as the service user, with the service's environment, or in a
// platform's console.
var wrappers = []string{"sudo", "ssh", "systemd-run", "fly", "flyctl"}

// viaWrapper returns the loadout command a wrapped line runs, starting at
// "loadout" (a path ending in /bin/loadout counts, and so does one
// quoted as a single argument, as in fly ssh console -C "..."); any other
// line is returned as it is.
func viaWrapper(cmd []string) []string {
	if len(cmd) == 0 || !slices.Contains(wrappers, cmd[0]) {
		return cmd
	}
	var words []string
	for _, tok := range cmd {
		if strings.Contains(tok, " ") {
			words = append(words, shellWords(tok)...)
			continue
		}
		words = append(words, tok)
	}
	for i, w := range words {
		if slices.Contains([]string{"systemctl", "journalctl"}, w) {
			break // loadout after these is the service's name
		}
		if (w == "loadout" || strings.HasSuffix(w, "/bin/loadout")) && words[i-1] != "-u" {
			return append([]string{"loadout"}, words[i+1:]...)
		}
	}
	return cmd
}

// shellWords splits s at spaces outside quotes and drops the quotes.
func shellWords(s string) []string {
	var out []string
	var cur strings.Builder
	var quote rune
	started := false
	for _, r := range s {
		switch {
		case quote != 0 && r == quote:
			quote = 0
		case quote != 0:
			cur.WriteRune(r)
		case r == '"' || r == '\'':
			quote, started = r, true
		case r == ' ' || r == '\t':
			if started {
				out = append(out, cur.String())
				cur.Reset()
				started = false
			}
		default:
			cur.WriteRune(r)
			started = true
		}
	}
	if started {
		out = append(out, cur.String())
	}
	return out
}

var word = regexp.MustCompile(`^[a-z][a-z-]*$`)

// checkCommand walks the command path of a `loadout ...` line and checks
// its flags on the command it reaches.
func checkCommand(root *cobra.Command, toks []string) error {
	c, i := root, 1
	for ; i < len(toks); i++ {
		tok := strings.Trim(toks[i], "[]")
		if strings.HasPrefix(tok, "-") || !c.HasSubCommands() {
			break
		}
		alts := strings.Split(tok, "|")
		sub := subcommand(c, alts[0])
		if sub == nil {
			if word.MatchString(tok) || i == 1 {
				return fmt.Errorf("%s has no command %q", c.CommandPath(), tok)
			}
			break // an argument, such as TEAM/NAME
		}
		for _, a := range alts[1:] {
			if subcommand(c, a) == nil {
				return fmt.Errorf("%s has no command %q", c.CommandPath(), a)
			}
		}
		c = sub
	}
	for _, tok := range toks[i:] {
		tok = strings.Trim(tok, "[]")
		name, ok := strings.CutPrefix(tok, "--")
		if !ok || name == "" {
			if len(tok) == 2 && tok[0] == '-' && c.Flags().ShorthandLookup(tok[1:]) == nil && c.InheritedFlags().ShorthandLookup(tok[1:]) == nil {
				return fmt.Errorf("%s has no flag %s", c.CommandPath(), tok)
			}
			continue
		}
		name, _, _ = strings.Cut(name, "=")
		if c.Flag(name) == nil {
			return fmt.Errorf("%s has no flag --%s", c.CommandPath(), name)
		}
	}
	return nil
}

func subcommand(c *cobra.Command, name string) *cobra.Command {
	for _, s := range c.Commands() {
		if s.Name() == name || slices.Contains(s.Aliases, name) {
			return s
		}
	}
	return nil
}

// proseFlags returns every --flag named anywhere in the text.
func proseFlags(md string) []string {
	var out []string
	for _, m := range flagInText.FindAllStringSubmatch(md, -1) {
		if !slices.Contains(out, m[1]) {
			out = append(out, m[1])
		}
	}
	return out
}

func anyCommandHas(c *cobra.Command, flag string) bool {
	found := false
	visit := func(f *pflag.Flag) { found = found || f.Name == flag }
	c.LocalFlags().VisitAll(visit)
	c.PersistentFlags().VisitAll(visit)
	for _, s := range c.Commands() {
		found = found || anyCommandHas(s, flag)
	}
	return found
}
