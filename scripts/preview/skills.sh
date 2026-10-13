#!/bin/bash
set -euo pipefail
S=${LOADOUT_PREVIEW_DIR:-${TMPDIR:-/tmp}/loadout-preview}
L=$S/loadout; export HOME=$S/home
P=$S/pk; rm -rf $P/release-notes-skill $P/runbooks
cd $P
$L new skill lime/release-notes-skill >/dev/null
cat > release-notes-skill/loadout.yaml <<Y
name: lime/release-notes-skill
kind: skill
version: 1.0.0
description: Writes release notes from merged pull requests
Y
cat > release-notes-skill/SKILL.md <<Y
---
name: release-notes-skill
description: Write release notes from the pull requests merged since the last tag. Use when asked for release notes or a changelog.
---
# Release notes

1. List the PRs merged since the last tag.
2. Group them as Features, Fixes and Internal (see references/style.md).
3. One line per PR, with its ticket.
Y
printf '# Style\n\nPlain words. No marketing.\n' > release-notes-skill/references/style.md
rm -f release-notes-skill/references/README.md
printf '# Template\n\n## Features\n\n## Fixes\n' > release-notes-skill/references/template.md
$L publish release-notes-skill --remote lime >/dev/null
$L new kb lime/runbooks >/dev/null
cat > runbooks/loadout.yaml <<Y
name: lime/runbooks
kind: kb
version: 1.0.0
description: How the lime team deploys, rolls back and handles incidents
Y
rm runbooks/README.md; mkdir -p runbooks/deploy runbooks/incidents runbooks/diagrams
printf '# Deploy to production\n\nMerge to main; the gate job deploys.\n' > runbooks/deploy/production.md
printf '# Roll back a release\n\nRun the rollback job with the previous tag.\n' > runbooks/deploy/rollback.md
printf '# Database is slow <script>alert(1)</script>\n\nCheck locks first.\n' > runbooks/incidents/slow-db.md
printf 'flowchart LR\n  dev[Merge] --> gate[Gate job] --> prod[Production]\n' > runbooks/diagrams/deploy-flow.mmd
# A document with two diagrams and links to the other documents, for the
# preview page.
cat > runbooks/deploy/how-it-works.md <<'Y'
# How a deploy works

A merge to main starts the gate job. The steps for people are in
[Deploy to production](production.md); when something goes wrong, see
[Roll back a release](rollback.md) and the [flow diagram](../diagrams/deploy-flow.mmd).

```mermaid
sequenceDiagram
  participant Dev
  participant Gate as Gate job
  participant Prod as Production
  Dev->>Gate: merge to main
  Gate->>Gate: build and test
  Gate->>Prod: deploy the bundle
  Prod-->>Dev: health check green
```

## When a deploy fails

```mermaid
flowchart TD
  fail[Deploy failed] --> logs{Logs clear?}
  logs -- yes --> fix[Fix and merge again]
  logs -- no --> rollback[Roll back to the last tag]
  rollback --> page[Page the on-call person]
```

| Step | Who | Time |
| --- | --- | --- |
| Build | Gate job | 4 min |
| Deploy | Gate job | 2 min |
| Check | On call | 5 min |
Y
# A draw.io file with three pages, saved uncompressed.
cat > runbooks/diagrams/architecture.drawio <<'Y'
<mxfile host="drawio" version="24.7.17">
  <diagram id="p1" name="Overview">
    <mxGraphModel dx="800" dy="600" grid="1" gridSize="10" page="1" pageScale="1" pageWidth="827" pageHeight="583">
      <root>
        <mxCell id="0" />
        <mxCell id="1" parent="0" />
        <mxCell id="a" value="Browser" style="rounded=1;whiteSpace=wrap;fillColor=#dae8fc;strokeColor=#6c8ebf;" vertex="1" parent="1">
          <mxGeometry x="40" y="80" width="120" height="60" as="geometry" />
        </mxCell>
        <mxCell id="b" value="Caddy" style="rounded=1;whiteSpace=wrap;fillColor=#d5e8d4;strokeColor=#82b366;" vertex="1" parent="1">
          <mxGeometry x="240" y="80" width="120" height="60" as="geometry" />
        </mxCell>
        <mxCell id="c" value="loadout serve" style="rounded=1;whiteSpace=wrap;fillColor=#fff2cc;strokeColor=#d6b656;" vertex="1" parent="1">
          <mxGeometry x="440" y="80" width="120" height="60" as="geometry" />
        </mxCell>
        <mxCell id="d" value="SQLite" style="shape=cylinder3;whiteSpace=wrap;boundedLbl=1;size=10;fillColor=#f8cecc;strokeColor=#b85450;" vertex="1" parent="1">
          <mxGeometry x="460" y="200" width="80" height="80" as="geometry" />
        </mxCell>
        <mxCell id="e1" style="endArrow=classic;" edge="1" parent="1" source="a" target="b"><mxGeometry relative="1" as="geometry" /></mxCell>
        <mxCell id="e2" style="endArrow=classic;" edge="1" parent="1" source="b" target="c"><mxGeometry relative="1" as="geometry" /></mxCell>
        <mxCell id="e3" style="endArrow=classic;" edge="1" parent="1" source="c" target="d"><mxGeometry relative="1" as="geometry" /></mxCell>
      </root>
    </mxGraphModel>
  </diagram>
  <diagram id="p2" name="Deploy">
    <mxGraphModel dx="800" dy="600" grid="1" gridSize="10" page="1" pageScale="1" pageWidth="827" pageHeight="583">
      <root>
        <mxCell id="0" />
        <mxCell id="1" parent="0" />
        <mxCell id="m" value="Merge" style="ellipse;whiteSpace=wrap;" vertex="1" parent="1">
          <mxGeometry x="40" y="60" width="100" height="50" as="geometry" />
        </mxCell>
        <mxCell id="g" value="Gate job" style="rounded=0;whiteSpace=wrap;" vertex="1" parent="1">
          <mxGeometry x="200" y="60" width="120" height="50" as="geometry" />
        </mxCell>
        <mxCell id="p" value="Production" style="rounded=0;whiteSpace=wrap;fillColor=#d5e8d4;" vertex="1" parent="1">
          <mxGeometry x="380" y="60" width="120" height="50" as="geometry" />
        </mxCell>
        <mxCell id="x1" style="endArrow=classic;" edge="1" parent="1" source="m" target="g"><mxGeometry relative="1" as="geometry" /></mxCell>
        <mxCell id="x2" style="endArrow=classic;" edge="1" parent="1" source="g" target="p"><mxGeometry relative="1" as="geometry" /></mxCell>
      </root>
    </mxGraphModel>
  </diagram>
  <diagram id="p3" name="Backups">
    <mxGraphModel dx="800" dy="600" grid="1" gridSize="10" page="1" pageScale="1" pageWidth="827" pageHeight="583">
      <root>
        <mxCell id="0" />
        <mxCell id="1" parent="0" />
        <mxCell id="t" value="Nightly timer" style="rounded=1;whiteSpace=wrap;" vertex="1" parent="1">
          <mxGeometry x="40" y="60" width="140" height="50" as="geometry" />
        </mxCell>
        <mxCell id="bk" value="Backup folder" style="shape=folder;whiteSpace=wrap;" vertex="1" parent="1">
          <mxGeometry x="260" y="50" width="140" height="70" as="geometry" />
        </mxCell>
        <mxCell id="y1" style="endArrow=classic;" edge="1" parent="1" source="t" target="bk"><mxGeometry relative="1" as="geometry" /></mxCell>
      </root>
    </mxGraphModel>
  </diagram>
</mxfile>
Y
$L publish runbooks --remote lime >/dev/null
# A proposal for the skill: change a reference, add one, remove one.
sed -i '' 's/1.0.0/1.1.0/' release-notes-skill/loadout.yaml
printf '# Style\n\nPlain words. No marketing. Link every ticket.\n' > release-notes-skill/references/style.md
rm release-notes-skill/references/template.md
printf '# Examples\n\n- Fix: retries on upload (TRK-1234)\n' > release-notes-skill/references/examples.md
$L propose release-notes-skill --remote lime --note "Link every ticket; examples instead of a template" --prompt "Write release notes for 2.4.0" >/dev/null
echo seeded
