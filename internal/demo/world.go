package demo

import "time"

func d(n int) time.Duration { return time.Duration(n) * 24 * time.Hour }
func h(n int) time.Duration { return time.Duration(n) * time.Hour }

// The people. Each has a role in one or more places, see memberships.
const (
	alice  = "alice@example.com"
	bogdan = "bogdan@example.com"
	olga   = "olga@example.com"
	ada    = "ada@example.com"
	chen   = "chen@example.com"
	dana   = "dana@example.com"
	emil   = "emil@example.com"
)

// member is one membership, on the day the person joined.
type member struct {
	ago       time.Duration
	ns, email string
	role      string // owner, maintainer or member
}

var memberships = []member{
	{d(35), Acme, olga, "owner"},
	{d(35), Globex, chen, "owner"},
	{d(34), Lime, alice, "maintainer"},
	{d(34), Pear, dana, "maintainer"},
	{d(33), Koala, alice, "member"},
	{d(32), Otter, emil, "member"},
	{d(30), Pear, alice, "member"},
	{d(26), Lime, bogdan, "member"},
}

// release is one package published directly.
type release struct {
	ago time.Duration
	who string
	f   files
}

// proposal is one candidate. decide is "", "approve" or "reject", and the
// decision comes after later by by. report adds a comparison report.
type proposal struct {
	ago      time.Duration
	who      string
	f        files
	note     string
	prompts  []string
	decide   string
	by       string
	after    time.Duration
	why      string
	reportBy string
}

const (
	planDesc    = "Turns a ticket into an ordered plan and a spec, with files, risks and checks"
	devDesc     = "Implements a plan on a branch, one small commit at a time"
	reviewDesc  = "Adversarial review of a change against its spec"
	testDesc    = "Proves a reviewed change in a safe environment and puts everything back"
	notesDesc   = "Drafts release notes from merged pull requests"
	devLoopDesc = "Plan, dev, review and test a change, with a person at every gate."
)

var releases = []release{
	// acme: the project's own packages, then its templates.
	{d(33), olga, agent("acme/python-standards", "4.1.0", "balanced", rs, "House rules for Python across every acme team", "Follow the house rules.",
		"Type hints on every public function.", "One obvious way to do a thing, and no clever code.", "Tests next to the code they cover.")},
	{d(33), olga, agent("acme/docs-writer", "1.0.3", "balanced", rse, "Short user-facing docs with diagrams and links", "Write short docs.",
		"Say what it is, who it is for and the first command to run.", "Add a diagram when the order of steps matters.")},
	{d(29), olga, kb("acme/engineering-handbook", "1.0.0", "How every acme team plans, reviews and ships.", map[string]string{
		"README.md":            "# Engineering handbook\n\nThe rules every acme team shares. Teams add their own on top.\n",
		"review.md":            "# Review\n\nA change needs one reviewer who did not write it. Read the spec first and the diff second.\n",
		"diagrams/ship.mmd":    "flowchart LR\n  plan --> dev --> review --> test --> release\n",
		"release/checklist.md": "# Release checklist\n\n- Tests pass on the release branch.\n- The notes are written.\n- The rollback is known.\n",
	})},
	{d(26), olga, pipeline("acme/dev-loop", "1.0.0", devLoopDesc, devLoop("Turn the ticket into plan.md, and spec.md after a grill.", "Judge the change against the requirement."))},
	{d(24), olga, pipeline("acme/incident", "1.0.0", "Triage, mitigate, verify and write up an incident.", incidentTemplate)},
	{d(5), olga, pipeline("acme/dev-loop", "1.1.0", devLoopDesc, devLoop("Turn the ticket into plan.md, and spec.md after a grill. Name every file.", "Judge the change against the requirement."))},
	{d(3), olga, skill("acme/docs-style", "Keeps acme documents short and plain.", "Cut every sentence that does not help the reader act.", "Put the command before the explanation.", "Link the source instead of copying it.")},

	// lime: web.
	{d(30), alice, planner("2.2.0", "Plans stay short and general.")},
	{d(28), alice, reviewer("lime/code-review", "1.8.0")},
	{d(27), alice, agent("lime/scratch-notes", "0.2.0", "fast", rs, "A scratch agent from the first week", "Take notes while the team tries things out.")},
	{d(25), alice, dev("lime/builder", "1.0.0")},
	{d(23), alice, tester("lime/tester", "1.0.0")},
	{d(22), alice, pipeline("lime/dev-loop", "1.0.0", "The lime team's dev loop, on acme's template.", binding("acme/dev-loop@1",
		[][2]string{{"plan", "planner"}, {"dev", "builder"}, {"review", "code-review"}, {"test", "tester"}}, "from: dev, on: DONE"))},
	{d(20), alice, embeddedFiles("lime/pr-description@1.0.0")},
	{d(18), alice, embeddedFiles("lime/handbook@1.0.0")},
	{d(14), alice, embeddedFiles("lime/dev-course@1.0.0")},
	{d(6), alice, embeddedFiles("lime/handbook@1.1.0")},
	{d(4), alice, planner("2.3.0", "Plans stay short and general.", "Ask before guessing.")},
	{d(3), alice, embeddedFiles("lime/pr-description@1.0.1")},
	{d(3), alice, embeddedFiles("lime/dev-course@1.1.0")},
	{d(1), alice, reviewer("lime/code-review", "1.8.2", "Name the line of every finding.")},

	// koala: mobile.
	{d(31), olga, agent("koala/release-checklist", "0.3.0", "balanced", rs, "Walks a mobile release through the store checks", "Walk the release through the checklist.",
		"Version and build number are bumped.", "Release notes are in every store language.", "Screenshots match the new screens.", "The crash rate of the last build is below the team limit.")},
	{d(24), olga, agent("koala/triager", "1.0.0", "balanced", rss, "Assesses a mobile alert and says who is affected", "You triage an alert for the mobile apps.",
		"Find out which app version, platform and share of users are hit.", "Write triage.md with the symptom, the first bad build and the blast radius.", "Do not change anything.", "End with `Verdict: ASSESSED`.")},
	{d(24), olga, agent("koala/mitigator", "1.0.0", "balanced", rses, "Applies the safest step that stops the damage", "You stop an incident from growing.",
		"Prefer a flag flip or a staged rollout halt over a code change.", "Record every step in mitigate.md with the time.", "Stop and say ESCALATE when the safe step needs a person.", "End with `Verdict: MITIGATED` or `Verdict: ESCALATE`.")},
	{d(23), olga, agent("koala/verifier", "1.0.0", "balanced", rss, "Checks that a mitigation held", "You check that the service is stable again.",
		"Compare the signals in triage.md with what you see now.", "Wait for one full cycle of the slowest signal before you decide.", "End with `Verdict: STABLE` or `Verdict: NOT FIXED`.")},
	{d(23), olga, agent("koala/postmortem-writer", "1.0.0", "deep", rse, "Writes the timeline, causes and follow-ups of an incident", "You write the postmortem.",
		"Build the timeline from triage.md, mitigate.md and verify.md only.", "Name causes, never people.", "List follow-ups with an owner each.", "End with `Verdict: DONE`.")},
	{d(22), olga, pipeline("koala/incident", "1.0.0", "The koala team's incident loop, on acme's template.", binding("acme/incident@1",
		[][2]string{{"triage", "triager"}, {"mitigate", "mitigator"}, {"verify", "verifier"}, {"postmortem", "postmortem-writer"}}, "from: mitigate, on: MITIGATED"))},
	{d(16), olga, skill("koala/store-notes", "Writes app store release notes in every store language.", "List the user-visible changes since the last build.", "Write each in one line a customer understands.", "Keep each note under 170 characters.")},
	{d(12), olga, kb("koala/mobile-handbook", "1.0.0", "How the koala team ships the mobile apps.", map[string]string{
		"README.md":          "# Mobile handbook\n\nHow an app goes from a merged change to the stores.\n",
		"release/stores.md":  "# The stores\n\nA build goes to the beta track first. It moves up after a day without new crashes.\n",
		"diagrams/track.mmd": "flowchart LR\n  merge --> beta --> staged --> full\n",
	})},
	{d(5), olga, kb("koala/mobile-handbook", "1.1.0", "How the koala team ships the mobile apps.", map[string]string{
		"README.md":           "# Mobile handbook\n\nHow an app goes from a merged change to the stores.\n",
		"release/stores.md":   "# The stores\n\nA build goes to the beta track first. It moves up after a day without new crashes.\n",
		"release/rollback.md": "# Roll back\n\nHalt the staged rollout first. Then ship the previous build as a new version.\n",
		"diagrams/track.mmd":  "flowchart LR\n  merge --> beta --> staged --> full\n",
	})},
	{d(2), olga, agent("koala/triager", "1.1.0", "balanced", rss, "Assesses a mobile alert and says who is affected", "You triage an alert for the mobile apps.",
		"Find out which app version, platform and share of users are hit.", "Write triage.md with the symptom, the first bad build and the blast radius.", "Check the crash dashboard before you read the logs.", "Do not change anything.", "End with `Verdict: ASSESSED`.")},

	// globex: the second project.
	{d(33), chen, agent("globex/style-guide", "2.0.0", "balanced", rs, "The style rules every globex team writes to", "Apply the globex style guide.",
		"Names say what a thing is, not how it works.", "Comments explain why.", "A function that needs a long comment needs a better name.")},
	{d(31), chen, kb("globex/ways-of-working", "1.0.0", "How globex teams plan, hand over and decide.", map[string]string{
		"README.md":             "# Ways of working\n\nShort rules that every globex team follows.\n",
		"decisions.md":          "# Decisions\n\nWrite a decision down in one page with its reason. Link it from the ticket.\n",
		"diagrams/handover.mmd": "flowchart LR\n  author --> reviewer --> owner\n",
	})},
	{d(27), chen, pipeline("globex/dev-loop", "1.0.0", devLoopDesc, devLoop("Turn the ticket into plan.md, and spec.md after a grill. Cite the style guide.", "Judge the change against the requirement and the style guide."))},
	{d(25), chen, pipeline("globex/release", "1.0.0", "Prepare, verify, approve and announce a release.", releaseTemplate)},
	{d(20), chen, skill("globex/changelog", "Writes a changelog entry from a merged change.", "Read the change and its ticket.", "Write one line that says what a user can now do.", "File it under Added, Changed or Fixed.")},
	{d(3), chen, pipeline("globex/dev-loop", "1.1.0", devLoopDesc, devLoop("Turn the ticket into plan.md, and spec.md after a grill. Cite the style guide and name the risks.", "Judge the change against the requirement and the style guide."))},

	// pear: web.
	{d(29), dana, plannerFor("pear/planner", "1.0.0")},
	{d(29), dana, dev("pear/builder", "1.0.0")},
	{d(28), dana, reviewer("pear/reviewer", "1.1.0")},
	{d(28), dana, tester("pear/tester", "1.0.0")},
	{d(26), dana, pipeline("pear/dev-loop", "1.0.0", "The pear team's dev loop, on globex's template.", binding("globex/dev-loop@1",
		[][2]string{{"plan", "planner"}, {"dev", "builder"}, {"review", "reviewer"}, {"test", "tester"}}, ""))},
	{d(19), dana, kb("pear/runbooks", "1.0.0", "How the pear team deploys and handles incidents.", map[string]string{
		"README.md":           "# Runbooks\n\nWhat to do when a deploy or a service goes wrong.\n",
		"deploy.md":           "# Deploy\n\nMerge to main. The gate job builds, tests and deploys.\n",
		"diagrams/deploy.mmd": "sequenceDiagram\n  Dev->>Gate: merge\n  Gate->>Prod: deploy\n  Prod-->>Dev: healthy\n",
	})},
	{d(15), dana, skill("pear/commit-style", "Writes commit messages the way pear does.", "Summarise the change in one plain line.", "Add a body only when the reason is not obvious.")},
	{d(10), dana, kb("pear/onboarding-course", "1.0.0", "The pear dev loop in two short lessons.", map[string]string{
		"01-setup.md": "# Set up loadout\n\nInstall the CLI and sign in with `loadout login`.\n",
		"02-loop.md":  "# The dev loop\n\nStart a ticket with the planner. Review before you merge.\n",
	})},
	{d(4), dana, kb("pear/runbooks", "1.1.0", "How the pear team deploys and handles incidents.", map[string]string{
		"README.md":           "# Runbooks\n\nWhat to do when a deploy or a service goes wrong.\n",
		"deploy.md":           "# Deploy\n\nMerge to main. The gate job builds, tests and deploys.\n",
		"rollback.md":         "# Roll back\n\nRun the rollback job with the previous tag.\n",
		"diagrams/deploy.mmd": "sequenceDiagram\n  Dev->>Gate: merge\n  Gate->>Prod: deploy\n  Prod-->>Dev: healthy\n",
	})},
	{d(1), dana, reviewer("pear/reviewer", "1.2.0", "Check the style guide last.")},

	// otter: releases.
	{d(28), chen, agent("otter/preparer", "0.3.0", "balanced", rse, "Collects what goes into a release and writes the notes", "You prepare a release.",
		"List the merged changes since the last release.", "Write the notes from the change titles and the checklist.", "Write both into prepare.md.", "End with `Verdict: READY`.")},
	{d(28), chen, agent("otter/verifier", "0.2.0", "balanced", rss, "Runs the release checklist against the build", "You verify a release build.",
		"Run each checklist item and write down what you saw.", "A skipped item is a failure until someone says otherwise.", "End with `Verdict: PASS` or `Verdict: FAIL`.")},
	{d(27), chen, agent("otter/approver", "0.2.0", "deep", rs, "Judges a release against its checklist", "You decide whether a release goes out.",
		"Read prepare.md and verify.md before you decide.", "Reject when a failed item has no owner and no date.", "Never edit files.", "End with `Verdict: APPROVED` or `Verdict: REJECTED`.")},
	{d(27), chen, agent("otter/announcer", "0.1.0", "fast", rse, "Writes the announcement for a release", "You announce a release.",
		"Say what changed for the reader in the first line.", "Link the notes instead of copying them.", "End with `Verdict: SENT`.")},
	{d(24), chen, pipeline("otter/release", "1.0.0", "The otter team's release loop, on globex's template.", binding("globex/release@1",
		[][2]string{{"prepare", "preparer"}, {"verify", "verifier"}, {"approve", "approver"}, {"announce", "announcer"}}, "from: prepare, on: READY"))},
	{d(17), chen, skill("otter/release-notes-template", "Lays out the notes of a release.", "Open with the one change that matters most.", "Group the rest as Added, Changed and Fixed.")},
	{d(13), chen, kb("otter/release-guide", "1.0.0", "How the otter team cuts a release.", map[string]string{
		"README.md":         "# Release guide\n\nFrom a frozen branch to an announcement.\n",
		"steps.md":          "# Steps\n\nFreeze, prepare, verify, approve, announce.\n",
		"diagrams/flow.mmd": "flowchart LR\n  freeze --> prepare --> verify --> approve --> announce\n",
	})},
	{d(6), chen, kb("otter/release-guide", "1.1.0", "How the otter team cuts a release.", map[string]string{
		"README.md":         "# Release guide\n\nFrom a frozen branch to an announcement.\n",
		"steps.md":          "# Steps\n\nFreeze, prepare, verify, approve, announce.\n",
		"hotfix.md":         "# Hotfix\n\nA hotfix skips the freeze and keeps every other step.\n",
		"diagrams/flow.mmd": "flowchart LR\n  freeze --> prepare --> verify --> approve --> announce\n",
	})},
	{d(2), chen, agent("otter/preparer", "0.4.0", "balanced", rse, "Collects what goes into a release and writes the notes", "You prepare a release.",
		"List the merged changes since the last release.", "Write the notes from the change titles and the checklist.", "Flag a change that has no test.", "Write both into prepare.md.", "End with `Verdict: READY`.")},
}

// Proposals. Members propose and a maintainer or owner decides.
var proposals = []proposal{
	// lime
	{
		ago: d(24), who: bogdan, f: embeddedFiles("lime/release-notes@0.9.0"), note: "A first notes agent for the team.",
		prompts: []string{"Draft notes for the 2.4.0 release"}, decide: "approve", by: alice, after: d(1), why: "Good start. We can tune it as we use it.",
	},
	{
		ago: d(12), who: bogdan, f: dev("lime/builder", "1.1.0", "Rewrite files you touch for style."), note: "Let the builder tidy the files it touches.",
		prompts: []string{"Build the retry from TRK-1234 and tidy what you touch"}, decide: "reject", by: alice, after: d(1),
		why: "Rewriting files we did not ask about makes reviews harder. Keep the diff to the plan.",
	},
	{
		ago: d(5), who: bogdan, f: embeddedFiles("lime/pr-description@1.1.0"), note: "Add a test plan section.",
		prompts: []string{"Write the description for a change that adds a retry"},
	},
	{
		ago: h(20), who: bogdan, f: planner("2.4.0", "Ask before guessing.", "Name every file you would touch.", "End with one concrete test."),
		note: "Plans now name the exact files and end with a test step.", prompts: []string{"Plan TRK-1234: add a retry to the upload job", "Plan a database migration for the releases table"}, reportBy: alice,
	},
	// koala
	{
		ago: h(30), who: alice, f: agent("koala/triager", "1.2.0", "balanced", rss, "Assesses a mobile alert and says who is affected", "You triage an alert for the mobile apps.",
			"Find out which app version, platform and share of users are hit.", "Write triage.md with the symptom, the first bad build and the blast radius.", "Check the crash dashboard before you read the logs.", "Name the store track the bad build is on.", "Do not change anything.", "End with `Verdict: ASSESSED`."),
		note: "Say which store track the bad build is on.", prompts: []string{"Triage: crash rate doubled on Android 14 since build 412"},
	},
	// pear
	{
		ago: d(14), who: alice, f: tester("pear/tester", "1.1.0", "Run the slowest checks last."), note: "Order the checks so a quick failure comes first.",
		prompts: []string{"Test the retry change in the upload job"}, decide: "approve", by: dana, after: d(1), why: "Fine. Faster feedback.",
	},
	{
		ago: d(9), who: alice, f: dev("pear/builder", "1.1.0", "Open a draft pull request when you start."), note: "Open the pull request early.",
		prompts: []string{"Build the pagination change from the plan"}, decide: "reject", by: dana, after: d(2), why: "Draft pull requests triggered the full pipeline. Open it when the change is ready.",
	},
	{
		ago: d(2), who: alice, f: reviewer("pear/reviewer", "1.3.0", "Check the style guide last.", "Say when the diff is too big to review."),
		note: "Tell the author when a diff is too big.", prompts: []string{"Review a change that touches forty files", "Review a one-line fix"},
	},
	// otter
	{
		ago: d(11), who: emil, f: agent("otter/announcer", "0.2.0", "fast", rse, "Writes the announcement for a release", "You announce a release.",
			"Say what changed for the reader in the first line.", "Link the notes instead of copying them.", "Keep it under ten lines.", "End with `Verdict: SENT`."),
		note: "Keep announcements short.", prompts: []string{"Announce release 3.2.0"}, decide: "approve", by: chen, after: d(1), why: "Yes.",
	},
	{
		ago: d(1), who: emil, f: agent("otter/verifier", "0.3.0", "balanced", rss, "Runs the release checklist against the build", "You verify a release build.",
			"Run each checklist item and write down what you saw.", "A skipped item is a failure until someone says otherwise.", "Repeat a failed item once before you report it.", "End with `Verdict: PASS` or `Verdict: FAIL`."),
		note: "Repeat a failed check once.", prompts: []string{"Verify the 3.2.0 build", "Verify a build with one failing check"},
	},
}

func planner(version string, rules ...string) files {
	return plannerFor("lime/planner", version, rules...)
}

func plannerFor(id, version string, rules ...string) files {
	return plannerAgent(id, version, planDesc, rules...)
}

func dev(id, version string, rules ...string) files { return devAgent(id, version, devDesc, rules...) }

func reviewer(id, version string, rules ...string) files {
	return reviewAgent(id, version, reviewDesc, rules...)
}

func tester(id, version string, rules ...string) files {
	return testAgent(id, version, testDesc, rules...)
}

func embeddedFiles(ref string) files { return embedded(ref) }
