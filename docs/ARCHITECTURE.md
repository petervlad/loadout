# Architecture

The diagrams of loadout, and where the technical docs are. Everything AI
stays on the user's machine; the registry only stores and serves files,
accounts and reviews.

## Technical docs

| Doc | What it covers |
|---|---|
| [DESIGN.md](DESIGN.md) | Packages and kinds, registry, layers, roles, login, review, web pages, the rules that protect users. The source of truth. |
| [STANDARDS.md](STANDARDS.md) | The Go rules this codebase follows. |
| [UI.md](UI.md) | Web UI rules and the Apple HIG review record. |
| [GUIDE.md](GUIDE.md) | The user guide, published at https://loadout.lat/docs/. |
| [CHANGES.md](CHANGES.md) | What each deploy added and fixed, published at https://loadout.lat/docs/whats-new/. |
| [plans/](plans/) | One plan per larger feature, such as [one-login](plans/one-login.md) and [skills-and-kb](plans/skills-and-kb.md). |
| [deploy/](../deploy/README.md) | Running your own registry: the deploy script, Caddy, services, backups. |
| [CONTRIBUTING.md](../CONTRIBUTING.md), [SECURITY.md](../SECURITY.md) | How to change loadout, and how to report a vulnerability. |

## The big picture

```mermaid
flowchart LR
  subgraph laptop["Your machine"]
    tools["Claude Code<br/>GitHub Copilot"]
    files["agent, skill and KB files<br/>~/.claude, ~/.copilot"]
    cli["loadout CLI<br/>(one Go binary)"]
    cli -->|writes| files
    tools -->|read| files
    cli -.->|review only: runs the agent| tools
  end
  browser["Browser"]
  subgraph cloud["Cloud: your registry"]
    reg["loadout serve<br/>API + website"]
  end
  cli <-->|HTTPS, JSON API| reg
  browser <-->|HTTPS, web pages| reg
```

## Inside the binary

The same binary is the CLI and the server; each side is thin on top of
shared services.

```mermaid
flowchart TB
  subgraph clientside["loadout (CLI)"]
    cmds["commands<br/>login, install, sync, propose, review"]
    svc["install, update, review services"]
    adapters["adapters<br/>Claude Code | Copilot"]
    cmds --> svc --> adapters
  end
  subgraph serverside["loadout serve"]
    api["JSON API"]
    web["website"]
    core["registry and account services<br/>layers, roles, reviews"]
    db[("SQLite<br/>packages, accounts, reviews")]
    api --> core
    web --> core
    core --> db
  end
  svc <-->|HTTPS| api
```

## Layers

A project (L0) sits above its teams (L1). A team reads its own packages and
the project's; when both ship the same name, the team's wins. Files a person
made themselves (L2) are never touched.

```mermaid
flowchart TB
  acme["acme (project, L0)<br/>shared by every team"]
  payments["payments (team, L1)"]
  mobile["mobile (team, L1)"]
  web["web (team, L1)"]
  acme --> payments & mobile & web
  laptop["a payments laptop<br/>payments + acme packages, and the person's own files (L2)"]
  payments --> laptop
```

A pipeline package depends on other packages across layers. The project
writes a template (its slots and the edges between them). A team writes a
binding that names the template and one of its own agents for each slot.
On a laptop, loadout renders the binding into a conductor agent, which the
person's own tool runs and which starts the team's agents in turn.

```mermaid
flowchart TB
  template["acme/dev-loop (template, L0)<br/>slots and edges, no agents"]
  binding["lime/dev-loop (binding, L1)<br/>uses acme/dev-loop@1, one agent per slot"]
  agents["lime-planner, lime-dev, lime-code-review, lime-test (L1 agents)"]
  conductor["lime-dev-loop (conductor)<br/>written by loadout install, run by Claude Code or Copilot CLI"]
  template -- "uses (a major)" --> binding
  agents -- "bind" --> binding
  binding -- "install renders" --> conductor
  conductor -- "starts each slot's agent, stops at gates" --> agents
```

## One login, many teams

`loadout login` gives one account token per registry. Each request names the
team it is about, and the registry checks the user's role there at that
moment. See [plans/one-login.md](plans/one-login.md).

```mermaid
sequenceDiagram
  participant CLI as loadout CLI
  participant Reg as registry
  participant Roles as account roles
  CLI->>Reg: GET /v1/packages<br/>Bearer account token, X-Loadout-Namespace: payments
  Reg->>Reg: token: user u1, no namespace yet
  Reg->>Roles: role of u1 in payments?
  Roles-->>Reg: maintainer (owner above, or member below, count too)
  Reg-->>CLI: payments and acme packages
  CLI->>Reg: POST /v1/packages (an acme package)
  Reg->>Roles: role of u1 in acme?
  Roles-->>Reg: member
  Reg-->>CLI: 403: publishing to acme needs the maintainer role
```

## Install and update

```mermaid
sequenceDiagram
  participant Dev as teammate
  participant CLI as loadout CLI
  participant Reg as registry
  participant Tools as Claude Code / Copilot
  Dev->>CLI: loadout install --all
  CLI->>Reg: list and fetch packages (digest checked)
  CLI->>Tools: write agents, skills, KBs where each tool looks
  Note over Dev,Tools: later, from cron or by hand
  Dev->>CLI: loadout sync
  CLI->>Reg: newer versions?
  CLI->>Tools: auto: install them; notify: say so; pin: stay
  Note over CLI,Tools: a file the person edited is never overwritten
```

## What is in the cloud

```mermaid
flowchart LR
  users["CLI and browsers"] -->|HTTPS 443| caddy
  subgraph vm["One host: a Debian or Ubuntu machine with a firewall, or a container"]
    caddy["Caddy<br/>HTTPS, certificates"]
    serve["loadout serve<br/>127.0.0.1:7420"]
    docs["optional static site<br/>guide and What's new"]
    data[("/var/lib/loadout<br/>SQLite database")]
    backup["nightly backup 03:15<br/>14 days kept"]
    caddy --> serve
    caddy --> docs
    serve --> data
    backup --> data
  end
  serve -->|sign-in| google["Google"]
  serve -->|codes and invites| smtp["Your SMTP provider"]
  serve -.->|server stats, read only, only with a token| hetzner["Hetzner API"]
```
