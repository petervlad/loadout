# UI

The web UI (browse layers, agents and teams, review proposals side by side, login) is designed with the Apple Human Interface Guidelines as the reference.

- Design reviewer: [dickwu/apple-design-skill](https://github.com/dickwu/apple-design-skill), an agent skill grounded in Apple's HIG. Install it in your own AI tool and run it on every UI change:
  `npx skills add dickwu/apple-design-skill -a claude-code` (or clone it into `~/.claude/skills/apple-design`).
- The repository has no license. Use it as a review tool only. Do not copy its text or reference files into this repository.

## Width on large displays (September 2026)

On a 32 inch 4K display the packages list stopped at 1240 px next to the sidebar and left half the window empty, while descriptions were cut after about 80 characters. Checked with the design skill as a checklist (layout, going full screen, lists and tables, typography): the packages list with two long descriptions, a package page, the reviews list, a proposal, its full diff, the Members page, the account page and a loadout skill page, in light and dark, at 390, 1000, 1440 and 2560 px, and without JavaScript (the change is CSS only).

What changed:

- List pages (packages, reviews, members) grow to 1800 px (`.main-wide`, set by the handler with `uiView.Wide`). All the extra room goes to the first column. At 2560 px a 230 character description fits on its line.
- Follow-up: with fixed `colgroup` widths the packages columns were sized for their longest possible value, so the gaps after short values ranged from 85 to 135 px at 4K. The packages and reviews tables are now `.list.fit`: the other columns shrink to their content (`width: 1%`, no wrapping) and every gap is the same 40 px of cell padding, while the first column takes the rest and still truncates. A long layer name truncates in its pill (160 px, 96 px between 1001 and 1100 px where the table is tightest) with the full name as its tooltip, and a long "Proposed by" at 220 px. At 761 to 1000 px the reviews list gives the proposal column more room than before, when the fixed columns left it about 20 px.
- The Members tables keep their fixed columns: the people, "Through the project" and tokens tables sit one above the other and their columns line up only because the widths are shared. The header's search, Sort by and filters stay on the table's right edge, since they sit in the same box. The cap keeps a row scannable on a 5K or 6K screen.
- Reading pages (a package, account, loadout skills, New, Models) keep 1240 px.
- The proposal and diff pages have no sidebar and had no cap, so at 2560 px each answer column was over 1000 px wide. They now stop at 1800 px and center, which keeps each column near 700 px.
- Rendered Markdown (answers, skill pages) keeps its paragraphs, lists and headings to 72ch, about 80 characters, however wide the column. Code blocks and tables still use the whole column.
- The Add field on a team's Members page stops at 560 px, like the invite form, so the role and Add member stay next to the address instead of 1100 px away.

Left as it is:

- At 1440 px and below nothing moves: the window minus the sidebar is already under 1240 px. Phones and the 1000 px stacked rows are unchanged.
- Package pages show agent files as source (`.markdown`, monospaced, wrapped), not prose. They keep the 1240 px page, which is under 130 characters of source per line.
- One-line hints under panels (the Add form's note) span the wider panel. They are single sentences, not a text to read line after line.

## Download and preview (September 2026)

The package page gets a Download button and per-release download links, older versions through `?version=`, and a preview page per file (`.../{version}/files/{path}`) with Mermaid and draw.io diagrams drawn in sandboxed frames. Checked with the design skill as a checklist (buttons, links, segmented controls, lists and tables, writing, accessibility, color, dark mode): a knowledge base with linked Markdown, a document with two Mermaid diagrams, a `.mmd`, a three-page `.drawio`, plain text and a skill's manifest, an agent at its newest and an older version, in light, dark (chosen and from the system), at 390, 800 and 1280 px, and without JavaScript. The screenshot script also recorded what the page and every frame logged: no CSP violations and no errors in any of them.

What it is:

- The header panel's **Download 1.4.0** is a link with the rest of its name visually hidden ("Download 1.4.0 of lime/planner as a zip"), so the visible words start the accessible name. Release rows and file rows end in a download icon link with a hidden label ("Download 1.3.0 as a zip", "Download ops/deploy.md"). `.icon-btn` makes them 44 px under a coarse pointer. A version is a link to that release's page. The shown one is `aria-current="page"` in the text color.
- An older release says "You are looking at 2.2.0. The newest is 2.3.0." in its own panel (`--text-2` on the panel, 7.9:1 light, 10.1:1 dark), `role="note"`, and its install command names the version (`loadout install lime/planner@2.2.0`).
- The preview page keeps the sidebar and drops the team header: a back link to the package, the path as the one `h1` (it may break after a slash, not inside a name), then the version, size and kind ("draw.io diagram, 3 pages"). One panel holds the preview, headed Preview or Source, with Show source (Markdown only) and Download. Previous and Next are cards with a visible word and the path, in reading order, side by side and stacked full width on phones.
- A diagram is a frame 100% wide whose height the frame reports (clamped 120 to 3,000 px), so the page never gets a second scrollbar. Its source folds under it as **Diagram source**. Without script the source shows, with "Diagrams are drawn with JavaScript on." in `--muted` (5.1:1 on the panel). A frame's title names the diagram ("Diagram 2 in how-it-works.md", "Diagram: architecture.drawio, page Deploy").
- draw.io pages are a segmented row of links (the tabs' tokens), in a `nav` labelled Pages, the current one `aria-current="page"`, wrapping on phones, 44 px under a coarse pointer. Each is a real link to `?page=N`, so they work without script. The viewer's own arrows and zoom sit in the frame.
- Mermaid follows the theme (its `default` and `dark` themes). draw.io diagrams keep their author's colors on a white canvas (`--diagram-canvas`, the same in both themes) inside the page's hairline border, rather than being recolored.
- Notes a diagram needs ("Links inside the diagram do not open here...") sit under it in `--muted`. A long file's note ("Showing the first 512 KB of 1.8 MB...") uses `--warn-text` on `--warn-bg` (about 6.5:1 light, 8:1 dark).

What changed while building it:

- Frames first showed a scrollbar of their own: the page's `border-box` put the frame's borders inside the reported height. The frame is `content-box` now, and it measures its body (and the drawing, which the draw.io viewer lets overflow its container) rather than the document, which never shrinks below the frame.
- In dark mode the draw.io frame showed a dark strip below the white drawing. The frame's own background is white for draw.io in both themes.
- On phones the release rows pushed the download icon onto a line of its own. Below 760 px the Size column of the releases is hidden (the zip link and digest matter more) and the header's Download wraps under the description.
- A diagram file showed two "Show source" controls (the page's and the fold under the frame). Diagram files keep only the fold. Markdown keeps the page's button, and the fold under each of its diagrams is called Diagram source.
- The source view of a Markdown file said "Text" as its kind. It says Markdown.

Left as it is:

- The draw.io viewer's toolbar (page arrows, zoom, fit) is its own, light gray in both themes. Restyling it would mean patching the vendored library.
- Frames load lazily. A page with many diagrams draws them as they scroll into view.
- A page tab's focus ring is the global `:focus-visible` outline, checked in the stylesheet rather than on a screenshot (headless Chrome does not show focus rings for focus set by script).

## loadout skills (September 2026)

The new pages `/skills` and `/skills/{name}` show loadout's four built-in skills, from the same embedded files the CLI installs, and a sidebar entry leads to them. Checked with the design skill as a checklist (sidebars, lists and cards, writing, accessibility, color): signed in as a maintainer, as a member and signed out, in light and dark, at 390 and 1280 px, and without JavaScript (only the Copy button needs it, the command is selectable text).

What it is:

- The sidebar entry sits above the projects for everyone who is signed in, styled like a project row: a sparkle icon of its own and a small outlined "Built in" word where a namespace shows L0 or L1, so it never reads as a layer. On phones the word hides like the layers do and the icon tells it apart.
- Signed out, the pages keep the app's frame with a sidebar that has only this entry, Docs and "Sign in" (back to the same page after signing in). The sign-in pages' narrow card was the other choice. A 400 px column is too narrow for Markdown with tables and code.
- `/skills` shows one card per skill, in the order a person meets them (setup, author, review, admin): the name, what it does and, in `--muted`, when the agent loads it, both taken from the skill's own description. The name is the one link. The whole card is its target (a stretched link), so a screen reader hears the name, not the whole card. Two columns, one below 760 px.
- A skill's page renders `SKILL.md` and each reference file with the safe Markdown renderer of review answers (raw HTML shown as text, dangerous links dropped, images as their alt text). A relative link to one of the skill's files leads to that file's section on the page. The install card (`loadout setup --skills`, with Copy) and the CLI version the text belongs to close both pages.

What changed while building it:

- The command box shrank to its minimum next to the long sentence and broke `--skills` across lines. A short command now stays on one line (`.cmd-short`) and wraps under the text only on phones, like the other command cards.
- Cards were first in name order (admin first). A newcomer's first question is setup, so the order is fixed in `internal/builtin`, which the CLI's messages use too.

Left as it is:

- Headings inside a skill render at the answer sizes (`.md`), smaller than the page title, so the page keeps one `h1` visually on top even though the Markdown has its own.

## Invite someone, on a project (September 2026)

A project's Members page replaces its one-line Add form with **Invite someone**: email, the role in the project, and the project's teams, each a checkbox with its own role picker. Checked with the design skill as a checklist (entering data, text fields, toggles, pickers, lists and tables, feedback, writing, accessibility): as the owner of acme with three teams, as a project maintainer (no teams), the result after a new person, after raising an existing member and after a repeat that changed nothing, and the sign-in page opened from the link, in light and dark, at 390 and 1280 px, and without JavaScript (the form is a plain POST, the result is a rendered page). Contrast is from the tokens: the hints use `--muted` on `--panel` (5.1:1 light, 6.6:1 dark), the link line `--text-2` (7.9:1, 10.1:1).

What changed while building it:

- Each team row is one line: the checkbox and team name on the left (the whole label is the tap target, 44 px tall), its role picker on the right, with a visually hidden label ("Role in lime"). The list and the email row are 560 px wide, like the New page's form. Stretched across a 1280 px panel, the pickers ended far from their teams.
- A role picked for an unticked team was dropped without a word. With the script, choosing a role ticks that team (`data-ticks`). Without it, nothing is ticked for you and the result table shows exactly what was given.
- Only roles the viewer may give are offered: owner in the project only for admins, maintainer in a team only for those who govern it. A project maintainer gets no team list and a line saying who adds people to teams. A project with no teams says so and points to New team.
- The result replaces the form, so the page does not show a filled form next to its outcome: a heading ("dana@acme.example is invited", or "Nothing to change for ..."), a table of where, the role now and what changed ("Added", "Raised from member", "Unchanged, already higher"), the sign-in link in a code box with Copy (script only, the text is selectable without it), and one sentence saying whether it was mailed. "Invite someone else" leads back to the empty form. The heading row is `role="status"`.
- The sign-in page opened from the link first showed two sign-up prompts ("First time here?" in the card and "New here?" below it). With a prefilled address only the one in the card stays, and its link keeps the address. No `autofocus`: it would skip the line that tells a new person to create an account first.

Left as it is:

- The result is rendered on the POST, not redirected to. Sending it again is harmless: nothing changes and nothing is mailed, and the page says so.
- The result and the mail never say whether the address had an account. "They get access when they sign in with this address" is true either way.
- On a team the one-line Add form stays: a team has no teams below it to choose.

## Review of prompts on the proposal page (September 2026)

Checked with the design skill as a checklist (accessibility, entering data, popovers, alerts, writing): the proposal page with five prompts by three people and one removed, as a reviewer with one prompt of their own and as the author with three, its Edit open, its Remove popover open, the team packages folded and open, the page after release and the reviews list, in light and dark, at 390 px and 1280 px, and without JavaScript. Contrast is from the tokens.

What changed:

- A Prompts panel under the answers lists the active prompts, numbered, each with who added it and when. Prompt text is escaped with its line breaks kept (`pre-wrap`), never Markdown, since anyone on the team may add one. A prompt the shown report did not ask carries a plain "Not in this report" pill. The words carry the meaning.
- Only the adder gets Edit and Remove. Edit opens in place below its row (details and summary, no script), with Cancel and Save, and hides Remove while open. Remove asks first in the existing popover with Cancel, since removal cannot be undone. On the last prompt Remove is disabled and tied with `aria-describedby` to a line saying why. The server refuses too and says "A proposal keeps at least one prompt." After a change the page returns to `#prompts` (the panel takes `tabindex="-1"`) and the notice sits inside the panel, not at the top of the page where it would be off screen.
- The add form is a visible labeled textarea (`maxlength` 4096, `required`), with its hint tied by `aria-describedby`. At 20 prompts it gives way to a sentence saying so. A closed proposal shows the prompts read-only, with "N prompts" instead of "N of 20" and a line saying they no longer change.
- The answer columns name each side's source ("Installed · 2.3.0", "Proposal · 2.4.0"), keeping the existing "Label · version" form. Older reports keep "Current" and "Candidate". The bar adds "Full access" or "Safe mode". The team packages beside each side are a folded disclosure with the sidebar's chevron (rotation only under `prefers-reduced-motion: no-preference`, 44 px under a coarse pointer). The tool's notes show on `--warn-bg` with `--warn-text` (about 6.5:1 in light, 8:1 in dark), marked `role="note"`.
- Header and reviews list showed "2.4.0 → 2.4.0" after release. They now compare with the release below the proposal's version, so a released proposal reads "2.3.0 → 2.4.0" and its diff still shows the change. The list's Reports column said "None yet" for every proposal because listings did not load reports. They do now.

Left as it is:

- In rows with buttons the "Added by" line sits a few pixels lower, centered on the 32 px buttons. Aligning it to the text row would push the buttons off the line on narrow widths.
- Activity quotes cut prompts at 60 characters. A prompt that contains quotes of its own shows them inside the curly quotes. They are escaped text.
- On phones the Prompts panel comes before What changed and Try it, following the order of the columns on desktop.

## Review of Approve as owner (September 2026)

Checked with the design skill as a checklist (popovers, buttons, alerts, color, accessibility): an owner's own open proposal, its popover open, the proposal after release, the reviews list and the package page, in light and dark, at 390 px and 1280 px, and without JavaScript.

What changed:

- On phones the actions sit on the left, and a popover anchored to its button's right edge ran off the screen (Reject had the same problem). Below 760 px the popovers of the proposal actions now open from the row's left edge.
- "Released without review" did not fit the Status column of the reviews list. The column is 190 px now.

Checked and left as they are:

- The button on the page ends in an ellipsis ("Approve as owner…") because it opens a form, and the form's button says what happens ("Approve without review"), with Cancel beside it, as for Reject and Withdraw. It keeps the primary style: releasing is what the owner chose, and it is not destructive (a later version replaces it).
- The warning in the popover uses `--warn-text` on the panel (about 7:1 in light) and the note is `required`, so the browser refuses an empty note before the server does.
- "Released without review" uses the warn pill, like "Waiting for review". The words carry the meaning, the color only draws the eye. The activity dot is decoration next to the same words.

## Review of the answer preview (September 2026)

Checked: the proposal page with a 390-word candidate cut to 150 words next to a 129-word answer shown whole, the full view, and a prompt whose answers are all short. Looked at in light and dark, at 390, 800 and 1280 px, with the keyboard and without JavaScript (the control is a plain link).

What changed:

- Each answer over 180 words shows its first 150, cut on the Markdown tree so a table or code block is never split. One link under both columns switches both, so the two answers stay comparable (one disclosure control per view). It reloads with `?full=1#answers`, and `.sides` takes `tabindex="-1"` so the next Tab after landing is the link. The prompt picker keeps the chosen view.
- A cut column ends with "N more words" in `--muted` (4.9:1 on `--panel-alt` in light, 6.3:1 in dark). The bar sentence uses the same token. It is text, not a second tab stop.
- The bar first said "Showing the first 150 words of each answer", which was untrue when only one side was cut. It now says "Long answers show their first 150 words."
- The word count joins time and tokens in the column head. Up to 1100 px the head wraps the stats under the title instead of breaking "Current · 2.3.0" across lines.
- On phones the answers panel sized itself to its content (492 px at a 390 px screen, because of the prompt picker and long code lines), so the page scrolled sideways. It now stretches to the screen and code scrolls inside itself.
- Markdown tables in answers no longer break words inside cells ("Respon se"). A table too wide for its column scrolls.

Left as it is:

- The link sits under the columns, not above them: it acts on what was just read, and the "more words" line shows which side was cut. In the full view the link to go back is at the end of the longer answer. Landing on `#answers` keeps the jump short.
- Inline code in answers still breaks anywhere in the narrow 761 to 1000 px columns (`nightly/bundle.p` / `y`). Keeping it whole would push the column wider than the panel.

## Review of the newer pages (September 2026)

Checked: the New project or team page, the sidebar with folding projects and the Docs item, the New team button, the account page's Sign out panel, the packages list's kind badges, filters and Sort by, the skill and KB package pages, the multi-file proposal diff, and the static docs page. Each was looked at in light, dark, at 390 px, at 800 and 1024 px, with touch input, with the keyboard and without JavaScript. Contrast was computed from the tokens in `style.css`.

What changed:

- Without JavaScript, the teams of a folded project were hidden with no way to open them. The server now marks a folded list with `data-folded`, and only `.js` hides it, so every team stays reachable.
- On phones the Docs item hid its word with `display: none` and was left with no accessible name. The word is now only visually hidden.
- The Docs item used `--muted` on the sidebar (4.3:1 in light). It now uses `--muted-2` (5.7:1). The docs page's "docs" next to the logo got the same change.
- The counts in the kind filter were `--muted` on the segment background (4.0:1 in light, 3.4:1 on the selected segment in dark). They now use `--text-2` (6.2:1 and 5.2:1).
- The fold arrow rotated even with reduced motion. The rotation now sits in the `prefers-reduced-motion: no-preference` block. Its focus ring no longer overlaps the layer label, and with a touch pointer it is 44 px like the other controls. The kind filter links also get 44 px on touch screens.
- The Kind column made the packages list too wide between 761 and about 1100 px, so package names broke one letter per line. The Kind column is narrower, the Releases column is hidden below 1100 px, and below 1000 px the rows stack as they do on phones.
- On phones the file list of a skill kept its 48 px row height per cell, which left a gap under every file name. It now uses the stacked phone rows.
- In diffs, each file name is now an `h3` in the text color with a separator above it, so a multi-file change reads as one section per file and screen reader users can jump between files. The full diff page lines up with its heading (20 px), and so does the main file view on the package page.
- Sign out everywhere ends every session and every login token at once. It now asks first in a popover with Cancel, the same way Remove and Reject do.
- The project picker on the new page is tied to its hint with `aria-describedby`.
- Docs page: the phone contents list lost the article's list indent. Its Contents summary uses the same chevron as the chapters and is 44 px tall. Contents links and the Open all and Close all buttons are 44 px on touch screens and the buttons have a visible border (`--field-line`). The page follows increased contrast like the registry. Open all and Close all moved from above the title to just above the first folding chapter, next to what they act on.

Checked and left as they are:

- Tab order runs skip link, logo, sidebar, main content, in reading order, and every control shows the focus ring. Chapter summaries in the docs contain real `h2` headings, so they keep their ids and their place in the contents.
- No page scrolls sideways at 390 px. Code blocks and the sidebar strip scroll inside themselves.
- Kind badges use `--text-2` on the panel (7.9:1 light, 10.1:1 dark). Their outline is decoration, the word carries the meaning.
- The Create button stays one word: without script it cannot say which kind it creates, and the radio choice right above it does.

## Members through the project (September 2026)

A team's Members page listed only people stored on the team, so a team run by its project's owners looked empty. It now has a read-only panel, Through the project, with the owners of each project above ("Owner, from acme") and a link to that project's Members page when the viewer can open it ("Manage in acme", or "View in acme" for a project member). Project members and maintainers get no rights below, so they are not listed. A team with no stored members shows "Nobody is added to wombat directly" instead of a table with only headers, and says the project's owners already manage it when there are some. Checked in light, dark, at 390 px and 1024 and 1280 px, and without JavaScript (the panel uses no script).

- The inherited rows reuse the member row (avatar, name, sign-in) and span the Role, Joined and Remove columns with the role text, so row lines run the full width and the role lines up with the table above.
- Left as is: the panel has no table header row, like the tokens table. The panel heading names the rows. The link is a normal `.btn`, so it gets the focus ring and 44 px on touch screens.

## Models for agents (September 2026)

The new page `/t/{ns}/tiers`, reached from **Choose models** (owners) or **See models** (everyone else) on the Members page. Checked with the design skill as a checklist (forms, text fields, pickers, tables, color, accessibility): lime as an owner of acme and as a maintainer, acme as its owner, in light and dark, at 390, 800 and 1280 px, with the keyboard and without JavaScript (the page uses no script).

What changed while building it:

- The plan drew one table with a column per tool. With a text field and an effort picker per tool that is four controls a row, too wide for 800 px and unusable at 390. Each tool now gets its own panel (Claude Code, GitHub Copilot) with the same three rows, and a new tool is one more panel.
- On phones the rows stack and the column heads are hidden, so each field shows a small visible label ("Model", "Reasoning effort") there. On wide screens the label is visually hidden and the column head names it. Screen readers hear the whole name ("GitHub Copilot model for Deep") everywhere.
- The first draft had a Back to members button in the intro panel. On phones it squeezed the text into a narrow column, and the Members tab already leads back. It is gone. The save panel wraps its button under the sentence on phones.
- The hint descriptions were cut with an ellipsis ("Planning and hard..."). They wrap now.
- The inherited effort first read "Inherit high, from acme" and was cut in the 190 px column. It reads "high, from acme", the same wording as the model placeholders.
- The Save button is always shown, not only after a change (`data-dirty`), because a text field reports its change only when it loses focus.

Checked and left as they are:

- Inherited values are placeholders in `--muted` (about 5:1 on the panel in light, higher in dark), so a value the namespace sets itself (text color) stands apart from one it inherits. The placeholder always says where the value comes from, so no information is carried by color alone.
- Copilot's two notes (Auto ignores agent models, a model not on the plan falls back silently) sit under the Copilot panel in `--text-2`, where an owner reads them before relying on a value.
- Fields and pickers are 34 px with a pointer and 44 px on touch screens, like the other forms.

Follow-up (September 2026): the page opens only for maintainers, owners and admins, so the read-only view and the "See models" link are gone. Members get no link and a 404, and `sync` still applies the settings for them. A "Members" back link, like the review pages' "lime / Reviews", sits above the first panel.

## Server page for admins (September 2026)

The new page `/account/server`, reached from **Server details** on the account page, which only registry admins get. Anyone else gets the usual "Nothing here" 404. A sub-page, not a section of Account: Account is about the person, the server page about the whole registry, and it would double the account page's length. It uses no script. Checked with the design skill as a checklist (accessibility, color, layout, gauges, writing), filled from a fake Hetzner API, with Hetzner not configured and unavailable, in light and dark, at 390, 900 and 1280 px, and signed in as a member (404, no link).

What it is:

- Four tiles on top answer the questions an admin opens the page for: estimated cost, disk, last backup, uptime. Below 1000 px they wrap two by two. Panels follow in pairs (Hetzner server and cost, storage and backups, registry counts and mail with process), one column below 1000 px.
- Numbers use tabular figures. Sizes are in powers of 1024 labelled KB, MB, GB, TB, as Hetzner and `df` do, so "40 GB disk" and "20 TB included" match Hetzner's own words. Money is "€4.75".
- The disk is a `<meter>` with low 80 and high 90. The fill turns `--warn-text` and then `--bad-text`, and the text above it says the numbers, so color never carries the meaning alone. A tile marked warn or bad says why in its note ("Filling up: 49.9 GB free", "Older than a day and a half").
- The cost is a table with the total in its footer, and a footnote that it is an estimate from list prices with VAT, since the Cloud API has no invoices.
- A part that cannot be read keeps its panel and says why in a sentence ("Hetzner unavailable: Hetzner did not answer within 5 seconds."). "Hetzner not configured" says which two variables to set and where.

Contrast, from the tokens: warn tile values #8a4600 on #ffffff about 7:1 and #ffb35c on #1c1c1e about 10:1. Bad #b3261e on white about 6.5:1 and #ff6b61 on #1c1c1e about 6:1. Notes in `--muted` about 5:1 in light. The meter fill against its track (`--segment`) is about 4.5:1 or more in both appearances.

Checked and left as they are:

- Long data and backup paths wrap anywhere inside their column. On the real server they are short (`/var/lib/loadout`).
- The Hetzner panel is taller than the cost panel beside it, leaving space under the cost. Aligning both to the top keeps the total where the eye expects it.
- The page has no refresh button: reloading it is the refresh, and the footnote says Hetzner figures are kept for 5 minutes.

### Host panel (October 2026)

The page no longer assumes Hetzner. A Host panel is always drawn, first, beside Storage: stated provider, region, size and contact (rows left out when unset), then the machine's own host name, system and CPUs, the registry's address and, when set, a link to the console with a sentence that it may open only from the company network or VPN. The first tile is Host (provider, else host name, region as note) unless a Hetzner token is set, when it stays the cost. The Hetzner and cost pair is drawn only with a token. Without one there is no "not configured" nag. No new styles: the panel reuses `.facts`, `.panel-note` and `.panel-foot`, and the lower panels now pair as backups and registry, then mail and process, so no row is left half empty. The stated values are marked as stated, not measured, in the panel's footnote. Checked against the rules in this file by reading. Screenshots in light, dark and at 390 px are still to be taken with `loadout-preview`.

## Home page and a UX pass (October 2026)

Clicking the logo opens a home page instead of the first team: one line on what loadout is for, four reasons (write once, fixes reach everyone, review, no AI on our side), the three kinds and the three layers in two panels side by side, and the set-up command. Built only from existing parts (panels, setting rows, the command card), so it reads like the account page.

Then a pass with the MIT-licensed [rampstackco/claude-skills](https://github.com/rampstackco/claude-skills) (accessibility-audit, form-strategy, information-architecture, journey-mapping, performance-optimization) as a checklist, keeping every rule above:

- A form error now names its field: the field gets `aria-invalid`, is described by the banner (`#form-error`) and takes focus, with a `--bad-text` border. New team name, Add member, Invite and the Models page (the first wrong cell).
- Pickers that apply at once (sort, prompt, run) no longer reload on every arrow key: after a key they apply on Enter or when focus leaves, like a macOS pop-up button.
- On a proposal, why Approve is disabled and what approving does sit under the buttons, not at the end of the Activity panel.
- With no teams, someone who may create a project gets a button for it instead of "ask a maintainer".
- A server error is the site's own page, with dark mode and a way back, not plain text.
- "Who to ask" is one sentence everywhere: ask a maintainer of the team to invite you. The device page no longer shows a CLI command.
- `style.css`, `ui.js` and the frame files are linked with a content hash (`?v=`) and cached for good. Unversioned requests revalidate with an ETag.
- Focused fields keep a transparent outline, so Windows High Contrast still shows focus.
- The Reviews page says whether review before release is on, with a link to the setting for those who may change it.

Not taken: wizards and progress bars, floating labels, confirmations on non-destructive actions, validation on every keystroke, CAPTCHA, analytics.

## Members becomes Settings (October 2026)

The Members tab is now **Settings** (`/t/{ns}/settings`, `/t/{ns}/members` answers 301 with its query, so bookmarks and the "Invite someone else" link of an open page keep working). The page holds more than people, and will hold the wiki switch, so the people part became one panel with a **Members** heading: the heading, the Add form and the list (or the line saying why there is none), separated by `--line-soft` rules. The panels below it are unchanged. The models page's back link and the Reviews page's "Change in Settings" follow. Checked with the design skill as a checklist (tab bars, lists and tables, writing): lime as an owner of acme, as a maintainer and as a member, acme as its owner, in light and dark, at 390 and 1440 px, and without JavaScript.

- "Settings" is one word, as tab labels should be where possible, and says what the tab holds. It stays the label for members who only read the list, since the tab is the same for everyone.
- Grouped panels each carry their own header, so Members now looks like Review before release and Tokens for machines instead of a bare table under a form.
- Not changed: on a project, Invite someone stays its own fold-out above Members, since it also adds people to the project's teams.

## Wiki pages (October 2026)

The Wiki tab of a project or team, shown while its switch is on: the page, its editor, history, old revisions and their changes. Checked with the design skill as a checklist (segmented controls, text views, editing, alerts, lists and tables) and the rampstack accessibility-audit and form-strategy skills: lime with three revisions (headings, a table, code, two Mermaid diagrams and a draw.io diagram), an MDX page with front matter, imports and components, the empty page for an editor and for a reader through a team, Write, Preview, a stale save, History and an old revision, in light and dark, at 390 and 1440 px, and without JavaScript. The screenshot run logged no CSP violations and no errors in the page or its frames.

What it is:

- The page sits in one panel under a bar like the file preview's: "Revision 3 · Alice Martin · just now" (`--text-2` on the panel, 7.9:1 light, 10.1:1 dark), then Edit (primary, editors only), Show source, History and a download icon with a hidden label ("Download wiki.md"). Prose keeps the 72ch measure. Code, tables and diagrams use the panel. Only the wiki's reading pages add `frame-src 'self'` to the policy.
- A reader through a team below gets a tab bar of Packages and Wiki and no Edit. A team's wiki ends with "acme, the project above, has a wiki too." when the project's is on.
- Edit is one form. Write and Preview are a segmented pair of submit buttons (`aria-pressed`, the pressed one also bold on `--segment-on`, 44 px on touch) that post the text back, so switching needs no script and loses nothing. In Preview the textarea stays in the form, hidden. A visually hidden Save comes first in the form, so Enter in "What changed" saves instead of switching views. Save is trailing, Cancel leading.
- A save that came too late answers 409 with the edit page, never an error page: the banner names who saved which revision (`role="alert"`, the textarea `aria-invalid`, described by the banner and its hint, and focused), a panel shows what changed since the revision the edit started from, and the form's base moves on, so the next Save is deliberate. Too large or a two-line note: 400, the text kept, the field marked.
- History is a `.list` table (Revision, Who, When, Note, Size, Changes) that stacks on phones. Changes appears only when the revision before is kept. An old revision says "You are looking at revision 1. The newest is revision 3." with Edit from this revision for editors, which is how a page is restored.

Left as it is:

- Cancel leaves the editor without asking, like every other form here. The text of a stale save is never lost, but an abandoned edit is.
- The empty page's `loadout wiki push` command shows only to those who may save. A reader would be refused.
- The diff panel names the file (wiki.md) as package diffs do, though the page has one file.

## Start here (October 2026)

Signed out, the sidebar has only `/skills`, Docs and Sign in, so for a visitor that page is loadout's front door. It used to say "Four skills that teach your AI agent to use loadout" and assumed the reader knew what an agent, a skill or a knowledge base is. The readers are engineers who mostly use GitHub Copilot and are new to AI tools. Checked with the design skill as a checklist (design principles, onboarding, writing, sidebars, lists and tables, layout, color, accessibility) and the rampstack information-architecture, design-standards, frontend-component-build and accessibility-audit skills: signed out and signed in, in light and dark, at 390 and 1280 px, a skill page, the home page, and without JavaScript.

What it is:

- The page is **Start here**, in its `h1`, the sidebar and the skill pages' back link. "loadout skills" named only the last part of the page. "Start here" is two plain words, says who it is for, and fits a sidebar row. The URL stays `/skills`. The sidebar entry has a compass icon and no "Built in" word: that word only kept the old label from reading as a layer.
- Top to bottom: what loadout is (one lead paragraph), the one rule in a note ("loadout never runs AI and never sees your questions."), **How it works** (the home page's three step cards), **Words you will meet** (a `dl`: agent, skill, knowledge base, package, registry, project/team/layer, with an everyday comparison for the first three), **Get started** (five numbered steps), **Skills that come with loadout**, and the `loadout setup --skills` card.
- Get started uses the registry's public URL in `loadout login --remote`. Signed out, a Sign in button sits above the steps with who to ask when you are in no team. Signed in, the button is gone and step 2 says you are already signed in. Steps are a list with a CSS counter in a circle. Commands use the copy row (Copy needs script, the text is selectable).
- Skill cards lead with what you get ("Set up loadout", "Share your team's helpers", "Review a teammate's change", "Run a team"), then a summary, then **You can ask** with example prompts shaped like chat bubbles, then the skill name in `--muted` monospace. Titles, summaries and prompts are `builtin.IntroOf`, next to the skill order. A test fails when a skill has no intro. A skill without one falls back to its name and description. The home page and the skill page use the same card wording.
- "Your agent loads it when ..." is gone from the cards: it repeats the description written for the AI tool, and the example prompts show the same thing in plain words. It stays on the skill page, as "Your AI tool loads it when ...". The page says "AI tool" throughout, since "agent" is now a defined word with its own meaning.

Contrast, from the tokens: bubble text `--text` on `--pill-bg` 14.7:1 light, 13.1:1 dark. "You can ask", the comparisons and skill names in `--muted` on the panel 5.1:1 and 6.6:1. On the skill page the bubbles sit on the page background and get a panel fill with a hairline edge.

What changed while building it:

- The three step cards had numbers as well as chevrons. Two numbered lists on one page read as one sequence, so only Get started is numbered.
- Definitions ran past 110 characters at 1280 px. They stop at 72ch.
- `role="list"` on the two step lists, since Safari drops list semantics under `list-style: none`.

Left as it is:

- The page is long on a phone (about 4,000 px). Each section answers a question a newcomer asks in that order, and the skills, which a returning reader wants, have their own anchor (`#skills-h`) that the home page and skill pages link to.
- The words are fixed copy in the template, not Go data: only the prompts and titles are shared between pages.

## Wiki sections fold (October 2026)

Every `##` section of a wiki page folds, with nothing for authors to write, so a long page opens as its title, its intro and a list of its sections. Checked with the design skill as a checklist (disclosure controls, outline views, layout, accessibility, color, motion) and the rampstack accessibility-audit and frontend-component-build skills: a real onboarding page (MDX, eleven sections, two Mermaid diagrams) and a short test page with a `###` and a diagram inside a section, all closed, one section opened by a click and one by a link, Open all, the edit preview, in light and dark, at 390 and 1280 px, and without JavaScript.

What it is:

- Each top-level `##` and what follows it, up to the next `#` or `##`, is a native `details` whose `summary` holds the `h2`, so the summary is the control (a button with its expanded state for screen readers), the heading keeps its level and id, and it works without script. Text before the first `##` stays as it is.
- The chevron is drawn in CSS, `--muted` on the panel (5.1:1 light, 6.6:1 dark): it points to the text while closed and down while open, as in `details.report-context`, and it turns only under `prefers-reduced-motion: no-preference`. Rows are 44 px tall at every width with a hairline (`--line`) between them, like a grouped list.
- Sections start closed on the page and on old revisions, and open in the edit preview, where the author reads what they just wrote.
- Headings get ids from their text, so `#how-a-change-gets-made` works from inside the page and from outside. Chrome opens a closed section by itself for a link to something inside it and for find in page (checked without script). For a link to a section's own heading, which sits in the summary, `ui.js` opens it, on load, on `hashchange` and when a link names the fragment already in the address.
- With script, **Open all** / **Close all** (a plain button in `--link`, 5.6:1 light, 6.5:1 dark, 44 px under a coarse pointer, named "Open all sections" for screen readers) sits just above the first section, near what it opens (`disclosure-controls.md`), on pages with two sections or more.
- A diagram in a closed section keeps its lazy frame, which loads only once the section opens and then reports its height: both diagrams drew at full height (535 px and the long dev loop) at 1280 and 390 px, with no inner scrollbar.
- A printed page shows closed sections where the browser supports `::details-content`.

What changed while building it:

- Open all first sat in the page's bar beside Show source. On a phone it pushed History onto a third row. It moved next to the sections.
- The sidebar's team lists had ids like `sub-acme`, which a heading "Sub acme" would have taken. They are `sub_acme` now, and heading ids never contain a `_`. The page's other ids are reserved, and a test writes a heading for every id of the page and checks none repeats.

Left as it is:

- Open sections are not remembered across reloads (`outline-views.md` asks to retain expansion choices). A wiki is read top to bottom, a reload is rare, and a link to a heading brings a reader back to a section.
- VoiceOver in Safari may announce a summary's heading as part of the button rather than as a heading. A heading that contains a real button would need script, and the page must work without it.
- Old Safari and Firefox versions do not open a closed section for find in page. The text is still there once the section is opened, and Open all opens every one.

## Course box on the wiki (October 2026)

A team's wiki page ends with a **Course** box: the lessons of a knowledge base an owner chose in Settings, one open at a time, with quizzes whose answers fold out (plan in `docs/plans/wiki-course.md`). Checked with the rules above, the design skill as a checklist (disclosure controls, lists and tables, layout, writing, color, accessibility) and the rampstack frontend-component-build, design-standards and accessibility-audit skills, on the preview's lime course (seven lessons, a Mermaid diagram, three quizzes and one broken quiz, below a wiki page with four sections): no lesson open, lesson 3 open with two answers opened, the broken quiz, the course inherited from acme on koala, the owner's line for a course that cannot be found, and the Settings panel, in light and dark, at 390 and 1280 px, and without JavaScript.

What it is:

- The box is a panel after the wiki and its "has a wiki too" line, in the page's flow: nothing sticky, no script. Its head is an `h2` **Course** and a meta line, "lime-course 1.4.0 · 7 lessons" (a link to the package) and "From acme" when inherited.
- The lessons are an ordered list of links in a grouped list (`.lessons`): 44 px rows with a hairline between them, a number badge (`--text-2` on `--segment`, 6.2:1 light, 8.5:1 dark, hidden from screen readers, which count the list) and the title. Each lesson has its own address (`?lesson=02-propose.md#lesson-h`), so a link can be sent and Back works.
- With a lesson open the list folds into **All 7 lessons** (a `details` with a chevron in `--link`), the current row marked `aria-current="page"` (`--own-text` on `--own-bg`, 7.0:1 light, 7.5:1 dark), then an `article` labelled by its `h3` title, "Lesson 3 of 7" above it, the lesson, **Previous** and **Next** as the file preview's step cards (`rel="prev"`/`"next"`, side by side on phones) and **Close lesson**. The lesson's own headings move down two levels, so the outline reads Wiki, Course, lesson.
- A quiz is a group labelled by its question, with a small "Question" label (`--muted` on `--panel-alt`, 4.9:1 light, 6.3:1 dark). Each answer is its own `details` row, 44 px, letter badge (hidden from screen readers) and a chevron that points right while closed and down while open. Opened, it shows **Right.** or **Wrong.** in words first (`--ok-text` on `--ok-bg` 4.75:1 light, 7.2:1 dark, `--bad-text` on `--bad-bg` 5.7:1 light, 5.6:1 dark), then the explanation. Several can be open at once to compare. Open all acts on wiki sections only, so it never reveals answers.
- A quiz loadout cannot read stays code under a warn note naming the line ("This quiz could not be read: line 4: indent the explanation ..."), so the author can fix it from the package's preview page.
- When the course cannot be shown, owners see one line with **Open Settings**. Everyone else sees no box.
- Settings has a **Course** panel after the Wiki switch: a labelled `select` with an `optgroup` per namespace, the first option "No course", or on a team "acme's course (acme/onboarding)". A refusal comes back as a 400 with the error tied to the select. The row wraps before it squeezes the text on phones.

What changed while building it:

- The All lessons summary first had no marker, so it read as a plain link. It has the chevron of the other disclosures now.
- On phones the explanation's indent under the letter badge took a sixth of the width. It is flush with the answer's row there.
- The box sat 16 px lower than the gap between the other blocks of the page. It uses the page's own gap.

Left as it is:

- Opening a lesson loads the page again, which closes any wiki sections the reader opened. The box is at the end and the link jumps to the lesson, so nothing is lost.
- Links inside a lesson to its own headings (`#part`) do not scroll: lessons have no heading ids, as package previews have none.
- Answers stay closed when a page is printed.

## Proposals behind the latest release (October 2026)

A proposal records the release it was made from, so its pages show the real base ("1.0.0 → 1.1.0") instead of guessing the release below its version, which read as if the author had deleted the newer release's changes. Checked with the design skill as a checklist (segmented controls, buttons, feedback, writing, color, accessibility) and the rampstack accessibility-audit skill: a skill proposal two releases behind (one by a known person, one from before publishers were kept), a proposal whose version was released directly (blocked), and one that is up to date, as a maintainer, the author and a member, the reviews list and all three diff views, in light and dark, at 390 and 1280 px, and without JavaScript (nothing here uses script).

What it is:

- The header adds a `pill-warn` "Behind 1.0.2" after the status. The words carry the meaning. Two warn pills side by side are both true and both short.
- A notice under the byline (`role="note"`, headed by an `h2` for screen reader users) comes before the actions in reading and Tab order. It lists the releases since, newest first, with who released each and when ("1.0.2, 2 hours ago, 1.0.1 by Carla Ruiz, 2 days ago"), at most five then "and N more". A release with no known publisher shows its version and age only. It names the files those releases changed (by content, the manifest only when more than its version changed) and how many of them the proposal also changes, and links to "What changed since 1.0.0" and "What approving does". `--warn-text` on `--warn-bg` (about 6.5:1 light, 8:1 dark). Its links keep `--link` (5.1:1 on `--warn-bg` light, 5.2:1 dark) and are 44 px under a coarse pointer.
- A release at or above the proposed version blocks it: the notice says so, Approve is disabled and tied to "Approve is off: 1.9.0 is already released.", Reject and Withdraw stay. The longer reason sits in the notice only, not twice.
- The diff page shows tabs when the proposal is behind: This proposal (base to proposal), Released since (base to latest) and After approval (latest to proposal), each with one line saying what it shows. Unknown `?view=` values fall back to the first. The segments are equal width (`.diff-tabs`). On phones they keep one row and a long label wraps inside its segment rather than the row wrapping to two.
- The reviews list reads the base from the proposal and adds the Behind pill under the version, from one `Standings` call for the whole list instead of one `Releases` call per row.

What changed while building it:

- The plan named the third tab "What approving does". Segment labels should be noun phrases, so the tab is "After approval". The proposal page's link keeps "What approving does", which is a link, not a segment.
- With several releases the sentence read "without the changes released since to 2 files". It now says "without their changes to 2 files", right after the list it refers to.

Left as it is:

- Approve still works on a proposal that is behind. The explicit "Approve over 1.0.2…" step comes with the approval gate. Until then the notice and the "What approving does" view are the warning.
- The author's line says to withdraw, add the changes and propose again. It becomes the `loadout rebase` command once that exists.

Follow-up, approving over a newer release (October 2026): a maintainer's Approve on a behind proposal is **Approve over 1.0.2…**, a popover in the Reject pattern (a `details` form, no script) that says what approving drops, links to What approving does, takes an optional note tied to its hint with `aria-describedby`, and confirms with **Approve over 1.0.2** in the danger style, since it drops other people's changes for everyone. The action keeps its name from button to confirmation. An owner's own proposal keeps one form with `as_owner`, the note and `over`. The activity says "approved and released it over 1.0.2" with the warn dot. Checked in light and dark at 390 and 1280 px (on phones the popover opens from the row's left edge, as for Reject) and without JavaScript. The warning is `--warn-text` on the panel (about 7:1 light, 10:1 dark). The popover's link is 44 px under a coarse pointer. Left as is: the popover's text is the popovers' 12 px, like Reject and Approve as owner.

Follow-up, replaced proposals (October 2026): when an author proposes again after `loadout rebase`, the old proposal reads **Replaced** and says "Replaced by 6c573cefeab5 after a rebase onto 0.9.1. Reviews and approval happen there." in a plain panel note under the byline (`role="note"`, `--text-2` on `--panel`, 7.9:1 light, 10.1:1 dark, the id is a mono link). "after a rebase onto" shows only when the new proposal's base moved. Its activity says "replaced it with 6c573cefeab5" (a link) instead of "withdrew it" and the decision note, which only repeats the id. The replacement's byline adds "Replaces 1aac6ddf15d8", and ". Its reports compare the older content" when the old one had reports. Its activity says "proposed 1.0.0, replacing 1aac6ddf15d8", and prompts that came along read "added a prompt on 1aac6ddf15d8" with their own authors and times (the link stays on "added a prompt", which goes to the prompt). The author's behind notice ends with **Bring it up to date on your machine** and `loadout rebase --proposal ID` in a copy row (the command box on `--panel`, Copy with script, selectable without), then one line on resolving conflicts and `loadout propose`. Checked in light and dark at 390 and 1280 px and without JavaScript. Left as is: on phones the command wraps inside its box after `--proposal`.

## What's new (October 2026)

A static page at `/docs/whats-new/`, built from `docs/CHANGES.md` with the guide's template, and a **What's new** item in the registry sidebar under Docs. Checked with the design skill as a checklist (accessibility, layout, color, typography, sidebars, segmented controls, icons) and the rampstack accessibility-audit skill: a team page signed in with a 31-character name and `/skills` signed out at 320, 340, 360, 390, 800 and 1440 px, both docs pages at 320, 390, 800 and 1440 px, in light, dark and increased contrast, with a touch screen, with the keyboard and without JavaScript. Widths below are measured in the browser, not estimated.

What it is:

- Registry: Docs, What's new and the account item sit together at the bottom of the sidebar. Both help links use `--muted-2` (5.7:1 light, 6.8:1 dark, 9.6:1 and 11.5:1 with increased contrast). Tab order is Docs, What's new, account. The page is on another site, so the link never carries `aria-current`, and it opens the page with `?from=` like Docs.
- The icon is a megaphone drawn for loadout, at the other icons' 16 px and stroke 2. The plan had a sparkle, but a sparkle already marks the skill cards on Start here, and one glyph should mean one thing.
- Phones: both help links are icons in the top bar, their words visually hidden and still the accessible names, 36 px wide with a mouse and 44 by 44 px on touch. The account item takes the room the row has left (`flex: 1 1 0; min-width: 0; max-width: fit-content`) instead of a fixed cap, so the row stays one line from 320 px up and a name loses only the letters that do not fit: "Alice Martin" shows whole at 360 px, a long name gets 131 px at 390 (the plan's fixed cap would have left it 112), and "Sign in" is never cut.
- Docs pages: a two-segment switch, Guide and What's new, styled like the registry's tabs (`--segment`, `--segment-on`, text 13.2:1 and 16.8:1 light, 13.1:1 and 8.1:1 dark), with `aria-current="page"` on the page shown, in its own `nav` ("Docs pages") under the brand on wide screens and on the right of the phone header. The sidebar is no longer one `nav` named Contents. The contents keep their own `nav`. With script, the switch keeps `?from`, so either page still leads back to the registry. A `from` outside loadout's sites is ignored, as for the brand.
- Each item starts with its tag, a label of equal width: Feature on `--own-bg` in `--own-text` (7.0:1 light, 7.5:1 dark), Fix on the new neutral `--fix-bg` in `--fix-text` (9.6:1 light, 9.1:1 dark, 14.3:1 and 12.8:1 with increased contrast, where tags also get an outline). The word carries the meaning. The color only helps the eye. A visually hidden colon follows the label, so a screen reader, or the page without its style, reads "Feature: ..." rather than running the label into the sentence. On wide screens the tags hang in a column of their own so the sentences line up. On phones they stay inline.
- Months are `h2` under a rule. Days are `h3` in `--text-2` (7.9:1 light, 10.1:1 dark) at body size, so a day never reads as a month, even on phones where the month heading shrinks.

What changed while building it:

- On phones the project fold arrow showed at the right edge of the top bar on every page with a project: `.js .js-only` outranked `.side-toggle { display: none !important }`. The phone rule now names `.js .side-toggle` too.
- The fixed account caps from the plan (112 px, 84 px below 375, 104 px on touch) cut "Alice Martin" at 340 px while wrapping the item to a second row anyway. They are replaced by the flexible width above.
- At 320 px the docs header (brand 133 px, switch 164 px) pushed the page 5 px sideways. It now wraps the switch under the brand there.
- The label's margin plus the space after it left a wider gap than inside the pill on phones. The margin is 4 px.
- `scripts/preview/shoot.mjs` takes `"contrast": "more"` and `"touch": true`, for these checks.

Left as it is:

- On touch the two 44 px icons sit 4 px apart, like the registry's tabs and filters. The targets do not overlap.
- Under 320 px nothing more is done: the account name shrinks to its ellipsis but the row still fits.
- Screen readers were not run. Names, landmarks and order were checked in the markup and the browser's tree.
- The page's text is the user's (`docs/CHANGES.md`). The review covered its look, not its words.

## The docs logo leads back (October 2026)

The "loadout docs" logo goes back to the registry page that opened the docs. Every registry link into the docs is made by `docsLink` and carries `?from=<this page>`, the #fragment after the query (the sidebar's Docs and What's new, and Start here's install link, which had none). `docs.js` keeps the checked page for the tab in `sessionStorage`, so a docs page opened without `?from` in the same tab (a link that lost it, the address typed again) still leads back. A new `?from` replaces it, and both pass the same check (a loadout site, or localhost). Links to other docs pages, the switch and links in the text, carry it on. Links within the page need nothing. With no page to go back to, or without JavaScript, the logo goes to the top as before. Nothing changes visually.

- Checked in headless Chrome with the docs served under the live CSP: from a team's wiki through the sidebar, from Start here's install link, after a contents link, both switch links, the same tab without `?from` and a reload, and at 390 px through the phone menu in dark.
- Links in wiki and package Markdown to the docs are left as written: rendering them has no request, and the kept page covers the same tab.

## Member search and teams line (October 2026)

The Members panel head holds one search box for both lists on a settings page. The server sends every row and hides the ones `?q=` does not match, so the page works without JavaScript, and `ui.js` filters as you type and can show hidden rows again. Under a person's name a muted "Teams: a, b" line lists their other teams in the same project. On a phone the head wraps and the box takes the full width.

## Delete a package (October 2026)

Two surfaces, no script: a **Delete package** section at the end of the package page, and a confirmation page at `/t/{ns}/p/{team}/{name}/delete`. Since requests to delete a package (below) only admins delete at once: the red section and the confirmation page are theirs. Everyone else sees the section without the red border and with **Ask to delete…** alone, and a direct request to the confirmation page gets "Not allowed". The page is a plain GET and the form a plain POST, so it works without JavaScript. Written to the rules here and `docs/plans/package-delete-ui.md`. The Apple HIG review is recorded below.

- The package page section is a `section.panel.danger-zone` with its own `h2`, one sentence and an `a.btn.btn-danger` "Delete package…" whose accessible name adds the package id. A link, not a button: it only opens the next page, so a stray click deletes nothing.
- The confirmation page keeps the team header and tab. Its heading is an `h2` ("Delete lime/x?"), because the header already holds the page's `h1`. A panel lists the footprint in the existing `dl.facts`, then the plain sentences: it cannot be undone, what is removed, what people who installed it see, that a copy of the database is saved first, and who may have it (loadout records no installs).
- The form is its own panel with `danger-zone`: a required labelled textarea ("Why is it deleted?", error field `delete-reason`) and one labelled field ("Type lime/x to confirm") with a hint, `autocomplete="off"`, `autocapitalize="off"`, `spellcheck="false"`, and Cancel then the destructive button. The destructive button is never the primary action and never the default focus. Cancel comes first so on a 390 px phone the row wraps with the safe choice on top.
- Errors are server-rendered into the layout's `.alert` (`role="alert"`, `id="form-error"`). A wrong name marks the field `aria-invalid` and `autofocus`, ties it to the message and the hint with `aria-describedby`, and keeps what was typed. A changed list (409) shows the new list above the same form.
- Colour: no new tokens. `--bad-text` carries the border and the button text in light, dark and the forced dark theme. Contrast of `--bad-text` on `--panel` is the same pair `.btn-danger` already uses for Reject.
- After a delete the packages page shows a `role="status"` notice with the counts, read from the query (display only, like the email on Settings).

Apple HIG review (October 2026), at 1280 and 390 px, light and dark, as admin, as a member and signed in as an owner. Screenshots from the throwaway preview, not kept in the repo:

- Checked and kept: the section is absent for members. The link opens a page, nothing is deleted by a click. Cancel is first in the order and the destructive button is never the default. The typed id has a visible label, a hint and `aria-describedby`. An error moves focus to the field and the alert is announced. Targets are 44 px under `(pointer: coarse)`. No sideways scroll at 390 px.
- Changed: the consequences were one dense paragraph, now one sentence and a short list (what installed copies see, old versions, who may have it, the database copy), so the one line that matters, "This cannot be undone.", is not buried. The facts read "2 (2.2.0, 2.3.0)" and "1 (1 open: ...)" instead of "1, 1 open:", and "Course of" became "Course for" with "Nobody" when empty (it wrapped into its value at 390 px with a longer label). The button that deletes is tinted with the alert pair (`--bad-bg` fill, `--bad-strong` text, `--bad-text` border), so it reads as the dangerous choice without a new token. The link on the package page stays an outline. The package page sentence names the package and says "for good".
- Left alone: the error shows above the back link, in the layout's alert, as on every form. The form panel stays 560 px wide, narrower than the facts panel, like the other forms.

## Requests to delete a package (October 2026)

Phase 2 of the delete plan: anyone who reads a package may ask to delete it, and a second person approves. No script anywhere: every page is a GET and every action a plain POST, and the buttons that open a note use the same `details.menu` fold-outs as the proposal page.

- **Package page.** One section, "Delete package", for everyone signed in. Without a request open it says who approves and offers `a.btn` "Ask to delete…" (a link to the form, accessible name adds the package id). With one open it says "Deletion asked by bob 2 days ago" and links to the request when the viewer can open the namespace's reviews (members, a reader through a team below sees the sentence alone), and offers no second ask. An admin gets the red `danger-zone` border, one more sentence and the red "Delete package…" beside it.
- **Ask page** (`ask.html`, team header, tab "packages", `h2`). The footprint (`dl.facts`), a list of what a request does and does not do (nothing deleted yet, who decides, what installed copies see, who may have it), a required textarea "Why should it be deleted?" with a hint, Cancel first, then the primary "Ask to delete". A missing reason is a 400 with `.alert` (`role="alert"`, `id="form-error"`), the field `aria-invalid`, `autofocus` and `aria-describedby` on the message and the hint. When a request is already open the form is replaced by "Someone already asked" with a link.
- **Request page** (`deletion.html`, tab "reviews", `h2`). Header like the proposal page (`prop-head`): the title with a "Delete" pill (`pill-bad`) and the status pill, "Asked by ... 2 days ago", the buttons on the right and the reason they are on or off in `act-why`, tied to the buttons with `aria-describedby`. Approve is the destructive `btn-danger` ("Approve and delete", accessible name adds the package) and never `btn-primary`. Reject, Withdraw and "Approve as owner…" open a `pop` with a note (required for the owner's, which also says it deletes without review). Unavailable buttons stay as `disabled` buttons with the reason, as on proposals. A release after the request shows a `replaced-note` and turns Approve off. Then panels: Reason, the outcome (heading and one sentence: "Deleted 1 release, 0 proposals, 0 reviews and 0 prompts by alice last month", "Deleted without review ... on their own request, without a second person's review", "Rejected ... so the package stays", "Closed ... because the package was deleted another way", with the note below), and the footprint (what loadout removes now, or what it removed).
- **Reviews list.** Requests sit among the proposals by date: the first column names the package and shows the reason as its description, the version column holds a `pill-bad` "Delete", the reports cell has a visually hidden "A request to delete has no reports", and the status pill reads Waiting for review, Deleted (`pill-ok`), Deleted without review (`pill-warn`), Rejected, Withdrawn or Deleted another way. The tab count adds open requests.
- Colour: no new tokens. The pills and `--bad-text` are the ones the proposal page and the delete page already use, checked in light and dark.
- Checked by the author in headless Chrome from the preview (`scripts/preview/deletions.sh` seeds a request in every state) at 1100 and 390 px, light and dark: the request page as the person who may approve, as the author, as an owner who asked, stale, approved, approved without review and rejected. The ask page and the package page. The list. The Apple HIG review is below.
- Changed after the first look: the request page's own request no longer shows in its facts ("Deletion requests"). The outcome sentence puts "without a second person's review" after the time, not between the name and the time. The list's first column head reads "Package", since it now names packages of proposals and requests.

Apple HIG review of the request pages (October 2026), with the apple-design skill as a checklist and the rampstack design-standards, form-strategy, accessibility-audit and qa-testing skills. Looked at in the preview, light and dark, at 1280 and 390 px: the ask page (form, error, request already open), the request page as the author, as a maintainer who may decide, as an owner who may approve their own, as someone who may only look, stale, rejected, withdrawn, approved, approved without review and gone, each popover open, the reviews list with requests among proposals, the package page for a member, a maintainer, an owner and an admin, and the admin's delete page with its reason error. Screenshots stay in the preview folder, not in the repo.

Changed:

- **Approve and delete asks first.** It was a one-click form that deletes for good. It is now **Approve and delete…**, a popover in the Reject pattern (a `details` form, no script) with the consequence in `--warn-text` ("This deletes lime/x for good: every release, proposal, review and prompt. People who installed it keep their files. A copy of the database is saved first."), an optional note, Cancel then the confirming button in the danger style. Reject's popover says that rejecting closes the request and the package stays. The disabled Approve carries the ellipsis too.
- **The page title says what the page is.** "Delete lime/x?" stayed a question after the request was deleted, rejected or withdrawn. It is "Request to delete lime/x" in every state. The red "Delete" pill beside the status pill is gone from the page (the title says it, and a red "Delete" next to a red "Rejected" read as two warnings). The list keeps it, where it sits in its own column.
- **Panels share one heading size.** Reason and the outcome used the page's large `h2` while the footprint used the small panel head, so one page had two heading scales. All three use `.panel-head`. Their text is in `.panel-text`. For a closed request the outcome comes first, since it is what the visitor came to see. The decision note under it is a quote with a rule on the left and a visually hidden "Their note:".
- **The reason is the person's own words.** `.reason` keeps line breaks (`pre-wrap`) and breaks long words, so a pasted path or URL cannot push the page sideways at 390 px. It had no style at all.
- **Stale request.** "it cannot be approved: it was asked about something else" said the same as the line under the buttons. The note now says what happened ("so the request is about an older release than the one that is there now") and the line under the buttons says what to do.
- **Reviews list.** The third column head is "By", since it holds the author of a proposal or of a request.

Checked and kept:

- Status chips use the proposal tokens (`--warn-bg`/`--warn-text`, `--ok-bg`/`--ok-text`, `--bad-bg`/`--bad-strong`, the plain pill), checked in both themes. The words carry the meaning, color only helps the eye. "Deleted without review" stays a warn pill, as "Released without review" does.
- Cancel comes before the confirming button in every popover and form, the confirming one is never the default focus, and the order in the page is Reject, then Approve (Withdraw, then Approve as owner). Unavailable buttons stay as disabled buttons tied to their reason with `aria-describedby`.
- The ask page's error is the layout's `role="alert"` banner, tied to the textarea with `aria-describedby`, the field `aria-invalid` and focused. The same on the admin delete page's reason. 44 px targets under a coarse pointer come from `.btn`. No page scrolls sideways at 390 px, and the popovers open from the left edge there.
- On the list a request names its package, shows its reason as the description and a "Delete" pill in the version column. At 390 px the rows stack like proposals.

Left as it is:

- A person who may only look still sees both buttons, disabled, with "Only maintainers of lime approve or reject." under them, as on proposals. Hiding them would make the page differ by role in a way the proposal page does not.
- The error banner sits above the back link, as on every form, not next to the field.
- The admin on the package page sees "Ask to delete…" beside the red "Delete package…": asking is how an admin gets a second person's review, which they may prefer to deleting at once.

## Memory tab (October 2026)

Team memory on the web, behind the namespace's Team memory switch: `memory.html` (search and New this week), `memory_entry.html` (one memory, links, history), `memory_setup.html` (connect a tool by hand). Routes are `GET /t/{ns}/memory`, `/memory/{id}`, `/memory/setup` and `POST /memory/{id}/retract` and `/move`. All are the usual 404 while the switch is off, through `inMemory`, and the tab shows only while it is on. Looked at with the apple-design checklist and the rampstack accessibility-audit, design-standards and form-strategy skills, in the preview (a seeded `memory.db`), light and dark, at 1280 and 390 px, with a touch screen and without script: the list, a search with and without hits, an unconfirmed memory, a memory with a diff in its history, a retracted one, a memory whose text holds HTML, both popovers open, the setup page, and the entry as a maintainer and as an author.

Decisions:

- **Read only.** Nothing edits a memory. The only forms are the search (GET) and the two popovers. Entry text is escaped text in `.mem-body` (`pre-wrap`), never Markdown and never a link, since agents and people paste anything into it.
- **Retract and Move to project ask first.** Both are `details.menu` popovers in the Remove pattern, with the consequence in the text. Retract needs a reason (`required`, one line, 200 characters). A refused reason comes back as a 400 with the layout's `role="alert"`, the popover open again, the field `aria-invalid` and `aria-describedby="form-error"`, and the typed reason kept. Move says it cannot be moved back and that only the author and the project's maintainers can retract it afterwards.
- **Buttons only for those who may.** `memory.Service.History` returns `CanRetract` and `CanMove` from the same rules `Retract` and `Move` check, so the page never offers what the service refuses. A retracted memory has neither and shows a notice with who, when and why.
- **History** lists versions newest first: action, who, when, the note or reason, and a native `details` "Changes in version N" holding the line diff from `textdiff` (name, description, type, team, links, then the body). A confirm or a retract changes no text, so it has no diff. Works without script.
- **The unconfirmed marker** is a `pill-warn` "Unconfirmed since 13 Mar 2026" in lists and on the entry. Memories are never hidden for it.
- **Search** is a plain GET form with a visible Search button, a type select and, when a project is above, a team select. No autosubmit, so nothing depends on script. With no query the page shows New this week (7 days, newest first), filtered by the same selects.
- **Setup page** lists the endpoint, a curl test, the bridge command, an HTTP config with an `Authorization` header and the CA steps. Code sits in `pre.cmd.snippet` that scrolls inside itself (focusable, `tabindex="0"`). Copy buttons are `js-only`, and the text is selectable without them. It never prints a token. The page cannot show the CA's fingerprint, so it tells the person to compare it with the one their admin gave them.
- Colour: no new tokens. Pills, `--warn-*`, `--bad-*`, the diff colours and `--panel-alt` were checked in both themes. No script, inline style or handler was added, and the console showed no CSP violation.

Checked and kept:

- 390 px: the tab bar still fits five tabs, lists and the history stack, nothing scrolls sideways, the popovers open inside the screen. Touch: buttons and `summary` rows are 44 px (`.memhist-diff > summary` added to the coarse-pointer rule).
- Search field and selects have visible-hidden labels. Headings go h1 (team), h2 per panel. The current tab has `aria-current="page"`.

Left as it is:

- The author keeps the Retract button after a memory moves to the project, since the service allows it. A maintainer of the team does not, and the Move popover says so.
- Authors show as their display name, else the email they had when they saved, like the wiki.
- No pagination: lists stop at 50 and say so.

## Team memory on the server page (October 2026)

A "Team memory" panel on `/account/server` (admins only, like the rest of the page): a table with one row per namespace that has a switch row, with Switch (`pill-ok` "On" or plain "Off"), Active, Retracted, Versions, Newest write (a date, or "None"), Text (size) and Endpoint (`/mcp/t/<team>`), then one line with the count of valid memory tokens and the note that only counts and dates are shown. The panel is left out when memory is not configured, says so in words when no team has a switch yet or when counting failed. `memory.db` itself (size, write-ahead log) was already a "Database memory.db" row under Storage, so no second line was added. Looked at with the apple-design checklist and the rampstack design-standards and accessibility-audit skills, in the preview (a seeded `memory.db`), light and dark, at 1280 and 390 px, with a touch screen and without script.

Decisions:

- **Counts only.** The store query reads the lengths of the text, never the text, and no name or author. A web test runs the real store with a marker in every text field and checks it is not on the page.
- **A table that scrolls inside itself at 390 px.** Eight numeric columns do not stack well, so the table sits in `.tbl-scroll` (`role="region"`, labelled by the heading, `tabindex="0"` so the keyboard can scroll it, with a focus ring). The page never scrolls sideways. Cells do not wrap, so a team name stays on one line.
- **Words carry the state.** "On" and "Off" are text in a pill, so color is only a help. Numbers right-aligned with tabular figures, as the cost table.
- Colour: no new tokens. `pill-ok`, `--line-soft` and `--muted` were checked in both themes. No script, inline style or handler was added.

Left as it is:

- A team name is escaped like any text, and a very long one scrolls in the box.
- No sorting or paging: one row per namespace, ordered by name.

## Server page: system, certificate and backups (October 2026)

Follow-up to "Server page for admins". The page also shows the machine it runs on. Panels in this order, in pairs side by side from 1000 px up and stacked below: **Host** beside **System** (kernel and files), **Storage** beside **Backups**, the cloud pair when a provider is set up, **Certificates** beside **Process**, **Team memory**, then **Registry** beside **Login mail**. A **Site certificate** tile sits between Last backup and Running for. A panel whose part is off is left out, and a part that failed keeps its panel and says why in one sentence. The page was checked in light and dark, at 390, 900 and 1280 px, with scripts removed, in every state: all well, warnings (high load, reboot waiting, stale updates, a failed backup run, a database missing from the archive, a certificate this server does not trust, one about to end), everything failing, every new part off, an expired certificate and hostile text in names.

Decisions:

- **Words carry every state.** Pills and tiles say "Incomplete", "Failed", "Needed", "Not trusted". Tile tones reuse `--warn-text` and `--bad-text`, and pills `pill-ok`, `pill-warn` and `pill-bad`. No new token, no script, no inline style.
- **Facts get two small additions.** `Code` sets a value in code type (the fingerprint) and `Badge` puts a pill beside a value (the end date). The fingerprint breaks after a colon only (`<wbr>`), so a line never ends in the middle of a byte at 390 px.
- **A panel without a partner takes the whole row** (`.grid2 > :only-child`), and the tile row fills its width with `auto-fit`, so four tiles (certificate part off) are as wide as five.
- **A section can hold a sentence and facts.** The Backups panel with no archive yet says so and still shows the last run, since a failed first run is exactly when an admin needs it.
- **Failures keep the panel.** The System and Certificates rows each say why in the row.
- **Footnotes say what is cached**: "kept for 10 minutes", "Read each time the page loads". The page has no refresh button, reloading is the refresh.
- The cost table and the Team memory table share one `.table` style, and the memory table keeps its own scroll box at 390 px.

Left as it is:

- With a short panel beside a tall one the short one leaves empty space below it. Stretching the panel would only add an empty box.
- The System panel on a Mac says "Not available on this system" for kernel, memory, load and uptime, since those come from Linux system calls.

## Pipeline pages (October 2026)

A pipeline package's page (`package.html`, `ui_pipeline.go`) gets a diagram drawn from the stored `pipeline.yaml` and tables that say the same in words. A template shows Steps (the diagram), Slots (slot, reads, writes, verdicts, brief), Moves between slots (from, on, to, gate or auto, rounds) and **Use this pipeline** with a copy row holding `loadout new pipeline TEAM/NAME --uses ns/name@MAJOR` (the viewer's team when the page is a team's, the word TEAM on a project page). A binding shows a line naming its template (linked, with the release drawn), Steps with the agent names under each slot, **Agent for each slot** (each agent linked to its package page in the page's view) and the same moves table with the binding's extra gates applied. Checked with the design skill as a checklist and the accessibility-audit skill: template and binding in light and dark at 390 and 1280 px and without JavaScript, plus a binding with an unbound slot and a missing agent and one whose template is out of the view, with the keyboard.

- The diagram is a `figure.diagram` on the draw route, `draw/pipeline.yaml?pipeline=1`. The package page needed `frame-src 'self'` (the first screenshot showed Chrome refusing the frame), which it now sets only when the page has a diagram. Without script the Mermaid source shows, with a caption that the tables say the same. A line under the title says what dashed and solid arrows mean, so the diagram's one visual code is also in words. In the tables the same fact is a word, never a color alone: a Gate pill and an Auto pill.
- Problems come first, in one warn panel with a heading that counts them (`--warn-text` on `--warn-bg`, about 7:1 light and 10:1 dark, as the other warn notes). The bad row in the agent table carries the same warn colors plus a "No agent" or "Not in this view" pill, so the row says it too. An unbound slot, an agent the view does not have and a template the view cannot read are three sentences, each naming the slot or agent.
- Phones: the tables stack as the other lists do, and since the header row is hidden there each cell carries a visible label (`.cell-label`, shown only on phones, 12 px in `--muted`). Nothing scrolls sideways at 390 px, and code wraps inside its cell.
- Left as is: the Mermaid drawing is large with its default start and end circles, like every other diagram. Links in table cells are the 44 px of `.list` rows on coarse pointers only through the row's padding, as in the packages list. Screen readers were not run: names, headings (one `h2` per panel tied with `aria-labelledby`) and the tables' `scope` were checked in the markup.
