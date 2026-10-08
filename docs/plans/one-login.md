# One login per registry

Status: done (2026-09-29).

## Goal

`loadout login` once per registry, not once per team. The token says who you
are; what you may do is decided per request, in the namespace the request is
about, from your memberships as they are now. Machine tokens (CI, shared
machines) stay bound to one namespace.

## Role rules

Effective role in a namespace N, highest wins:

| Source | Role in N |
|---|---|
| a membership in N | that role |
| owner of a project above N | owner (was: maintainer) |
| any membership in a team below N | member: read and propose, never publish |
| admin flag | admin: through a token, read and publish everywhere; managing namespaces, owners and admins only on the web or the host |

A team maintainer is not a maintainer of its project; an owner adds that role
separately.

## Server

- An account token is a login token whose namespace is `*`
  (`registry.AccountNamespace`). Old per-namespace login tokens keep working
  until they expire.
- `Authenticate` returns an account principal (`Account`, no namespace yet).
  `Service.In(ctx, pr, ns)` narrows it to ns: the user's scope there
  (`Memberships.MemberScope`) and ns's chain. A per-namespace token is
  returned unchanged.
- The view namespace comes from the `X-Loadout-Namespace` header (the client
  sends the team it is working in). Operations on one package or one
  proposal narrow to that package's namespace themselves, so publish,
  propose, approve and fetch work whatever the header says, even when it
  names a team the user has left (the header is a hint). Listing
  (packages, changes, layers, proposals) needs a view: without one an account
  token gets a clear 400, and a view the user may not use a 403.
- `GET /v1/me`: the account's email and every namespace it can read, with
  kind, parent and role, so the CLI can bind them.
- Managing (namespaces, members, machine tokens) through an account token
  checks the user's role in the target namespace; admin rights never come
  through a token.
- Device sign-in: the CLI asks for `*`; the approval page shows "all your
  teams" and the account, no team picker.

## CLI

- `loadout login [--remote URL]`: one remote per registry URL with one token.
  It binds every namespace from `/v1/me`: the lowest ones directly, their
  projects as layers above. Older remotes for the same URL are folded into it
  (their bindings move, their tokens are revoked).
- Requests to a remote carry the view namespace of the package's binding.
- `list` shows each package once per registry, even when several teams read
  the same project. `install --all` installs from every team you are in.
- `logout` revokes the account token and removes the remote and its
  bindings.

## Not in this step

- Choosing a subset of teams at login (every team you belong to is bound).
- Migrating stored per-team tokens without a new `loadout login`.
