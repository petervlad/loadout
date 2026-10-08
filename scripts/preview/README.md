# UI preview

Throwaway registry with sample data, for looking at the web pages and taking screenshots. Nothing here touches a real registry or your `~/.loadout`: everything lives under `$LOADOUT_PREVIEW_DIR` (default `$TMPDIR/loadout-preview`).

```sh
export PATH=/opt/homebrew/bin:$PATH            # Go 1.27
scripts/preview/seed.sh                         # builds loadout, seeds project acme with teams lime and koala, agents, a proposal with a report, users
scripts/preview/serve.sh                        # runs the registry on 127.0.0.1:7431 and the cookie proxy
scripts/preview/skills.sh                       # adds a skill, a KB (with Mermaid and draw.io diagrams) and a skill proposal (needs serve.sh running)
scripts/preview/behind.sh                       # adds a proposal two releases behind and a blocked one (after skills.sh)
scripts/preview/course.sh                       # wikis on, a long lime page, lime's course (7 lessons, quizzes, a broken quiz), acme's inherited by koala, koala's missing; olga-session owns acme
node scripts/preview/shoot.mjs jobs.json        # exact-width screenshots through Chrome's DevTools protocol
scripts/preview/stop.sh
```

The proxy (`proxy.py`) signs you in per port, so headless Chrome can open signed-in pages:

| Port | As | Theme |
| --- | --- | --- |
| 7441 | alice, maintainer of lime | light |
| 7442 | alice | dark |
| 7443 | bogdan, member of lime | light |
| 7447 | bogdan | dark |
| 7444 | signed out | light |
| 7446 | signed out | dark |

`jobs.json` is a list of `{"out": "/abs/file.png", "path": "/t/lime", "cookies": {"loadout_session": "alice-session"}, "scheme": "light"|"dark", "full": true, "width": 390}`; shoot.mjs talks to the registry on 7431 directly and sets the cookies itself. Headless Chrome's `--window-size` has a minimum of about 500 px, so use shoot.mjs for phone widths.

shoot.mjs opens pages on `http://localhost:7431`, the registry's public URL: the diagram frames' CSP names that origin, so diagrams do not draw through the 127.0.0.1 proxies. After each screenshot it prints what the page and every frame in it logged (console errors and warnings, exceptions, CSP violations); `LOADOUT_SHOOT_LOG=file.json` also writes them to a file. Set `"js"` to a script to run before the shot (`"jsWait"` ms after it, default 300), `"nojs": true` to shoot without JavaScript, `"contrast": "more"` for increased contrast, `"touch": true` for a touch screen (`pointer: coarse`) and `"wait"` (ms) to give diagrams time to draw; frames load lazily, so use a tall `"height"` rather than `"full"` for pages with several diagrams.

Another preview already on 7431 (another checkout)? Set `LOADOUT_PREVIEW_PORT` (and `LOADOUT_CDP_PORT` for Chrome, default 9333) for every script; seed.sh refuses to start when the port answers. The cookie proxy runs only on the default port.
