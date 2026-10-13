# Open source: a general loadout, and skills that set up a registry

Status: steps 1 to 9 built (2026-10-07). Steps 10 to 12 to do. Decisions confirmed
by the owner (2026-10-06): a fresh public repository, built-in host skills, a
private ops repository (see Decisions). Open before step 12: who holds the
copyright (see Risks).

## Goal

Publish loadout under the MIT licence as a tool anyone can run. Today the code,
the deploy files and the docs assume one owner's setup: one registry
(the owner's), one Hetzner VM, one mail relay, one employer's cloud and its
teams. After this plan nothing in
the public tree names them, every place that held one of them reads
configuration or says "example.com", and two new skills let a person's own AI
agent take them from nothing to a running registry with sign-in mail.

The one rule holds. The two skills are Markdown that the person's own agent
reads on the person's own machine. They tell that agent which commands to run
with the person's own cloud CLI (`hcloud`, `aws`, `doctl`, `flyctl`) and the
person's own credentials, which the cloud CLI keeps in its own store. loadout
runs no model, calls no cloud API on anyone's behalf, and stores no cloud,
DNS or mail credential beyond what `loadout serve` already reads from its
environment file on the registry host. The skills forbid the agent to ask
for, read or print a secret (see Guardrails).

## What exists today

- `deploy/`: `cloud-init.yaml` (admin user, SSH hardening, ufw, unattended
  upgrades, Caddy from its apt repository, the `loadout` user), `Caddyfile`
  (registry block, plus a static-site block whose default domain is
  `loadout.lat`), `loadout.service` (hardened unit), `loadout-backup.sh`,
  `.service`, `.timer` (nightly `sqlite3 .backup`, 14 days on the same disk),
  `deploy.sh` (builds from the checkout with `go build`, uploads over ssh,
  installs, health-checks), `make-env.sh` (reads `GOOGLE_*` and a mail
  provider's names from a secrets file, with the provider's host, the owner's
  registry address and sender hardcoded), `site/` (the docs pages and a
  personal page with a photo), and `deploy_test.go` (runs `deploy.sh` against
  stubs).
- Login (`cmd/loadout/login.go`): `loginOptions` builds the mailer with
  `loginMailer` (test mailer, `mail.NewSMTP`, `mail.NewLog` with
  `LOADOUT_DEV=1`, or none) and Google with `loginGoogle`. `mail.SMTP` speaks
  STARTTLS only and refuses to authenticate without it. The port is free
  (`LOADOUT_SMTP_PORT`, default 587).
- Simple login (`LOADOUT_LOGIN=simple`): `account.Service.EnsureAccounts`
  seeds the accounts in `cmd/loadout/bootstrap.go`, two real work
  addresses of the owner's with the password `password`, as admins. There is no other way to
  give an account a password in simple mode.
- Host commands (`--data`): `admin grant|revoke|list`, `member ...`, `user
  delete`, `token create`, `namespace create`. Nothing sets a password, sends a
  test mail, or checks the configuration without starting the server.
- Client addresses (`internal/ratelimit`): `ClientIP` trusts `X-Real-IP` only
  from a loopback peer (Caddy on the same machine). Behind any other proxy (a
  container platform, a tunnel) every request has the proxy's address, so the
  per-address sign-in limits become one bucket for everyone.
- Built-in skills (`internal/builtin`): one embedded set (`loadout-setup`,
  `-author`, `-review`, `-admin`), installed by `login`, kept in step by
  `sync`, shown on `/skills`, checked by `internal/cli/builtin_skills_test.go`
  (every `loadout ...` command in their text must exist).
- Release: `.goreleaser.yaml` (archives, checksums, Homebrew cask with
  `skip_upload: true`), `.github/workflows/ci.yml` and `release.yml`, tag
  `v0.1.0`. No `LICENSE` file.
- Git history: 331 commits, one author (the owner's personal address). A scan
  of all branches for token, key and password patterns found only the fake
  test token `ldt_fake...`. `secrets.env` was never committed. History does
  hold the seed accounts with their password, the VM's IP, internal repository
  and team names in a publish skill, a personal page's photo, and a branch
  with an employer's branding.

## Audit: owner and company specifics, by file

Found with `git grep -i` for the company, client, team and host names, the
VM's IP, the mail relay and the owner's names, leaving out the Go module path
(`github.com/petervlad/loadout`, which stays if the repository does).

Done in step 8. Names of the companies and teams are deliberately not repeated
here. The denylist that finds them lives in the ops repository.

Left the public tree, for the owner's private ops repository (or deleted):

| What | Why | Where it went |
| --- | --- | --- |
| the deploy skill and its ssh and scp wrappers | the VM's IP, the ssh key name, the registry's address, pushing with a token from the secrets file | ops repository. The public repository has `CONTRIBUTING.md` for releases and the two host skills for operators |
| the publish-layers skill | the employer's and team's namespaces, internal repository paths, the owner's remotes | ops repository |
| the personal page under `deploy/site` | a photo of a person | ops repository. Never public |
| `deploy/make-env.sh` | the mail provider's names and hosts, the owner's address and sender | deleted in step 4. Replaced by `deploy/loadout.env.example` and `loadout serve --check` |
| `cmd/loadout/bootstrap.go` | two real addresses, password `password` | deleted in step 1. Replaced by `loadout user password` |
| the Host panel plan | the employer's cloud | rewritten as `docs/plans/host-panel.md` (the feature, no company). The original is in the ops repository |
| `.claude/worktrees/`, `.scratch/`, `secrets.env` | local work, secrets | git-ignored. Stay out |

Rewritten in place:

| File | What changed |
| --- | --- |
| `CLAUDE.md` | the borrowed-skills notes name no company or team. The Safety section keeps the general rules, and the owner's hosts and secrets file move to a git-ignored `CLAUDE.local.md` (Claude Code reads it) in the owner's checkout, with `.claude/settings.local.json` also ignored. The Go path note is "Go 1.27 or later on PATH" |
| `docs/DESIGN.md` | simple login is a supported mode. `loadout user password` replaces the seeds. The `LOADOUT_PUBLIC_URL` example is `https://registry.example.com`. The new variables are in the table |
| `docs/ARCHITECTURE.md` | the diagram says "your registry" and "your SMTP provider" |
| `docs/GUIDE.md` | "Run your own registry" points to the two skills and `deploy/README.md` |
| `deploy/README.md` | Hetzner is one provider among several. Section 5 uses `loadout.env.example`. "Login without mail" replaces the temporary-mode wording |
| `deploy/Caddyfile` | the static-site block lives in `deploy/site.caddy`, installed only when `LOADOUT_SITE_DOMAIN` is set |
| `internal/config/config.go`, `internal/mail/smtp.go` | comment examples use example.com and "a provider" |
| `docs/agents/triage-labels.md` | "a maintainer", "the live registry" |
| `docs/plans/*.md` | a pass for host, team and person names. The designs stay as design history |
| `.claude/agents`, `.claude/skills` (preview, diagnosing-bugs, pr, implement, fanout) | "never against a live registry" without its address. References to the deploy skill point at `CONTRIBUTING.md`. The apple-design reviewer stays a link to its repository, marked "if you have it installed" |
| tests, contracts, preview seeds, golden files | real addresses and the owner's name became `ada@example.com`, `vera`. Fixture names that were a real team's were renamed to neutral words of the same length, so one denylist covers the whole tree (an earlier draft kept them, a mechanical same-length rename was cheap, and the tests pin behaviour either way) |
| `README.md` | rewritten in step 10 |

Keep, since they are the project's and not the owner's instance:

- `loadout.lat` as the project's site (docs and What's new), referenced by
  `internal/web/ui.go` (`docsURL`), `internal/buildinfo/method.go` and
  `docs/guide/docs.js`. If the owner drops the domain, the same three places
  change to the new site in one commit. `docsURL` also becomes overridable
  (`LOADOUT_DOCS_URL`) for registries without internet access.
- `internal/hetzner` and the Hetzner panels of the server page: opt-in, read
  only, useful to every Hetzner operator.
- HydraFusion mentions: a public Copilot feature.
- `github.com/petervlad/loadout` in `go.mod`, `.golangci.yml`,
  `.goreleaser.yaml`, `scripts/install.sh` and the unit files'
  `Documentation=`, as long as the public repository lives there.

The audit is repeated before publishing with a denylist that lives in the ops
repository (the list itself names the companies, so it must not be public),
run as a pre-push hook there: `git grep -i -f denylist` on the export must be
empty.

## Design

### 1. Host commands: passwords, configuration, a test mail

Replaces the seeds and `make-env.sh`, and gives the skills something to run
and verify with. Logic lives in `internal/account`, `internal/config` and
`internal/mail`. `internal/cli` parses and prints.

- **`loadout user password --email E --data DIR`** reads the new password from
  stdin (a terminal gets no echo, a pipe is read to the first newline, at most
  256 bytes), never from a flag, so it stays out of shell history and process
  lists. `account.Service.SetPasswordAsOperator(ctx, actor, email, pw)` needs
  `account.Operator()`, applies `password.Check` (12 to 128 characters, the
  common list, not the email), creates the account if missing, sets the hash,
  marks the address verified (the operator vouches for it), and ends the
  user's other sessions, as setting a password already does. This is how the
  first admin and every simple-login user get a password.
- **`EnsureAccounts` and `bootstrap.go` are deleted.** Existing rows are not
  touched: seeded accounts stay, with whatever password they have now.
- **`config.ParseEnvFile(r io.Reader) (map[string]string, error)`** reads the
  subset of systemd's `EnvironmentFile` syntax that `deploy.sh` installs:
  comments, blank lines, `KEY=VALUE`, one pair of quotes. It never runs the
  file. `config.Unknown(env)` names `LOADOUT_` keys that `config` does not
  know, so a typo (`LOADOUT_SMTP_PASS`) is reported instead of silently
  turning email off.
- **`loadout serve --check [--env-file F]`** runs `config.Load` over the
  process environment (or the parsed file), builds the mailer and Google
  client exactly as `serve` would (`loginMailer`, `loginGoogle`, no network),
  prints one line per part (public URL, login mode, email via smtp
  host:port or off, Google on or off, server page facts, unknown variables)
  and exits 0, or names the variable that is wrong. It prints no value of a
  secret. `deploy.sh` runs it on the host before it restarts the service.
- **`loadout mail test --to ADDRESS`** builds the same mailer from the
  environment and sends one message ("loadout test mail from
  {public URL}"), waiting for the SMTP answer (not the queue), and prints
  either "sent" or the server's refusal with the secret left out (the SMTP
  errors already never carry the password). On the host it runs with the
  service's environment:
  `sudo systemd-run --pipe --wait --uid=loadout --property=EnvironmentFile=/etc/loadout/loadout.env /usr/local/bin/loadout mail test --to you@example.com`.
- `loginMailer` and `loginGoogle` move from `cmd/loadout/login.go` into one
  constructor both `serve` and the two commands share, so the check cannot
  disagree with the server. It stays in `cmd/loadout` (wiring) and reaches
  the commands as `cli.Deps.NewMailer`.

```go
// in internal/cli: the commands own the small interface they need.
type MailSender interface {
    Send(ctx context.Context, m mail.Message) error
}

// cli.Deps
NewMailer func(config.Login) (MailSender, string, error) // mailer, kind ("smtp", "log", "off")
```

### 2. Proxies that are not on the same machine

Container platforms (Fly), tunnels (Cloudflare Tunnel) and load balancers put
a proxy that is not loopback in front of `loadout serve`.

```go
// Package ratelimit
// Proxies says which peers may name the client, and in which header.
// The zero value trusts X-Real-IP from loopback only, as today.
type Proxies struct{ header string; trusted []netip.Prefix }

func NewProxies(header string, trusted []netip.Prefix) (Proxies, error)
func (p Proxies) Wrap(next http.Handler) http.Handler
```

- `NewProxies` refuses an empty header with prefixes, `X-Forwarded-For` (a
  list whose parsing invites mistakes. Platforms that matter send a single
  value: `Fly-Client-IP`, `CF-Connecting-IP`, `True-Client-IP`,
  `X-Real-IP`), and `0.0.0.0/0` or `::/0` (trusting every peer lets clients
  choose their own rate-limit key).
- `Wrap` sets `r.RemoteAddr` to the header's address when the peer is in a
  trusted prefix and the header holds exactly one valid address. Otherwise
  the request is untouched. `ClientIP` and its four callers
  (`server/auth.go`, `web/web.go`) do not change.
- Configuration: `LOADOUT_TRUSTED_PROXIES` (comma-separated prefixes) and
  `LOADOUT_CLIENT_IP_HEADER`, read in `config.Load` into `config.Proxies`,
  turned into `ratelimit.Proxies` and wrapped around the handler in
  `cmd/loadout`. Unset is today's behaviour.
- Caddy side, for a tunnel ending on the same machine: the Caddyfile sends
  `header_up X-Real-IP {client_ip}` instead of `{remote_host}` (equal unless
  Caddy's own `trusted_proxies` is set) and imports
  `/etc/caddy/loadout.d/*.caddy`, where a recipe may put a `servers {
  trusted_proxies ... client_ip_headers ... }` snippet. With the folder empty
  Caddy behaves as today. (To verify with `caddy adapt` in step 3.)

### 3. Deploy files any host can use

`deploy/` stays the one source of the host setup. The skills ship it as
assets (section 4).

- **`deploy/prepare.sh`** (new): everything `cloud-init.yaml` does after the
  account, as an idempotent script: packages (curl, gpg, sqlite3, ufw,
  unattended-upgrades, Caddy from its repository), SSH hardening, ufw, the
  `loadout` user and folders. `deploy.sh` runs it when `LOADOUT_PREPARE=1` is
  set (first deploy) and never otherwise, so the owner's live VM is untouched
  by routine deploys. `cloud-init.yaml` keeps only what must happen before
  the first ssh login (the `admin` user with the key, `ssh_pwauth: false`,
  `disable_root`). A bare VPS or a home server without cloud-init gets the
  same result: create the admin user by hand (the recipe gives the commands),
  then deploy with `LOADOUT_PREPARE=1`. One script, two ways in.
- **`deploy.sh` without a checkout.** `LOADOUT_BINARY=FILE` installs a binary
  the person has. `LOADOUT_VERSION=X.Y.Z` downloads
  `loadout_X.Y.Z_linux_ARCH.tar.gz` and `checksums.txt` from the project's
  GitHub release and refuses a SHA-256 mismatch. With neither, it builds from
  the checkout as today. Outside a checkout it stops and names the two
  variables. The static site is installed only when `LOADOUT_SITE_DIR` and
  `LOADOUT_SITE_DOMAIN` are set (then `site.caddy` goes to `loadout.d/`).
- **`deploy/loadout.env.example`**: every variable from DESIGN's table,
  commented out, with one line on what each does, the SMTP lines grouped
  first. The person copies it (`install -m 600`), fills it in with their
  editor, and passes it as `LOADOUT_ENV_FILE`, which `deploy.sh` already
  installs root-only. `deploy.sh` runs `loadout serve --check` against it
  locally (when a local `loadout` exists) and on the host.
- **`deploy/container/`** (new): a `Dockerfile` (a static release binary on a
  distroless base, non-root, `/data` volume, `loadout serve --data /data
  --addr :8080`) with `ARG LOADOUT_VERSION` and checksum verification, and
  `fly.toml.example` (one machine, one volume, `[http_service]`
  `force_https`, health check on `/v1/health`, `LOADOUT_TRUSTED_PROXIES` and
  `LOADOUT_CLIENT_IP_HEADER=Fly-Client-IP` with the platform's documented
  proxy range). One instance only: SQLite, the rate limits and device
  sign-ins are per process.
- **`deploy/files.go`** (new, `package deploy`): `//go:embed` of the files
  above as `deploy.FS`, so `internal/builtin` can ship them without copies.
  `deploy_test.go` stays `package deploy_test`.

### 4. The two skills: where they live and how they are built

They are built-in skills of a second set, **host skills**, embedded in the
binary but installed only on request.

- **Why built in.** The person who needs them has no registry yet, so they
  cannot come from one (a loadout package on a registry is ruled out). A copy
  under `.claude/skills` reaches only people who clone the repository and
  open it in their agent. Most operators install with Homebrew or a release
  archive. Built in, the text and the deploy files always match the CLI that
  wrote them, the honesty test checks every `loadout` command they name,
  `sync` keeps them in step and `uninstall` removes them, all for free.
- **The set is a table, not a flag on every call.** `internal/builtin` gets:

  ```go
  // Set is a group of built-in skills that are installed together.
  type Set struct {
      Name string // "everyday", "host"
      dir  string // folder in the embedded FS
      // Auto sets are installed by login and sync; others only by setup.
      Auto bool
      // Assets adds files to a skill, e.g. the deploy files from deploy.FS.
      Assets func(skill string) (map[string][]byte, error)
  }

  var Sets = []Set{everyday, host}
  ```

  `Packs(version)` keeps returning the everyday set (the `/skills` pages and
  login are unchanged). `PacksOf(set, version)` builds any set.
  `Service.apply` decides per pack with one rule: a pack of a set that is not
  `Auto` is considered only when the person asked for that set now, or when
  the lock already has it. So `sync` updates installed host skills, never adds
  them. `IsBuiltin` and `Decline` cover both sets. `builtin.yaml` gains no
  field, so older CLIs read it as before. An older CLI ignores a host skill's
  lock entry (it has source `builtin`, no remote, and is not in its packs).
- **CLI:** `loadout setup --host-skills [--target T] [--force]`, beside
  `--skills`. `internal/cli/setup.go` only adds the flag and prints the
  report it already prints.
- **Files:** `internal/builtin/host/loadout-host-setup/` and
  `internal/builtin/host/loadout-host-provision/`. The provision skill's
  `assets/` are filled from `deploy.FS` at pack time (`Set.Assets`), so
  `deploy/` stays the only copy. Assets are text, as packages require.
  Loadout installs files without the executable bit, so the skill says
  `sh assets/deploy.sh`.
- **Not on `/skills`.** Those pages serve people who already have a registry.

### 5. `loadout-host-provision`: a machine, a name, TLS, backups

Description (frontmatter): create and harden the machine for a loadout
registry on the person's provider, give it a name and HTTPS, install loadout
with systemd and Caddy, and set up off-host backups.

Layout:

```
SKILL.md                      flow, guardrails, the provider index table
references/shapes/vm.md       a Linux VM: cloud-init or a first ssh, prepare, deploy.sh
references/shapes/container.md a container platform: Dockerfile, volume, one instance
references/providers/hetzner.md
references/providers/aws.md            Lightsail first, EC2 for an existing VPC
references/providers/digitalocean.md
references/providers/fly.md
references/providers/vps.md            any VPS with ssh (Vultr, Linode, OVH, Scaleway, ...)
references/providers/home.md           a home or office server: LAN only, port forward, or a tunnel
references/dns.md             A and AAAA records, sslip.io, checking with dig before TLS
references/backups.md         provider snapshots, a pull with rsync, a restore test
assets/                       cloud-init.yaml, prepare.sh, deploy.sh, Caddyfile, units,
                              loadout-backup.*, loadout.env.example, Dockerfile, fly.toml.example
```

- **Providers are files, picked from a table.** `SKILL.md` has one table:
  provider, shape, file, what the person needs (an account, the provider's
  CLI logged in), rough monthly cost, notes. Each provider file follows one
  outline, the same headings in the same order: Before you start, Create the
  machine, Firewall, Name, Deploy, Off-host backups, Cost, Tear down. A
  provider file says only what differs. The shape file says the rest. An
  unknown provider is handled by the shape file plus `vps.md` ("map each
  heading to your provider's console"). Adding a provider is one file and
  one row. Nothing else changes, and a lint test enforces that (Tests).
- **Flow.** Ask where (provider, or "I have a machine"), which name (own
  domain or `<ip-with-dashes>.sslip.io`), and arch. Check the provider CLI is
  logged in (`hcloud context list`, `aws sts get-caller-identity`, `doctl
  account get`, `fly auth whoami`) without reading its credentials. Show the
  cost and ask. Create the machine with the person's SSH public key. Open
  22, 80, 443 (tcp and udp). Set DNS and wait until `dig +short NAME` returns
  the address (Let's Encrypt limits failed attempts, so TLS is tried only
  after DNS is right). Deploy with `LOADOUT_PREPARE=1` and
  `LOADOUT_VERSION` = the CLI's own version. Check `https://NAME/v1/health`.
  Turn on off-host backups and run one restore into a scratch folder. Write
  down, for the person, what was created and how to tear it down.
- **TLS** is Caddy's automatic certificate on the VM shape, the platform's on
  the container shape (`fly certs add`), and on a LAN-only home server either
  a tunnel's or Caddy's `tls internal` with the root certificate handed to
  each laptop (the web pages need HTTPS: session cookies are `Secure` except
  on `http://localhost`).
- **Provider facts that bite** go in the files with the date they were
  checked: DigitalOcean blocks outgoing SMTP on 25, 465 and 587 for new
  accounts, so mail goes over a provider's alternative port (2525, or 2587
  for Resend). Hetzner blocks 25 and 465 for new accounts (587 works). AWS
  Lightsail and EC2 throttle 25 only.

### 6. `loadout-host-setup`: the walk-through

Description: set up a loadout registry step by step: the machine (with
`loadout-host-provision`), the first admin, the domain, sign-in mail through
SMTP or a provider such as SES, Resend or Postmark, or simple login when there
is no mail, and the first project and team.

Steps, each ending with a check the agent runs and shows:

1. **Choose the login mode** with the person, from a table:

   | Mode | Needs | People sign up themselves | Lost password |
   | --- | --- | --- | --- |
   | Email codes (recommended) | an SMTP provider and a sender domain | yes | mailed code |
   | Email codes plus Google | the above and a Google OAuth client | yes | mailed code or Google |
   | Simple login (`LOADOUT_LOGIN=simple`) | nothing | no | the operator runs `loadout user password` |
   | Tokens only (no `LOADOUT_PUBLIC_URL`) | nothing | no (invites) | a new invite |

2. **The machine**: run `loadout-host-provision`. Check: `/v1/health`.
3. **The environment file**: `install -m 600 assets/loadout.env.example
   ~/loadout.env`. The person fills it in with their editor. The agent
   runs `loadout serve --check --env-file ~/loadout.env` and reads its
   summary, never the file. Check: the summary says the mode chosen.
4. **Email** (unless simple or tokens only): pick the provider from
   `references/email/` (one file each, same outline: Account, Sender domain
   and DNS (SPF, DKIM, DMARC), SMTP credentials, Values for loadout.env,
   Limits and sandbox, Cost). Files: `ses.md` (`email-smtp.REGION.amazonaws.com`,
   587, SMTP credentials derived from an IAM user, sandbox until production
   access, which also limits recipients), `resend.md` (`smtp.resend.com`,
   587 or 2587, user `resend`, an API key as the password, a verified domain),
   `postmark.md`, `brevo.md`, `mailgun.md`, `workspace.md` (Google Workspace
   relay or a Gmail app password, for a handful of people), `m365.md` (SMTP
   AUTH is often turned off by tenants), `other.md` (any provider that offers
   STARTTLS submission). The sender address is on a domain the person
   controls. The DNS records are added before the first mail. Deploy with
   the file, then `loadout mail test --to` the person's address on the host.
   Check: the mail arrives, not in spam, and the headers say `dkim=pass` and
   `spf=pass` (the person reads them, the agent asks).
5. **Google** (optional): the OAuth client, the redirect URI
   `{public URL}/auth/google/callback`, the two variables.
6. **The first admin**: on the host, `loadout admin grant --email E --data
   /var/lib/loadout`. With email login the person then signs up at
   `/auth/signup` with that address and confirms the code. With simple login
   the operator runs `loadout user password --email E` and types the password
   (the agent never sees it). Check: `loadout admin list` shows the address
   verified, and `loadout login --remote URL` works on the laptop.
7. **Simple login, said plainly**: no self sign-up, no reset by mail, no
   Google. The operator sets every password with `loadout user password` and
   hands it over out of band. Switching to email later is one line removed
   and mail settings added, and the accounts keep their passwords.
8. **First project and team** on the web (`/new`) or with `loadout namespace
   create`; invite people (`loadout member invite`). Then hand over to the
   built-in `loadout-admin` skill.
9. **Operate**: an uptime check on `/v1/health` from outside, `journalctl -u
   loadout`, updates by running `deploy.sh` again with a new
   `LOADOUT_VERSION` (back up first when the release notes name a
   migration), and the Host panel lines (`LOADOUT_HOST_*`).

### Guardrails (both skills)

- Ask before every action that costs money, changes DNS, or deletes
  anything, naming the resource, the provider and the price. Never tear
  down without a yes for that teardown.
- Never ask for, read, print or paste a secret: cloud tokens stay in the
  provider CLI's own store (the person logs in), SMTP passwords and Google
  secrets are typed by the person into `loadout.env` with their editor, and
  passwords into `loadout user password`. Never open `loadout.env`,
  `/etc/loadout/loadout.env` or `~/.loadout/credentials.yaml`. Use `loadout
  serve --check` to learn what is set.
- Never `set -x` around a command that loads the env file.
- Run host commands as the service user with `--data /var/lib/loadout`.
- loadout never calls a model and holds no AI key: do not look for one or
  suggest one.

### 7. Licence and third-party notices

- `LICENSE`: MIT, "Copyright (c) 2026 HOLDER", HOLDER as decided before
  publishing (see Risks).
- `THIRD_PARTY_NOTICES.md`, written by `scripts/notices.sh` from the modules
  the binary links (`go list -deps -f '{{with .Module}}...{{end}}'
  ./cmd/loadout`) with each module's `LICENSE` and `NOTICE` text from the
  module cache (cobra, pflag, mousetrap, go-oidc and go-jose are Apache-2.0.
  Modernc sqlite and libc, the `golang.org/x` modules, uuid and bigfft are
  BSD-style. Goldmark, yaml, isatty, humanize and strftime are MIT or
  MIT and Apache), plus the vendored mermaid (MIT) and draw.io viewer
  (Apache-2.0, with its own `THIRD-PARTY.md`), and a section listing the
  borrowed skills under `.claude/skills` with their repositories, commits and
  MIT licences (each keeps its `LICENSE` file in its folder). `notices.sh
  --check` fails when the file is stale. CI runs it. Release archives include
  `LICENSE` and `THIRD_PARTY_NOTICES.md` (`.goreleaser.yaml` `files`).
- The apple-design skill has no licence: it is never copied into the
  repository, and docs only link to its repository as an optional reviewer.
  A test in `internal/builtin` already refuses em-dashes. The notices check
  also fails on a folder under `.claude/skills` without a `LICENSE` that is
  not one of loadout's own (`loadout-fanout`, `loadout-preview`).

### 8. Docs and community files

- `README.md`: what loadout is and the one rule. Install (Homebrew, release
  archive, `go install`). A quick start for a LAN registry (as today). "Run
  your own registry" with `loadout setup --host-skills` and then "ask your
  agent to set up a loadout registry", or `deploy/README.md` by hand.
  Supported tools. Links to GUIDE, DESIGN, CONTRIBUTING, SECURITY. Licence.
- `docs/GUIDE.md` "Run your own registry": the four login modes, the host
  commands (`serve --check`, `mail test`, `user password`), the env file,
  trusted proxies. Rebuilt with `docs/guide/build.sh`.
- `CONTRIBUTING.md`: Go 1.27, `make check`, pandoc for the guide, the code
  and writing rules (pointing to `CLAUDE.md` and `docs/DESIGN.md`), the one
  rule as a hard line for features, tests expected, plain commit messages,
  inbound licence equals outbound (MIT, no CLA, DCO sign-off optional), how to
  cut a release (tag, CI builds it), AI-assisted contributions welcome under
  the same review.
- `SECURITY.md`: report privately through GitHub security advisories. What
  is in scope (registry, web pages, bundle decoding, CLI file writes, deploy
  files). Supported versions (the latest minor). What a report gets back and
  when.
- `.github/`: issue templates (bug, feature, provider recipe), a pull
  request template with the `make check` box.

### 9. CI and release

- Actions pinned by commit SHA. Dependabot for Go modules and Actions.
- New CI jobs: `scripts/notices.sh --check`. `shellcheck` on `deploy/*.sh`
  and `scripts/*.sh`. `caddy adapt` on the Caddyfile with and without the
  site block. Gitleaks over the tree and history (generic rules only).
- Release: goreleaser adds `LICENSE` and notices to archives, SBOMs (syft),
  and build provenance with `actions/attest-build-provenance`. The Homebrew
  cask's `skip_upload` goes back to `auto` once the repository is public.
  `deploy.sh` and the Dockerfile verify `checksums.txt`.
- The first public tag is above `v0.1.0` (for example `v0.2.0`), so CLIs
  that already know `0.1.0` still compare correctly.

## Data and API changes

- No database change, no migration, no wire change. Existing rows, including
  the accounts the seeds made, are kept as they are.
- New environment variables (all optional, unset is today's behaviour):
  `LOADOUT_TRUSTED_PROXIES`, `LOADOUT_CLIENT_IP_HEADER`, `LOADOUT_DOCS_URL`.
  New deploy variables: `LOADOUT_PREPARE`, `LOADOUT_BINARY`,
  `LOADOUT_VERSION`, `LOADOUT_SITE_DIR` (`LOADOUT_SITE_DOMAIN` becomes
  opt-in).
- New host commands: `user password`, `mail test`, `serve --check`. New
  client command: `setup --host-skills`.
- Removed: `LOADOUT_LOGIN=simple` no longer seeds accounts.

## Migration

- **The live registry first.** Before a build without the seeds reaches it,
  the owner sets real passwords on the seeded accounts with `loadout user
  password` (built in step 1), or confirms they were changed. Do this now
  regardless of the release: the seed password is in this repository's
  history.
- The owner's deploys continue from the ops repository with the public
  `deploy.sh`: `LOADOUT_PREPARE` unset, `LOADOUT_SITE_DIR` and
  `LOADOUT_SITE_DOMAIN=loadout.lat` set so the docs keep being served. The
  first deploy after step 3 is checked with a backup taken first, as for a
  migration, because the Caddyfile changes.
- Old CLIs: a host skill's lock entry is ignored by an older CLI. Nothing
  else they read changes.

## CLI and web changes

- CLI: `loadout user password`, `loadout mail test`, `loadout serve
  --check [--env-file F]`, `loadout setup --host-skills`.
- Web: none, except the Docs and What's new links following
  `LOADOUT_DOCS_URL`.

## Order of work

Each step ends with `make check` green and can ship alone.

1. **Passwords without seeds.** (built) `account.Service.SetPasswordAsOperator`,
   `loadout user password`, tests (unit, accounttest contract unaffected, e2e
   in `cmd/loadout`). Owner sets real passwords on the live registry. Then
   delete `bootstrap.go`, `EnsureAccounts`, `Seed` and their tests. DESIGN's
   simple-login paragraph rewritten.
2. **Configuration you can check.** (built) `config.ParseEnvFile`, `config.Unknown`,
   `LOADOUT_DOCS_URL`, shared mailer constructor, `serve --check`, `mail
   test`. Comment examples to example.com.
3. **Proxies.** (built) `ratelimit.Proxies`, config, wiring in `cmd/loadout`. Caddyfile
   `{client_ip}` and the `loadout.d` import. Site block to `site.caddy`.
4. **Deploy files for any host.** (built) `prepare.sh` split from `cloud-init.yaml`.
   `deploy.sh` with `LOADOUT_PREPARE`, `LOADOUT_BINARY`, `LOADOUT_VERSION`
   (checksum), optional site, remote `serve --check`. `loadout.env.example`.
   `make-env.sh` deleted. `deploy/container/`. `deploy/files.go`.
   `deploy/README.md` generalised. Owner deploys the live registry with the
   new script (backup first).
5. **Host skill sets.** (built) `builtin.Set`, `Sets`, `PacksOf`, the apply rule,
   `setup --host-skills`, with a placeholder skill per name so the machinery
   is tested before the text.
6. **`loadout-host-provision`.** (built) SKILL.md, shapes, providers, dns, backups,
   assets from `deploy.FS`. Recipe lint test. Tried end to end on at least
   Hetzner, one other cloud and a local VM (multipass or a spare machine).
7. **`loadout-host-setup`.** (built) SKILL.md and the email files. Tried with SES or
   Resend, and with simple login.
8. **Owner specifics out.** (built) The ops repository gets the deploy and
   publish-layers skills, the personal page, the Host panel plan, the owner's
   env template, the denylist and the pre-push hook. `CLAUDE.md` split with `CLAUDE.local.md` (added to
   `.gitignore` with `.claude/settings.local.json`). The rewrites in the
   audit table.
9. **Licence and notices.** (built) `LICENSE`, `scripts/notices.sh`,
   `THIRD_PARTY_NOTICES.md`, goreleaser `files`.
10. **Docs and community files.** README, GUIDE (rebuilt), DESIGN, ARCHITECTURE,
    CONTRIBUTING, SECURITY, templates.
11. **CI and release.** Pinned actions, Dependabot, new jobs, SBOM and
    provenance, `release-snapshot` checked locally.
12. **Publish.** The export (see decision 1), the denylist check and gitleaks
    on it, a last read of `git ls-files`, then the public repository, the
    first public tag, the Homebrew cask.

## Tests

- `internal/account`: `SetPasswordAsOperator` table (operator only, weak
  password refused, new account created and verified, sessions ended,
  existing hash replaced). Simple login without seeds still signs in a user
  whose password the operator set.
- `internal/config`: `ParseEnvFile` table (comments, quotes, `export`
  refused or accepted as systemd does, CRLF, a line without `=`, duplicate
  keys take the last), `Unknown` names typos, `LOADOUT_TRUSTED_PROXIES`
  parsing and refusals.
- `internal/ratelimit`: `NewProxies` refusals. `Wrap` table (trusted peer
  with one address, untrusted peer with the header, two header values,
  garbage, IPv6 mapped).
- `cmd/loadout` e2e: `user password` then simple sign-in over the web.
  `serve --check` with a good and a bad env file (exit code, no secret in
  the output, checked by searching the output for the test secret).
  `mail test` against `mailtest`'s SMTP server. `setup --host-skills`
  Installs both host skills, `sync` updates them after a version change and
  does not add them when absent, `uninstall` declines them.
- `internal/builtin`: host packs build and lint like the everyday ones (no
  em-dashes, guardrails present). Every provider and email file has the
  outline's headings in order. The index tables in both SKILL.md files list
  every file exactly once and nothing else. Each provider names a shape that
  exists. Assets equal `deploy.FS` byte for byte.
- `internal/cli/builtin_skills_test.go`: extended to the host set, so every
  `loadout ...` command and flag in the skills exists.
- `deploy/deploy_test.go`: with `LOADOUT_BINARY` no `go` is called. With
  `LOADOUT_VERSION` a wrong checksum stops before upload. Without the site
  variables no site is uploaded and `site.caddy` is not installed.
  `LOADOUT_PREPARE` unset never runs `prepare.sh`. The env file is still
  removed on failure.
- CI: `caddy adapt` of both Caddyfile forms, shellcheck, notices check,
  gitleaks.
- Manual, recorded in the PR: one full run of `loadout-host-setup` by an
  agent on a fresh account of two providers, with timings and cost.

## Risks

- **Copyright.** MIT needs the person who holds the copyright to grant it.
  loadout was used for an employer's teams and run on their cloud. Whether
  the owner or the employer holds the copyright depends on the employment
  contract and where and when it was written. Confirm in writing before
  step 12 (this plan is not legal advice). The `LICENSE` holder follows
  from it.
- **Known credentials in history.** The seed password and addresses are in
  history. Publishing that history tells everyone how to sign in to the
  owner's registry if those passwords were never changed. Rotate first
  (Migration), and prefer a fresh export (decision 1).
- **Agents with cloud access.** An agent can create billable resources or
  delete the wrong one. The skills ask before every such step and name the
  resource. Teardown lists only what the skill created and recorded.
- **Secrets in the conversation.** If the agent read `loadout.env`, the SMTP
  password would sit in the person's chat history and the vendor's logs. The
  skills never read it and `serve --check` reports without values. The
  person types secrets in their own editor.
- **Recipes go stale.** Provider CLIs, prices and SMTP policies change. Each
  file carries "checked on DATE", the lint keeps their shape, and the skill
  tells the agent to check the provider's current docs when a command fails
  rather than improvising around it.
- **Proxy trust misconfigured.** Trusting a range too wide lets clients pick
  their rate-limit key. `NewProxies` refuses the whole internet, and the
  container recipe names the platform's documented range.
- **One instance only.** SQLite and in-memory limits mean the container
  shape must not scale out. The recipe and `fly.toml.example` pin one
  machine, and the docs say why.
- **The live registry during steps 3 and 4.** The Caddyfile and deploy script
  change under it. A backup before the first new deploy, and `serve --check`
  on the host before restart, keep a typo from taking it down.
- **Loss of history in a fresh export.** Blame and design history stay in
  the owner's private repository. The public one starts at the first public
  commit.

## Not in this step

- The registry serving its own guide at `/docs` (offline, same-origin back
  links).
- A `loadout backup` command in Go (`VACUUM INTO`) for hosts without
  systemd. The container shape relies on platform volume snapshots.
- HTTP API mailers (SES, Resend, Postmark APIs): every one of them offers
  STARTTLS submission, which `mail.SMTP` already speaks. Implicit TLS on 465
  likewise waits for a provider that needs it.
- Terraform or Pulumi modules, Kubernetes and Helm.
- Server page panels for providers other than Hetzner (`serverstats.Cloud`
  returns `hetzner.Summary` today. Generalising it is its own plan).
- A published container image (GHCR). The Dockerfile builds from a release.
- More than one registry instance, or another database.

## Decisions

Confirmed by the owner on 2026-10-06, each as recommended: 1 a fresh public
repository, 2 built-in host skills, 3 a private ops repository. The options
below are kept as the record of what was weighed.

1. **History.** Publish a fresh repository whose first commit is the
   scrubbed tree (recommended), rewrite this history with `git filter-repo`
   (keeps blame, but every rewritten commit and branch must be audited and a
   missed one is permanent), or publish as is (refused: the seed password,
   the VM's IP, internal repo names and the photo are in it).
2. **Where the two skills live.** Built-in host skills in the binary,
   installed with `loadout setup --host-skills`, with `deploy/` as their
   assets (recommended), or a top-level folder in the repository only (only
   people who clone get them, and the text can drift from the CLI). A
   loadout package on a registry is ruled out, since the person has no
   registry yet.
3. **The owner's own setup.** A private ops repository holding
   the deploy and publish skills, the personal page, the Host panel plan, the
   denylist and the owner's env template, with `CLAUDE.local.md`
   in the owner's checkout (recommended), or keeping them in the public
   repository behind placeholders (simpler, but the next edit brings a name
   back).
