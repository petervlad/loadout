# Server details on any host (the Host panel)

Status: built (2026-10-06). Decisions: option 3. No cost figure for a host without an API. Values come from the operator's env file only. Deviation: the lower panels now pair as backups and registry, then mail and process. Screenshots still to take.

## Goal

On a registry that does not run at Hetzner, an admin who opens Account, **Server details** (`/account/server`) sees Hetzner: a "Hetzner server" panel, an "Estimated monthly cost" panel and an "Estimated cost" tile. They should see their own host instead: where it runs, how big it is, who looks after it, and the parts that already work anywhere (disk, backups, counts, mail, process).

The one rule holds: nothing here runs a model or needs an AI key. The plan also adds no vendor token and no new network call. Host facts come from the operator's environment file and from the machine itself.

## What exists today

- `cmd/loadout/stats.go` `serverStats` builds a `serverstats.Collector` from `config.Stats`. With `LOADOUT_HETZNER_TOKEN` set it adds a `hetzner.Client` as `serverstats.Config.Cloud`.
- `internal/serverstats` collects `Report{Process, Disk, Counts, Backups, Mail, Hetzner}`. Every part fails on its own. `serverstats.Cloud` returns a `hetzner.Summary`, so the package imports `internal/hetzner`.
- `internal/web/ui_server.go` turns the report into `serverView`. `costTile` and `newHetznerView` are Hetzner only. `serverSize` and `place` take `hetzner` types. `templates/ui/server.html` always draws the Hetzner and cost panels. Without a token it shows "Hetzner not configured" with the two variables to set, and the tile says "Not set up, No Hetzner token".
- So on any other host the page points at Hetzner either way: with no token it asks for a Hetzner token, and with the token copied from the old `loadout.env` it shows the old Hetzner server (`LOADOUT_HETZNER_SERVER` names it, so the IP lookup that would have failed is skipped).
- Disk, database, data folder, backups (`LOADOUT_BACKUP_DIR`), counts, mail and process are already provider-neutral.

## What is knowable without a token or a network call

| Fact | Source | Notes |
| --- | --- | --- |
| Host name | `os.Hostname` | Works under `ProtectHostname=yes` (it only stops changes). |
| Operating system | `PRETTY_NAME` in `/etc/os-release` | Readable under `ProtectSystem=strict`. Bounded read. |
| CPUs the service sees | `runtime.NumCPU` | |
| Architecture | `runtime.GOARCH` | |
| Public address | `config.Login.PublicURL` | Already loaded. |
| Disk size and use | `statfs`, already shown | |
| Memory of the machine | not knowable | `ProcSubset=pid` hides `/proc/meminfo`. The operator states the size. |
| Provider, region or data centre, machine size, who to contact, a console or ticket link | operator, in `/etc/loadout/loadout.env` | a private or in-house host has no API we may call. These are plain text the operator writes once. |
| Cost | operator only, if at all | No list prices or invoices to read. Left out unless the user asks (decision 3). |

Rejected as built-in parts: a cloud metadata endpoint or a provider API for hosts other than Hetzner. Both are new network calls, and the second needs a vendor token. An operator who wants one adds a provider of their own through the `serverstats.CloudProvider` seam, see "Adding a cloud provider" in `docs/DESIGN.md`.

## Design

### Decision on Hetzner: keep it, only when configured

Three shapes were weighed:

1. **Replace**: delete `internal/hetzner` now. Simplest code, but Hetzner registries exist today, and their page would lose its numbers.
2. **Generalise**: a consumer-owned `serverstats.HostSource` with a Hetzner and a static implementation, and `internal/web` no longer imports `hetzner`. Cleaner, but it is an interface for one real API-backed source, and the Hetzner view (cost table, traffic warning) does not fit neutral facts without moving presentation into `hetzner`.
3. **Neutral host panel plus optional Hetzner** (recommended): a new provider-neutral `Host` part, always shown, fed by the operator's values and the machine. The Hetzner panel, the cost panel and the cost tile appear only when a Hetzner token is set. No "not configured" nag.

Option 3 changes the least, fixes the page on other hosts, and keeps Hetzner as the one opt-in provider panel. If a second provider with an API ever appears, that is the moment for option 2.

### config: operator-supplied host facts

`internal/config/config.go` gains a `Host` value inside `Stats`, read in `Load` like the Hetzner variables:

```go
// Host describes where the registry runs, as the operator states it, for
// the admins' server page. Every field is optional plain text.
type Host struct {
	Provider string // LOADOUT_HOST_PROVIDER, "Example Cloud"
	Region   string // LOADOUT_HOST_REGION, "Frankfurt"
	Size     string // LOADOUT_HOST_SIZE, "4 vCPU, 16 GB memory"
	Contact  string // LOADOUT_HOST_CONTACT, a team or an address
	Console  string // LOADOUT_HOST_CONSOLE, an https link to the host's console or ticket queue
}
```

`Load` trims each value, refuses one over 200 bytes or with control characters, and refuses a `Console` that is not an absolute `https` URL with a host. The error names the variable, as `LOADOUT_LOG` errors do, so a typo stops `serve` at startup instead of reaching the page. Validation lives in `config` because that is where every environment value is checked once.

### serverstats: a Host part

`internal/serverstats` gets a `Host` part collected like the others:

```go
// HostFacts is what the operator states about the machine.
type HostFacts struct {
	Provider, Region, Size, Contact, Console string
}

// Host is the machine the registry runs on.
type Host struct {
	Stated    HostFacts
	Name      string // os.Hostname; NameErr says why it is missing
	NameErr   string
	OS        string // PRETTY_NAME from os-release; OSErr says why it is missing
	OSErr     string
	CPUs      int
	Arch      string
	PublicURL string
}
```

`Config` gains `Host HostFacts`, `PublicURL string`, and two seams with defaults set in `New`: `Hostname func() (string, error)` (`os.Hostname`) and `OSRelease string` (`/etc/os-release`). `Report` gains `Host Host`, filled by `c.host()`. Reading os-release goes through a small pure `prettyName(r io.Reader) string` that reads at most 16 KiB, takes `PRETTY_NAME`, falls back to `NAME` plus `VERSION_ID`, and unquotes. A missing file gives a reason through the existing `reason`, never a failed page.

`serverstats.Hetzner` and `Cloud` stay as they are.

### cmd/loadout: wiring

`serverStats` takes the public URL (from `login.PublicURL` at the call in `cmd/loadout/login.go`) and maps `config.Host` to `serverstats.HostFacts`. The "server page" log line adds `host_provider`. When both a provider other than Hetzner and a Hetzner token are set, it logs a warning that the page will also show a Hetzner server, since that is the exact mistake behind this request.

### web: the view

In `internal/web/ui_server.go`:

- `serverView` gains `Host sectionView`, built by `hostSection(serverstats.Host)`: Provider (or "Not stated"), Region, Size, Host name, System (`OS`, note `Arch` and CPUs seen), Address (`PublicURL`), Contact. Rows for empty stated values are left out. The console link is a separate field (`HostConsole string`) so the template renders it as a link. `html/template` escapes it and `config` already allowed only `https`.
- `newServerView` picks the first tile through `firstTile(rep)`: with Hetzner configured, `costTile` as today. Without it, a new `hostTile` ("Host", value the provider or else the host name, note the region). Four tiles stay, so the layout holds.
- `hetznerView.State == "off"` now hides both Hetzner panels instead of drawing the nag.

In `templates/ui/server.html`: a "Host" panel first, beside "Storage" when Hetzner is off. With Hetzner on, the Hetzner and cost pair follows as today. The `loadout-ui` agent settles the exact order in the UI review. Without the Host variables the panel still shows the machine facts and one line: "Set LOADOUT_HOST_PROVIDER and the other LOADOUT_HOST_ variables in /etc/loadout/loadout.env to describe this host."

## Data and API changes

None. No HTTP API, no store method, no wire type changes. `serverstats.Report` grows a field. Its only consumer is `internal/web`.

## Migration

No database change. On a host that is not at Hetzner the operator edits `/etc/loadout/loadout.env` (root, 0600): add the `LOADOUT_HOST_*` lines, remove the `LOADOUT_HETZNER_*` lines, restart. `make-env.sh` does not write them, as with the Hetzner lines. Old environments without the new variables keep working and show the machine facts only.

## CLI and web changes

- CLI: none.
- Web: `/account/server` as above. Still admins only, still a 404 for anyone else, works without JavaScript, no new styles beyond what `.facts` and `.panel` give. Any new token goes in `static/style.css` for light and dark.

## Order of work

1. `config`: the `Host` type, five variables, validation, `config_test` table. Nothing visible yet.
2. `serverstats`: `HostFacts`, `Host`, `c.host()`, `prettyName`, the two seams, tests. Nothing visible yet.
3. `cmd/loadout/stats.go` and `login.go`: wiring, log line, warning. `stats_test`.
4. `internal/web`: `hostSection`, `hostTile`, `firstTile`, template changes, Hetzner panels hidden when off. `ui_server_test`. UI review by the `loadout-ui` agent with `loadout-preview` screenshots (light, dark, 390 px, with and without Hetzner), recorded in `docs/UI.md`.
5. Docs: `docs/GUIDE.md` (server page section and its variable table), `docs/DESIGN.md` (env table and the `/account/server` paragraph), `deploy/README.md` ("Server page" section: host lines first, Hetzner as the optional part for a Hetzner VM), `docs/ARCHITECTURE.md` (the VM box no longer says Hetzner only). Rebuild with `docs/guide/build.sh`. `make check`.
6. Live (ready-for-human): deploy, edit `loadout.env`, restart, check `journalctl -u loadout -n 20` and the page as an admin. No backup needed for the code, since nothing migrates.

Each step passes `make check` on its own.

## Tests

- `internal/config/config_test.go`: table over the five variables: unset, trimmed, too long, control character, `Console` with `http`, `javascript:`, no host, and a good `https` link.
- `internal/serverstats/serverstats_test.go`: `host()` with a fake `Hostname` (ok and error) and a temp os-release (quoted, unquoted, no `PRETTY_NAME`, missing file, a file over 16 KiB). `prettyName` as a table. `Report` carries `Host` and the other parts are unchanged.
- `cmd/loadout/stats_test.go`: stated facts reach the report. The log line names the provider. The warning appears with a non-Hetzner provider plus a token, never the token.
- `internal/web/ui_server_test.go`: the "no token" case now wants the stated provider, the region, the contact and the console link, and wants out "Hetzner", "LOADOUT_HETZNER_TOKEN", "Estimated cost" and "€". The Hetzner case keeps its assertions and also shows the Host panel. A stated value with `<script>` is escaped. Empty stated values leave their rows out and show the one-line hint.
- e2e in `cmd/loadout`: `serve` with a bad `LOADOUT_HOST_CONSOLE` exits with a plain sentence naming the variable.

## Risks

- **An old token stays in the env file.** Then the page shows the Hetzner server next to the stated host. The startup warning and the deploy README step cover it. Step 6 removes the lines.
- **Stated facts go stale.** They are the operator's words, not measured. The panel says "as stated in loadout.env" in its footnote so nobody mistakes them for live readings.
- **A host may not use the `deploy/` systemd unit.** If it runs in a container, the host name is the container's and `/etc/os-release` is the image's. The facts are still true of what runs loadout. The footnote says so. To confirm per host.
- **Contact details on a page.** Only admins see it, and the values are what the operator chose to write.

## Not in this step

- Any provider API other than Hetzner's, metadata endpoint or token.
- A cost figure for hosts without an API.
- Deleting `internal/hetzner`: it stays as the opt-in panel for Hetzner operators.
- The `HostSource` interface (option 2).
