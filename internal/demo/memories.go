package demo

import (
	"time"

	"github.com/petervlad/loadout/internal/memory"
)

const (
	fb = memory.TypeFeedback
	pj = memory.TypeProject
	rf = memory.TypeReference
)

// note is one team memory entry. links name earlier entries by key.
type note struct {
	key, ns, who string
	ago          time.Duration
	typ          memory.Type
	name, desc   string
	body         string
	links        []string
}

// later changes to an entry: an edit by someone, a retraction, a move up to
// the project.
type edit struct {
	key, who, body, why string
	ago                 time.Duration
}

type retraction struct {
	key, who, why string
	ago           time.Duration
}

type move struct {
	key, who string
	ago      time.Duration
}

// onSwitch turns a namespace's memory on.
type onSwitch struct {
	ns, who string
	ago     time.Duration
}

var switches = []onSwitch{
	{Acme, olga, d(25)},
	{Lime, alice, d(25)},
	{Koala, olga, d(24)},
	{Globex, chen, d(25)},
	{Pear, dana, d(25)},
	{Otter, chen, d(24)},
}

var notes = []note{
	// acme
	{
		"a-python", Acme, olga, d(20), rf, "Python house rules", "The rules every acme team follows for Python.",
		"The rules live in the acme/python-standards agent. Type hints on public functions, tests next to the code, no clever code.", nil,
	},
	{
		"a-review", Acme, olga, d(15), fb, "One reviewer who did not write it", "Every change gets a review from a second person.",
		"A change needs one reviewer who did not write it. Read the spec first and the diff second.\n\n**Why:** the author reads what they meant, not what is there.", nil,
	},
	{
		"a-incident", Acme, olga, d(6), pj, "Incidents use the acme incident pipeline", "Start incidents from the incident conductor so the record is the same every time.",
		"Teams bind acme/incident to their own agents. Triage, mitigate and verify leave files in the run folder, and the postmortem is written from them.", nil,
	},
	{
		"a-freeze", Acme, olga, d(3), pj, "Code freeze starts on 20 December", "Every team stops releasing on the same day.",
		"No team releases between 20 December and 5 January. Plan the last release for the week before.", nil,
	},
	{
		"a-handbook", Acme, olga, d(2), rf, "The engineering handbook has the review rules", "Read the handbook before your first review.",
		"The acme/engineering-handbook knowledge base has the review rules and the release checklist.",
		[]string{"a-review"},
	},

	// lime
	{
		"l-files", Lime, alice, d(24), fb, "Name the files in every plan", "Plans that list their files get reviewed faster.",
		"The planner names each file it would touch and ends with one test.\n\n**Why:** reviews stalled twice on plans that only described the idea.\n\n**How to apply:** ask for the file list before you read the rest.", nil,
	},
	{
		"l-train", Lime, alice, d(20), pj, "Release train leaves every second Thursday", "What goes out on a release day and who decides.",
		"A release leaves the gate job every second Thursday at 10:00. Alice decides what goes in on Wednesday.\n\n**Why:** support wants a fixed day to plan around.", nil,
	},
	{
		"l-staging", Lime, bogdan, d(14), rf, "Where to look when staging is slow", "The first panel to open on the staging dashboard.",
		"The staging dashboard is linked from the wiki under Deploys. Open the queue depth panel first. A queue over 200 means the worker pool is stuck, not the database.", nil,
	},
	{
		"l-oldflag", Lime, bogdan, d(12), pj, "Feature flag new-upload is on for staging only", "Which environments have the new upload path.",
		"The flag new-upload is on in staging and off everywhere else.", nil,
	},
	{
		"l-commits", Lime, bogdan, d(9), fb, "Keep commits small", "One idea per commit, named in plain words.",
		"Reviewers read commit by commit. A commit that mixes a rename with a fix is read twice.\n\n**How to apply:** commit the rename on its own.", nil,
	},
	{
		"l-freeze", Lime, alice, d(8), pj, "Deploys freeze on Fridays after 14:00", "No deploys late on Friday.",
		"Nothing is deployed after 14:00 on a Friday unless it fixes an outage.\n\n**Why:** nobody wants to watch a deploy on a weekend.", nil,
	},
	{
		"l-sunday", Lime, bogdan, d(6), pj, "The staging database resets every Sunday night", "Do not keep test data in staging for longer than a week.",
		"Staging is rebuilt from a snapshot every Sunday at 23:00. Test data you need later belongs in a fixture.",
		[]string{"l-staging"},
	},
	{
		"l-tables", Lime, alice, d(6), fb, "Prefer table-driven tests", "A table of cases reads better than copies of one test.",
		"Write one test with a table of cases and name each case. A new case is then one line.", nil,
	},
	{
		"l-flaky", Lime, bogdan, d(5), rf, "The flaky upload test is a known timeout", "Rerun it once before you dig.",
		"The upload test times out about one run in twenty on the shared runners. Rerun it once. If it fails twice, it is real.", nil,
	},
	{
		"l-router", Lime, alice, d(3), pj, "The web app moves to the new router in November", "New routes use the new router from now on.",
		"From now on new web routes go on the new router. Old routes move when someone touches them, and the old router goes away after November.", nil,
	},
	{
		"l-e2e", Lime, bogdan, d(2), fb, "Do not run the whole e2e suite on every push", "Run it on pull requests and before a release.",
		"The suite takes 25 minutes. Pushes run the fast tests and the pull request runs everything.\n\n**Why:** the queue was full by noon.",
		[]string{"l-flaky"},
	},
	{
		"l-handbook", Lime, alice, d(1), rf, "The handbook has the deploy steps", "Read the handbook package before you deploy or roll back.",
		"The lime/handbook knowledge base has the deploy steps, the rollback and a page for the slow database. Install it with the rest of the team's packages.",
		[]string{"l-freeze"},
	},
	{
		"l-notes", Lime, alice, d(1), fb, "Release notes come from merged PR titles", "Write the pull request title as the line the notes will use.",
		"The release-notes agent builds the notes from merged pull request titles. A title that says what a user can now do needs no editing.", nil,
	},

	// koala
	{
		"k-beta", Koala, alice, d(22), pj, "Builds go to the beta track first", "Nothing reaches the stores without a day on beta.",
		"A build goes to the beta track first. It moves to a staged rollout after a day without new crashes.", nil,
	},
	{
		"k-crash", Koala, olga, d(18), rf, "Where the crash numbers live", "Open the crash dashboard before the logs.",
		"The crash dashboard is linked from the mobile handbook. Compare the crash-free rate of the new build with the last one.", nil,
	},
	{
		"k-screens", Koala, alice, d(16), fb, "Retake the screenshots after a layout change", "Store screenshots must match the app.",
		"A layout change makes the store screenshots wrong. Retake them in the same pull request.", nil,
	},
	{
		"k-langs", Koala, olga, d(14), rf, "Store notes need all nine languages", "A missing language blocks the submission.",
		"The stores reject a release without notes in every language the app lists. The store-notes skill writes the first draft.", nil,
	},
	{
		"k-halt", Koala, olga, d(12), fb, "Halt the staged rollout before you debug", "Stop the damage first.",
		"When a crash spikes, halt the staged rollout first and look at the cause second.",
		[]string{"k-beta"},
	},
	{
		"k-signing", Koala, alice, d(10), rf, "Signing keys live in the team vault", "Never copy a signing key to a laptop.",
		"Release builds are signed by the build service. The keys stay in the team vault and nobody downloads them.", nil,
	},
	{
		"k-android", Koala, olga, d(6), pj, "Android 14 is the oldest version we test", "Older versions are best effort.",
		"The test matrix starts at Android 14 and iOS 17. Bugs on older versions are triaged but not blocking.", nil,
	},
	{
		"k-flag", Koala, alice, d(5), fb, "Ship risky changes behind a flag", "A flag turns a bad release into a settings change.",
		"A change to sign-in or payments ships behind a remote flag, off at first.",
		[]string{"k-halt"},
	},
	{
		"k-review", Koala, olga, d(4), pj, "Store review takes one to three days", "Plan releases around the review time.",
		"Submit on Monday for a release at the end of the week.", nil,
	},
	{
		"k-battery", Koala, alice, d(3), fb, "Check battery use on a real phone", "The simulator hides it.",
		"A background task that looks cheap on the simulator can drain a real phone. Test on a device.",
		[]string{"k-android"},
	},
	{
		"k-timeouts", Koala, olga, d(2), rf, "Network timeouts are ten seconds", "The apps give up after ten seconds.",
		"Every request has a ten second timeout. A screen must show a retry, not a spinner, after that.", nil,
	},
	{
		"k-notes", Koala, alice, d(1), fb, "Release notes come from merged PR titles", "One line per change a customer notices.",
		"Notes come from merged pull request titles. Leave out changes a customer cannot see.",
		[]string{"k-langs"},
	},

	// globex
	{
		"g-style", Globex, chen, d(22), rf, "The style guide is an agent", "Ask the style-guide agent before you argue about names.",
		"The globex/style-guide agent holds the rules. Review against it and not against taste.", nil,
	},
	{
		"g-decisions", Globex, chen, d(15), fb, "Write a decision down on one page", "A decision without a page is forgotten in a month.",
		"Write the decision, the reason and what was rejected on one page. Link it from the ticket.", nil,
	},
	{
		"g-quarter", Globex, chen, d(5), pj, "Planning day is the first Monday of the quarter", "Teams bring their plan for the next three months.",
		"Each team brings one page. The page lists what it will ship and what it will not.", nil,
	},
	{
		"g-handover", Globex, chen, d(3), rf, "Handover notes follow the decision template", "Use the same page for a handover.",
		"A handover is a decision page with a list of open questions.",
		[]string{"g-decisions"},
	},
	{
		"g-freeze", Globex, chen, d(1), pj, "No releases in the last week of the year", "Every team pauses.",
		"Nothing is released between 24 December and 2 January.", nil,
	},

	// pear
	{
		"p-lint", Pear, dana, d(24), fb, "Run the linter before you push", "A failing lint job costs everyone a queue slot.",
		"Run the linter locally. The pipeline lints again, and a failure there wastes ten minutes of the shared runners.", nil,
	},
	{
		"p-deploy", Pear, dana, d(18), pj, "Deploys freeze on Fridays after 14:00", "No deploys late on Friday.",
		"Nothing is deployed after 14:00 on a Friday unless it fixes an outage.", nil,
	},
	{
		"p-db", Pear, alice, d(12), pj, "The staging database resets every Sunday night", "Do not keep test data in staging.",
		"Staging is rebuilt from a snapshot every Sunday at 23:00.", nil,
	},
	{
		"p-oldrule", Pear, dana, d(9), fb, "Squash every pull request", "Keep the history flat.",
		"Squash when merging.", nil,
	},
	{
		"p-tables", Pear, dana, d(10), fb, "Prefer table-driven tests", "A table of cases reads better than copies of one test.",
		"Write one test with a table of cases and name each case.", nil,
	},
	{
		"p-cache", Pear, alice, d(8), rf, "Clear the build cache when a test passes only on your machine", "Stale caches hide missing files.",
		"If a test passes locally and fails in the pipeline, clear the local build cache and run it again.", nil,
	},
	{
		"p-flaky", Pear, dana, d(6), rf, "The flaky upload test is a known timeout", "Rerun it once before you dig.",
		"The upload test times out now and then on the shared runners. Rerun once. If it fails twice, it is real.", nil,
	},
	{
		"p-flags", Pear, alice, d(5), pj, "New screens ship behind a flag", "Turn a flag on for the team first.",
		"A new screen is behind a flag that is on for the team only. It is turned on for everyone a week later.", nil,
	},
	{
		"p-review", Pear, dana, d(4), fb, "Reviews finish within one working day", "A waiting review blocks the next ticket.",
		"Review within one working day, and say so in the channel when you cannot.",
		[]string{"p-lint"},
	},
	{
		"p-slow", Pear, alice, d(3), rf, "The slow query dashboard", "Open it when a page feels slow.",
		"The slow query dashboard is linked from the runbooks. Sort by total time, not by the slowest single query.", nil,
	},
	{
		"p-notes", Pear, alice, d(2), fb, "Release notes come from merged PR titles", "Write the title as the note.",
		"The notes are built from merged pull request titles.", nil,
	},
	{
		"p-oncall", Pear, dana, d(1), rf, "The on-call handover is in the runbooks", "Read it at the start of your week.",
		"The runbooks have the handover page. Read it, then post that you are on call.",
		[]string{"p-deploy"},
	},

	// otter
	{
		"o-freeze", Otter, emil, d(20), pj, "Release freeze starts two days before the release", "No new changes after the freeze.",
		"Two days before a release the branch is frozen. Only fixes for the checklist go in.", nil,
	},
	{
		"o-checklist", Otter, chen, d(18), rf, "The release checklist lives in the guide", "Run it from the guide, not from memory.",
		"The otter/release-guide knowledge base has the checklist. Verify runs it item by item.", nil,
	},
	{
		"o-notes", Otter, emil, d(14), fb, "Release notes come from merged PR titles", "Write the title as the note.",
		"Notes are built from merged pull request titles. Fix the title, not the note.", nil,
	},
	{
		"o-rollback", Otter, chen, d(11), fb, "Decide the rollback before you ship", "A release without a rollback does not go out.",
		"The approver rejects a release whose rollback is not written down.", nil,
	},
	{
		"o-hotfix", Otter, emil, d(6), pj, "A hotfix skips the freeze", "Every other step stays.",
		"A hotfix skips the freeze and keeps prepare, verify, approve and announce.",
		[]string{"o-freeze"},
	},
	{
		"o-announce", Otter, chen, d(5), fb, "Announce in the channel first", "People hear about it where they work.",
		"Post the announcement in the team channel before you send mail.",
		[]string{"o-notes"},
	},
	{
		"o-verify", Otter, emil, d(4), rf, "Verify runs on the staging build", "Never verify on production.",
		"The verifier runs the checklist against the staging build of the release candidate.",
		[]string{"o-checklist"},
	},
	{
		"o-owner", Otter, chen, d(3), pj, "The approver changes every month", "Whoever approved last month does not approve this one.",
		"The approver rotates monthly. The rota is in the team calendar.", nil,
	},
	{
		"o-slack", Otter, emil, d(2), rf, "Release questions go in the release channel", "Keep them out of private messages.",
		"Questions about a release go in the release channel so the answer helps the next person.", nil,
	},
	{
		"o-flaky", Otter, emil, d(1), fb, "Repeat a failed check once before you report it", "A single failure may be the runner.",
		"Repeat a failed checklist item once. Report it only if it fails again.", nil,
	},
	{
		"o-sign", Otter, chen, d(1), pj, "Releases are signed by the build service", "Nobody signs a release by hand.",
		"The build service signs every release. A release that is not signed is not announced.", nil,
	},
}

var edits = []edit{
	{"l-flaky", alice, "The upload test times out about one run in twenty on the shared runners. Rerun it once. If it fails twice, it is real. A fix is planned for the next quarter.", "Add when it is fixed", d(2)},
	{"k-review", alice, "Submit on Monday for a release at the end of the week. A rejected build adds another two days.", "A rejection costs time too", d(1)},
	{"p-flaky", alice, "The upload test times out now and then on the shared runners. Rerun once. If it fails twice, it is real. The same test is flaky in lime.", "Same in lime", d(2)},
	{"o-checklist", emil, "The otter/release-guide knowledge base has the checklist. Verify runs it item by item. Add a line to the guide when you add a check.", "Say where to add checks", d(2)},
}

var retractions = []retraction{
	{"l-oldflag", bogdan, "The flag was removed", d(4)},
	{"p-oldrule", dana, "Merge commits are fine now", d(3)},
}

var moves = []move{
	{"l-tables", alice, d(2)},
	{"p-tables", dana, d(3)},
}
