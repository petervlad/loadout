package memimport

import "regexp"

// rule is one row of the pre-selection table. A row that matches either
// selects the entry (by kind) or holds it back (a personal sign), and says why.
type rule struct {
	why     string
	matches func(Entry) bool
	selects bool
	holds   bool
}

func kindIs(k Kind) func(Entry) bool { return func(e Entry) bool { return e.Kind == k } }

func textMatches(expr string) func(Entry) bool {
	re := regexp.MustCompile(expr)
	return func(e Entry) bool {
		return re.MatchString(e.Name) || re.MatchString(e.Description) || re.MatchString(e.Body)
	}
}

// rules are all tried. An entry is pre-selected when a selecting row matches
// and no holding row does. Holding rows exist because a memory that speaks of
// one person is rarely a fact for the team.
var rules = []rule{
	{why: "A reference points to where something lives, which a team can use.", matches: kindIs(KindReference), selects: true},
	{why: "A project fact is shared work, which a team can use.", matches: kindIs(KindProject), selects: true},
	{why: "Feedback is often about one person's taste, so you choose it.", matches: kindIs(KindFeedback)},
	{why: "It speaks in the first person, so it is about you.", matches: textMatches(`\bI(?:'m|'ve|'d|'ll)?\b`), holds: true},
	{why: "It says \"my\", so it is about you.", matches: textMatches(`(?i)\bmy\b`), holds: true},
	{why: "It says what \"the user prefers\", which is a personal taste.", matches: textMatches(`(?i)\bthe user (?:prefers|likes|wants|dislikes)\b`), holds: true},
	{why: "It holds an email address, which names a person.", matches: textMatches(`[A-Za-z0-9._%+-]+@[A-Za-z0-9-]+(?:\.[A-Za-z0-9-]+)+`), holds: true},
}

// preselect applies the table. The reasons list every row that matched, so
// the person sees what decided the default.
func preselect(e Entry) (bool, []string) {
	selected, held := false, false
	var why []string
	for _, r := range rules {
		if !r.matches(e) {
			continue
		}
		why = append(why, r.why)
		selected = selected || r.selects
		held = held || r.holds
	}
	return selected && !held, why
}
