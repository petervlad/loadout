package demo

import (
	"io/fs"
	"path"
	"strings"
)

// embedded returns a package folder kept under content/, such as
// "lime/handbook@1.0.0". It panics on a missing folder: the folders ship in
// the binary, and a test reads every one.
func embedded(ref string) files {
	dir := path.Join("content", ref)
	out := files{}
	err := fs.WalkDir(content, dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := content.ReadFile(p)
		out[strings.TrimPrefix(p, dir+"/")] = data
		return err
	})
	if err != nil {
		panic("demo package " + ref + ": " + err.Error())
	}
	return out
}

func embeddedText(name string) string {
	data, err := content.ReadFile(path.Join("content", name))
	if err != nil {
		panic("demo text " + name + ": " + err.Error())
	}
	return string(data)
}

const (
	rs   = "read, search"
	rse  = "read, search, edit"
	rses = "read, search, edit, shell"
	rss  = "read, search, shell"
)

// Pipeline templates. The dev loop is the team's own shape: plan, dev,
// review and test, with gates, a review loop capped at two rounds, a way
// back to plan and a test failure that goes back to dev.
func devLoop(planBrief, reviewBrief string) string {
	t := embeddedText("dev-loop.yaml")
	t = strings.Replace(t, "Turn the ticket into plan.md, and spec.md after a grill.", planBrief, 1)
	return strings.Replace(t, "Judge the change against the requirement.", reviewBrief, 1)
}

const incidentTemplate = `start: triage
slots:
  triage:
    brief: Assess the alert and write what is broken and who is affected into triage.md.
    writes: triage.md
    reads: [ticket]
    verdicts: [ASSESSED]
  mitigate:
    brief: Apply the safest step that stops the damage and record it in mitigate.md.
    writes: mitigate.md
    reads: [triage.md, ticket]
    verdicts: [MITIGATED, ESCALATE]
  verify:
    brief: Check the service against the signals in triage.md and say whether it is stable.
    writes: verify.md
    reads: [triage.md, mitigate.md]
    verdicts: [STABLE, NOT FIXED]
  postmortem:
    brief: Write the timeline, the causes and the follow-ups into postmortem.md.
    writes: postmortem.md
    reads: [triage.md, mitigate.md, verify.md]
    verdicts: [DONE]
edges:
  - {from: triage,     on: ASSESSED,  to: mitigate,   via: gate}
  - {from: mitigate,   on: MITIGATED, to: verify,     via: auto}
  - {from: mitigate,   on: ESCALATE,  to: end,        via: gate}
  - {from: verify,     on: STABLE,    to: postmortem, via: gate}
  - {from: verify,     on: NOT FIXED, to: mitigate,   via: gate, max_rounds: 3}
  - {from: postmortem, on: DONE,      to: end,        via: gate}
`

const releaseTemplate = `start: prepare
slots:
  prepare:
    brief: Collect what goes into the release and write the notes and the checklist into prepare.md.
    writes: prepare.md
    reads: [ticket]
    verdicts: [READY]
  verify:
    brief: Run the checklist against the build and write what passed and failed into verify.md.
    writes: verify.md
    reads: [prepare.md]
    verdicts: [PASS, FAIL]
  approve:
    brief: Judge the release against its checklist and write the decision into approve.md.
    writes: approve.md
    reads: [prepare.md, verify.md]
    verdicts: [APPROVED, REJECTED]
  announce:
    brief: Write the announcement for the people who need to know into announce.md.
    writes: announce.md
    reads: [prepare.md, approve.md]
    verdicts: [SENT]
edges:
  - {from: prepare,  on: READY,    to: verify,   via: auto}
  - {from: verify,   on: PASS,     to: approve,  via: gate}
  - {from: verify,   on: FAIL,     to: prepare,  via: gate, max_rounds: 2}
  - {from: approve,  on: APPROVED, to: announce, via: gate}
  - {from: approve,  on: REJECTED, to: end,      via: gate}
  - {from: announce, on: SENT,     to: end,      via: gate}
`

// binding is a team's pipeline: one of its agents for each slot.
func binding(uses string, bind [][2]string, gate string) string {
	var b strings.Builder
	b.WriteString("uses: " + uses + "\nbind:\n")
	for _, s := range bind {
		b.WriteString("  " + s[0] + ": " + s[1] + "\n")
	}
	b.WriteString("work_folder: explorations/\n")
	if gate != "" {
		b.WriteString("gate:\n  - {" + gate + "}\n")
	}
	return b.String()
}

// The agents of the dev loop are written once and shaped per team.
func plannerAgent(id, version, desc string, extra ...string) files {
	rules := append([]string{
		"Read the ticket and the code it touches before you write anything.",
		"Write plan.md with the files you would change, the risks and one test.",
		"Grill the requester on anything unclear, then write what was decided into spec.md.",
		"End with the line `Verdict: READY`.",
	}, extra...)
	return agent(id, version, "deep", rses, desc, "You are the team planner. You turn a ticket into a plan and a spec.", rules...)
}

func devAgent(id, version, desc string, extra ...string) files {
	rules := append([]string{
		"Work on a branch and keep each commit small and named in plain words.",
		"Write the test first, then the change that makes it pass.",
		"Stop and say so when the plan does not fit the code.",
		"Write what you did into dev.md and end with `Verdict: DONE` or `Verdict: SPEC CONFLICT`.",
	}, extra...)
	return agent(id, version, "balanced", rses, desc, "You implement what the plan and spec say.", rules...)
}

func reviewAgent(id, version, desc string, extra ...string) files {
	rules := append([]string{
		"Judge the change against the spec and the plan, not against your taste.",
		"Start with the riskiest file and name the line of every finding.",
		"Say what is wrong, then what to change.",
		"Never edit files.",
		"End with exactly one of `Verdict: APPROVE`, `CHANGES REQUESTED`, `REPLAN` or `BLOCKED`.",
	}, extra...)
	return agent(id, version, "deep", rs, desc, "You are an adversarial reviewer.", rules...)
}

func testAgent(id, version, desc string, extra ...string) files {
	rules := append([]string{
		"Prove the change in a safe environment and never in production.",
		"Write down every step you ran and what you saw into test.md.",
		"Restore everything you touched, the data and the settings included.",
		"End with `Verdict: PASS`, `FAIL` or `INCONCLUSIVE`.",
	}, extra...)
	return agent(id, version, "balanced", rss, desc, "You test a reviewed change.", rules...)
}
