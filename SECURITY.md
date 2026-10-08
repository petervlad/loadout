# Security policy

## Report a vulnerability privately

Use GitHub's private vulnerability reporting: on the repository, open the
Security tab and choose "Report a vulnerability". If that is not available, email
security@example.com (the maintainer replaces this address before the
repository goes public). Please do not open a public issue or pull request for a
vulnerability.

Include what you found, the version (`loadout version`), how to reproduce it and
what an attacker gains. Never send a real token, password or invite; a test
registry of your own is the right place to prove it.

## What you get back

- An acknowledgement within 5 working days.
- A first assessment (accepted, need more, or not a vulnerability) within 14
  days.
- A fix and a release for an accepted report, normally within 90 days, with
  credit in the release notes if you want it. We ask you to keep the details
  private until the fix is out.

## Scope

In scope:

- The registry (`loadout serve`): the API, tokens, accounts, sessions, roles,
  device login, rate limits, proposals and reviews.
- The web pages: cross-site scripting, request forgery, header and CSP
  problems, and anything that shows one team's data to another.
- Decoding untrusted input: package bundles, `import`, YAML and Markdown.
- The CLI writing files: path traversal, symlink following, overwriting a file
  the user edited, and the session hooks and settings files it edits.
- The files in `deploy/` and the host skills, where they leave a host less safe
  than documented.

Out of scope: a registry whose operator chose a weak password or exposed it
without HTTPS, denial of service by an authenticated admin, findings that need
the attacker to already control the host or the user's account, and
vulnerabilities in the AI tools, cloud providers or mail providers loadout works
with (report those to them).

## Supported versions

The latest released minor version gets security fixes. Older versions do not;
update with the release binary, Homebrew or `go install`.

## What loadout never does with AI keys

loadout never runs a model and never holds an AI key. The registry has no
setting for one and calls no model. `loadout review` starts the reviewer's own
Claude Code or Copilot CLI on the reviewer's machine, with that person's own
login. The host skills are text your own agent follows with your own cloud
tools: they never ask for, read or print a secret, never open the settings file
of a registry, and use `loadout serve --check`, which reports what is set
without printing values. If you find a code path that breaks any of this, that
is a vulnerability: report it.
