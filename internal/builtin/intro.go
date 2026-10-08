package builtin

// Intro presents a built-in skill to someone who knows little about AI
// tools, on the registry's Start here page: what they get from it, in
// plain words, and things they can type to their AI tool. The skill's own
// description is written for the AI tool, not for them.
type Intro struct {
	// Title says what the person gets: "Set up loadout".
	Title string
	// Summary is one or two short sentences.
	Summary string
	// Asks are examples of what to type to the AI tool.
	Asks []string
}

// intros follow the order above: set up, write, review, run a team.
var intros = map[string]Intro{
	"loadout-setup": {
		Title:   "Set up loadout",
		Summary: "Your AI tool installs your team's packages, keeps them up to date and helps when a loadout command fails.",
		Asks:    []string{"Install my team's packages", "Why did loadout sync fail?"},
	},
	"loadout-author": {
		Title:   "Share your team's helpers",
		Summary: "Turn how your team works into an agent, a skill or a knowledge base. Try it, then publish it or propose it to your team.",
		Asks:    []string{"Make a skill from our release checklist", "Propose my change to the planner"},
	},
	"loadout-review": {
		Title:   "Review a teammate's change",
		Summary: "Run the current and the proposed version side by side in your own AI tool, read both answers, then approve or reject.",
		Asks:    []string{"Review the new version of the planner", "What does this review report say?"},
	},
	"loadout-admin": {
		Title:   "Run a team",
		Summary: "Add and remove people, change roles, ask for review before release and choose the models your team's agents use.",
		Asks:    []string{"Invite maria@example.com to my team", "Turn on review for my team"},
	},
}

// IntroOf returns the plain-words intro of the built-in skill name.
func IntroOf(name string) (Intro, bool) {
	i, ok := intros[name]
	return i, ok
}
