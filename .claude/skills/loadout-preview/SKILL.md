---
name: loadout-preview
description: >-
  See loadout's web pages without touching the live registry: seed a throwaway registry with
  sample projects, teams, agents, a skill, a KB, proposals and signed-in users, serve it locally,
  and take screenshots in light and dark at any width (390 px phones included). Use for UI work,
  UI reviews, "show me the page", "screenshot", "how does it look on a phone", and for testing
  web flows end to end.
---

# Preview the web UI

The scripts live in `scripts/preview/` (read its README for the port table). Everything goes under `$LOADOUT_PREVIEW_DIR`; point it at your scratch folder so nothing lands in the repo.

```sh
cd ~/personal/loadout
export PATH=/opt/homebrew/bin:$PATH
export LOADOUT_PREVIEW_DIR=<scratch>/loadout-preview

scripts/preview/seed.sh        # builds this checkout and seeds: project acme, teams lime and koala, agents, a proposal with a report, users
scripts/preview/serve.sh       # registry on 127.0.0.1:7431, signed-in proxies on 7441-7447
scripts/preview/skills.sh      # adds lime/release-notes-skill, the KB lime/runbooks and a skill proposal
```

Open pages through the proxy ports (7441 is alice, a maintainer, light; 7442 alice dark; 7443 bogdan, a member; 7444 signed out). The proposal id is in `$LOADOUT_PREVIEW_DIR/proposal-id`.

## Screenshots

Headless Chrome's `--window-size` cannot go below about 500 px, so use `shoot.mjs` (Chrome DevTools protocol, exact width, full page):

```sh
cat > "$LOADOUT_PREVIEW_DIR/jobs.json" <<JSON
[{"out": "$LOADOUT_PREVIEW_DIR/shots/packages-390.png", "path": "/t/lime", "cookies": {"loadout_session": "alice-session"}, "scheme": "light", "full": true, "width": 390},
 {"out": "$LOADOUT_PREVIEW_DIR/shots/packages-dark.png", "path": "/t/lime", "cookies": {"loadout_session": "alice-session", "loadout_theme": "dark"}, "scheme": "dark", "full": true}]
JSON
mkdir -p "$LOADOUT_PREVIEW_DIR/shots"; node scripts/preview/shoot.mjs "$LOADOUT_PREVIEW_DIR/jobs.json"
```

Then look at each image with the Read tool. For the docs page, run `sh docs/guide/build.sh` and serve `deploy/site` with `python3 -m http.server --bind 127.0.0.1 7499`; stop it afterwards.

When done: `scripts/preview/stop.sh`.

## Notes

- A page's link to the Docs goes to https://loadout.lat/docs/; that is expected in previews.
- Seeded passwords, sessions and tokens are test values for this throwaway registry only.
- Without JavaScript: screenshot with `--disable-javascript` or check the HTML; every page must still work.
