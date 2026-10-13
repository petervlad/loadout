# What's new in loadout

What changed in loadout, newest first. Each update lists what it added and what it
fixed. The [user guide](../) explains how to use each one.

## October 2026

### 13 October 2026
<!-- through 6b305e2 -->


- Feature: `loadout cleanup` lists the old agents, skills and knowledge bases an
  earlier setup left in your tool folders, and `--apply` moves them into
  `~/.loadout/backup/`. `--duplicates` also finds copies of names your registry does not
  offer, and `--tool vscode` covers the folders VS Code reads.
- Feature: `loadout install --adopt` moves aside the files and links an older setup left
  in the way, then installs. A link is moved, never the folder it points to.
- Feature: `loadout user add --email E --password-file F` adds an account on a
  simple-login registry, with `--admin` for an admin. An account that exists keeps its
  password.
- Feature: The built-in setup skill walks through a messy machine, including agents that
  show twice in VS Code and the `chat.agentFilesLocations` setting that fixes it.
- Feature: `loadout package delete TEAM/NAME` on the registry host lists what a package
  holds and, with `--yes`, deletes it with its releases and proposals. People who have it
  installed keep their files and `loadout sync` tells them it was removed from the registry.
- Feature: Admins delete a package from its page on the web. The registry saves a copy of
  its database first, and every delete leaves a record with the reason.
- Feature: Anyone who reads a package can ask to delete it, on its page or with
  `loadout propose delete TEAM/NAME --reason "why"`. A maintainer or owner who did not ask
  approves it on the request's page or with `loadout review approve del-ID`.

- Feature: `loadout update` updates the CLI the way it was installed. With Homebrew it
  runs `brew upgrade --cask loadout`, from a registry's download it runs that registry's
  install script again, and for the release binary or a clone it prints what to do.
  `--check` shows the command without running it.
- Feature: A registry can hand out the CLI. Deploy with `LOADOUT_DOWNLOADS=1` and people run
  `curl -fsSL https://REGISTRY/install.sh | sh`, with no Go and no clone. The download
  is checked against `checksums.txt` and uses the machine's normal HTTPS trust.
- Feature: A CLI installed from a registry names that registry's install line in the
  "newer loadout" message.
- Feature: New installs follow the `auto` update policy by default.
- Feature: A registry that serves its own copy of the docs gets a working "loadout" link
  back from them.
- Feature: The built-in setup skill tells an AI tool to run `loadout update` when asked to
  update loadout.

### 12 October 2026
<!-- through 9a4ea7f -->

- Feature: The members list on a project or team's **Settings** page is sorted: owners
  first, then maintainers, then members, each by name.
- Feature: A search box in the **Members** panel finds people by name, email or team
  name, and filters **Through the project** at the same time. It filters as you type;
  without JavaScript, press Enter and the page reloads with the matches.
- Feature: Under each person's name a muted "Teams: a, b" line lists their other teams
  in the same project, only those you may open.
- Feature: Changing a role or removing someone keeps your search. Adding someone clears
  it, so the new person shows.

### 5 October 2026
<!-- through 71fac77 -->

- Feature: A team's wiki page can end with a **Course**: lessons from a knowledge base
  the team publishes, listed in order. Each lesson opens on its own with **Previous**
  and **Next**, and has a link you can send to someone.
- Feature: Owners choose the course on the team's **Settings** page, next to the wiki
  switch, or with `loadout namespace set TEAM --course TEAM/NAME` (`--course none`
  removes it). A team without a course of its own shows the project's, marked
  "From PROJECT".
- Feature: A `quiz` block in any Markdown document (lessons, package previews, skill
  pages and wiki pages) shows a question whose answers fold out, each saying whether it
  is right and why. A quiz that cannot be read says so and names the line.

### 4 October 2026
<!-- through b59f0c6 -->

- Feature: Wiki pages fold at each `##` heading: a page opens with its title, its
  first lines and a list of its sections, and **Open all** opens every one. A link to a
  heading opens the section it is in.
- Feature: This What's new page lists what each update added and fixed, newest first.
  The **Guide** and **What's new** switch at the top of the docs moves between the two.
- Feature: The registry sidebar links to What's new, next to Docs. On a phone both
  show as icons in the top bar.
- Feature: A proposal now remembers the release it was made from. When a newer release
  comes out first, its review page shows **Behind 3.0.0**, says who published that
  release and which files it changed, and `loadout review list` reads "open, behind
  3.0.0".
- Feature: The diff of a proposal that is behind has three views: the proposal's own
  changes, what was released since, and what approving it would release.
- Feature: Approving a proposal that is behind takes an explicit **Approve over 3.0.0**
  on the web, or `loadout review approve ID --over 3.0.0`, so nobody drops a newer
  release's changes by accident.
- Feature: `loadout rebase` brings a folder or a proposal up to date with the latest
  release on your machine. It merges line by line and marks conflicts for you to
  resolve; proposing again replaces your old proposal and keeps its prompts.
- Feature: `loadout propose` and `loadout publish` refuse a folder that is behind the
  latest release. Run `loadout rebase` first, or add `--base latest` to publish over it
  on purpose, for example from CI.
- Feature: Releases show who published them.
- Feature: When your edits to an installed agent, skill or knowledge base hold back an
  update, `loadout sync` lists the files in the way and what to do, and remembers it.
  `loadout status` and the new `loadout notices` show it later without going online.
- Feature: Claude Code and Copilot tell you at the start of a session when an update is
  held back by your edits. `loadout login` sets this up; if you are already signed in,
  run `loadout setup --hooks`. `loadout setup --no-hooks` turns it off and leaves your
  settings file as it was.
- Feature: `loadout install TEAM/NAME --merge` takes the new release and keeps your
  edits, line by line. Where both changed the same lines it marks them for you to
  resolve.
- Feature: `loadout sync` exits with code 2 while your edits hold back an update, so a
  cron job can tell it apart from a registry problem.
- Feature: Pushover tells you about each new version once, not on every sync.
- Feature: loadout now has releases, starting with 0.1.0. `loadout version` names the
  release your CLI was built from.
- Feature: When a newer loadout is out, a line after your command says so, at most once a
  day, with the command to update. When an agent runs loadout, the line asks it to tell
  you. `loadout status`, `loadout notices` and the start of a Claude Code or Copilot
  session show it too. Set `LOADOUT_UPDATE_NOTICE=off` to turn it off.
- Feature: If your loadout is too old for your registry, every command says so.
- Fix: On phones, the project fold arrow no longer shows at the right edge of the top
  bar.
- Fix: A proposal's page and `loadout review show` compare it with the release it was
  made from, so changes released in between no longer look as if the author deleted
  them.
- Fix: The **loadout docs** logo takes you back to the registry page you opened the docs
  from, also after you move around the docs, reload, or come in through the install link
  on **Start here**.

### 3 October 2026
<!-- through 7158a8e -->

- Feature: Each project and team can have a wiki: one page its members write and the
  whole team reads, with **Write** and **Preview** while editing, a history of every
  save and a way back to any older revision. An owner turns it on in **Settings** or
  with `loadout namespace set NAME --wiki on`.
- Feature: If someone saves the wiki while you are editing, your save is held back and
  the page shows your text again with their change above it, so nothing is lost.
- Feature: `loadout wiki pull`, `push`, `log` and `show` let you write the wiki in your
  own editor. `pull` never replaces a file with changes that are not on the registry.
- Feature: The Members tab is now **Settings**: the team's people, review, the wiki
  switch, machine tokens and models in one place. Old links to Members still work.
- Feature: The **Start here** page (`/skills`) explains loadout to newcomers: what it
  is, the words it uses, the first commands for this registry, and loadout's own skills.
- Feature: A drawio block in a Markdown file is drawn as a diagram, like a mermaid block.
- Feature: The models page warns that Copilot may ignore the model set for an agent
  when HydraFusion is chosen in its model picker, as it does on Auto.

### 1 October 2026
<!-- through 696ee4a -->

- Feature: The logo opens a short home page: what loadout does in one line, a diagram
  of how it works, and loadout's own skills.
- Feature: The **Reviews** tab says whether the team requires review.
- Fix: A form with a mistake now marks the field to fix, and an error on the server
  shows a page with a way back.
- Fix: Choosing from a menu with the arrow keys, such as **Sort by**, waits for Enter
  instead of reloading the page at every step.
- Fix: Pages load faster on a second visit: the browser keeps the styles and scripts
  until they change.

## September 2026

### 30 September 2026
<!-- through 6620b4b -->

- Fix: Release sizes on a package page show in KB and MB instead of bytes.

### 29 September 2026
<!-- through d6430e5 -->

- Feature: Accounts. Sign up and sign in on the registry's website with email and
  password plus a mailed code, or with Google. `loadout login` signs in the CLI through
  the browser, and `loadout logout` signs it out.
- Feature: One sign-in covers every team you belong to on a registry and the project
  above them. What you may do follows your role in each team at that moment, so a new
  role needs no new login.
- Feature: Roles: member, maintainer, owner and admin. Maintainers add and remove people
  on the website or with `loadout member`; owners of a project are owners of every team
  in it.
- Feature: Projects: every team belongs to a project, whose packages all its teams read.
  Admins create projects and owners create teams in them on the website; owners can
  also use `loadout namespace create`.
- Feature: The registry's website: a sidebar of your projects and teams, the packages
  each team reads, proposals with both versions' answers side by side and the diff,
  approve, reject and withdraw, members, machine tokens and an account page with a light
  or dark appearance.
- Feature: The website follows the system's increased contrast setting, and its buttons
  are easier to tap on touch screens. Remove, revoke, reject and withdraw ask
  before they act.
- Feature: Skills and knowledge bases join agents as package kinds. `loadout new skill`
  and `loadout new kb` start one; a knowledge base gets an index loadout writes for you,
  and may hold Mermaid and draw.io diagrams.
- Feature: Agents can use the `skill` tool, which grants Skill in Claude Code.
- Feature: `loadout import` turns the agents, skills and folders of documents you
  already have into packages, ready to publish.
- Feature: Filter and sort packages by kind and by layer, on the team page and with
  `loadout list --kind`, `--layer` and `--sort`.
- Feature: `loadout status` shows one line per skill folder; `--files` lists every file.
- Feature: The user guide is online at `/docs`, with a Docs link in the registry's
  sidebar. The guide's title takes you back to the page you came from.
- Feature: `loadout review TEAM/NAME` runs the version you have and the newest proposal
  on the proposal's prompts in your own AI tool, next to the team's other packages, and
  attaches the report to the proposal. `--safe` refuses anything that would need your
  approval.
- Feature: Review answers show as formatted text on the website; long ones show their
  first 150 words, and **Show whole answers** opens both.
- Feature: Anyone on the team can add prompts to a proposal on its page, and the next
  `loadout review` asks them too.
- Feature: A project owner can release their own proposal without a second person's
  review, with a note saying why. The release is marked "Released without review" for
  everyone to see.
- Feature: Projects choose which model and reasoning effort each AI tool runs for the
  `fast`, `balanced` and `deep` hints, and teams inherit or override them, on the
  **Choose models** page or with `loadout namespace set`. `loadout sync` applies a change
  like a new version.
- Feature: Invite someone to a project and any of its teams in one step, on the website
  or with `loadout member invite`. They get a sign-in link, by mail when the registry
  sends mail.
- Feature: Admins get a **Server details** page: estimated monthly cost, disk, backups,
  and how many projects, teams, packages and accounts the registry holds.
- Feature: loadout's own skills teach your AI agent to use loadout. `loadout login`
  installs them, and anyone can read them on the website.
- Feature: Download any release as a zip, or a single file, from the package page.
- Feature: Open a package's files in the browser: Markdown is formatted, and Mermaid and
  draw.io diagrams are drawn, with a tab per draw.io page.
- Feature: Login tokens expire after 90 days. **Sign out everywhere** on the account
  page, or `loadout logout --all`, ends every session and token at once.
- Feature: Machine tokens for CI can propose but never approve or reject, and count as
  the person who made them.
- Fix: `loadout login` opens only the registry's own sign-in page, and asks before
  saving a token for a team you did not name.
- Fix: Sign-in is better protected: setting a password signs out your other sessions,
  a new code replaces the one before it, and an old sign-up code can no longer change an
  account.
- Fix: After you leave a team, `loadout list` and `loadout install --all` skip it and
  carry on with your other teams.
- Fix: loadout never writes or deletes through a skill folder that is a symbolic link.
- Fix: Fields in a skill's frontmatter that loadout does not use, such as
  `allowed-tools`, are kept as written.
- Fix: A skill's description may be 1,024 characters long in any language; it was
  counted in bytes before.
- Fix: `loadout status` no longer reports a skill's files as missing when a closer
  layer's skill of the same name shadows it.
- Fix: Updating a skill in which a file became a folder, or a folder a file, works.

### 28 September 2026
<!-- through 7b0a702 -->

- Feature: The first release. Write an agent once, in one tool-neutral format, and
  loadout installs it into Claude Code and GitHub Copilot, translating its tools and
  model for each. `loadout new`, `install`, `uninstall` and `status` cover a single
  machine.
- Feature: A registry for the team: `loadout serve` runs one on any always-on machine,
  `loadout publish` shares a package, and teammates `list`, `install` and `sync` it.
- Feature: Each installed package has a policy for updates: `notify` (the default),
  `auto` or `pin`. `loadout sync` can also send a phone notification through Pushover.
- Feature: `loadout diff` shows what changed between versions, and `loadout rollback`
  goes back to an older one and pins it.
- Feature: Layers: a team reads the packages of the namespace above it, and when both
  ship a package with the same name, the team's wins. `loadout layers` shows the order;
  `loadout install --all` installs everything.
- Feature: Join a team with an invite: `loadout team join`, `list` and `leave`.
- Feature: Proposals: `loadout propose` uploads a candidate with the prompts you tested
  it with, and a teammate compares it with the current release in their own AI tool
  before approving it.
- Feature: Your own files are safe: loadout never overwrites a file it did not write,
  or one you edited, without `--force`, and it works when your tool folders are
  symbolic links into a dotfiles repository.
