# Member search, ordering and teams line

**Ticket:** none (personal project) · **Agreed:** 2026-10-12 · **Layer:** loadout

## Problem

On a project or team settings page the member list comes in store order, so maintainers are mixed in with members and a long list is hard to scan. There is no way to find a person in it, and nothing says which other teams a person belongs to, so "who is in lime?" means opening every team.

## Solution

The member lists on the settings page are sorted by role, can be searched by name, email or team, and show under each person's name the other teams of the same project they are in.

## Acceptance criteria

Ordering

1. On a project settings page the Members table lists owners, then maintainers, then members.
2. On a team settings page the Members table lists maintainers, then members. Project owners stay in the "Through the project" section.
3. Within one role, people are sorted by name, ignoring case, falling back to their email when they have no name.

Search

4. The Members panel header shows a search box whenever the page lists at least one person (in Members or in Through the project). It is not shown to viewers who may not list members.
5. A search matches a person when its text, ignoring case and surrounding spaces, is part of their name, email or one of the team names on their teams line.
6. One search filters both the Members list and the Through the project list.
7. Without JavaScript, submitting the box loads the page with `?q=`. Every row is still sent, and rows that do not match carry the `hidden` attribute.
8. With JavaScript, typing filters the rows as you type, including showing rows the server hid for an earlier `q`.
9. When no row of a list matches, that list shows "No member matches." (Members) or "No owner matches." (Through the project). Otherwise the line is hidden.
10. An empty search shows every row.
11. Changing a role, removing a person and the remove popover's Cancel link keep the current search: the page that comes back has the same `q`. With JavaScript, the text typed in the box at submit time is what is kept, even if it was never submitted.
12. Adding a member clears the search, so the person just added is visible.

Teams line

13. Under each person's name (and email or pending line) a small muted line reads "Teams: a, b", with team names sorted.
14. The line lists only teams under the same top-level project as the page, never the team whose page it is, and never a project.
15. A team the viewer may not open is left out.
16. A person with no such team has no line.
17. Project owners in Through the project get the same line, shown only when they have team memberships of their own.
18. Admins and the operator see every team of the project on the line.

Docs

19. What's new, GUIDE.md, the registry wikis and the built-in skills describe the search, the order and the teams line, per the loadout feature rule.

## Decisions

- **Order by role rank**, owners then maintainers then members, then name. On a project page owners stay in the same table (no separate Owners section): the rank gives the reading order without a second layout.
- **Teams come from the account service.** The member detail returned by the member listing and the inherited-owner listing gains the sorted list of the person's other teams. The service computes it so the web layer never decides who may see which team.
- **Team visibility uses the viewer's own access**: a team is listed when the viewer could list its members. This stops a member of one team learning the names of teams they cannot see.
- **Same project only**: a team is in scope when its top-level project is the page's top-level project. Teams under other projects are noise on this page.
- **Search is server-side with live filtering on top.** The server always sends every row and hides the non-matching ones, so the no-JS page works and the script can widen the search without a reload. This follows the rule that every page works without JavaScript.
- **One search box per page**, in the Members panel header, covering both lists. It is always shown, so it does not jump around between small and big teams.
- **The search survives role changes and removals** through a hidden `q` field on those forms that the redirect carries back. It does not survive an add, because the new person may not match.
- **The API and CLI keep their current order.** Only the web pages change.

## Testing decisions

- Test external behaviour: what the service returns and what the rendered page contains, not the helpers.
- **Account service** (prior art: `TestMemberDetails`, `TestInheritedMembers` in the account browse tests): a person in two teams of the same project and one team of another project gets only the same-project team, never the page's own team. A viewer without access to one of those teams does not see it, and an admin does.
- **Web settings page** (prior art: `TestInheritedMembers`, `TestNoInheritedMembers` in the web UI tests): order of rows on a project page and a team page, `?q=` marks non-matching rows hidden on both lists and shows the no-match line, the teams line text, `q` kept after a role change and dropped after an add, escaping of names and team names.
- **Test-stage scenario:** `loadout-preview` seeds a throwaway registry with a project, three teams and people in several teams, then checks in light and dark at 390 px and desktop width: the order, the teams line, filtering as you type, no-JS `?q=`, and a role change while filtered keeping the search.

## Out of scope

- The order of members in the API and the CLI.
- Showing a person's role in each of their other teams.
- Team chips, links on team names, or truncating long lists with "+N more".
- Searching anywhere other than the settings page member lists.

## Open questions

None.

## Changed
