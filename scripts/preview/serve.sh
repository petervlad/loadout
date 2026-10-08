#!/bin/bash
# Runs the seeded preview registry on 127.0.0.1:$LOADOUT_PREVIEW_PORT (7431)
# and, on the default port, the cookie proxy. The proxy's ports are fixed,
# so a second preview on another port runs without one.
set -euo pipefail
S=${LOADOUT_PREVIEW_DIR:-${TMPDIR:-/tmp}/loadout-preview}
here=$(cd "$(dirname "$0")" && pwd)
PORT=${LOADOUT_PREVIEW_PORT:-7431}
export LOADOUT_PREVIEW_PORT=$PORT
"$here/stop.sh" >/dev/null 2>&1 || true
LOADOUT_PUBLIC_URL=http://localhost:$PORT LOADOUT_DEV=1 HOME=$S/home nohup "$S/loadout" serve --addr 127.0.0.1:$PORT --data "$S/data" >"$S/serve.log" 2>&1 &
if [ "$PORT" = 7431 ]; then
  nohup python3 "$here/proxy.py" >"$S/proxy.log" 2>&1 &
fi
for _ in $(seq 50); do curl -fs http://127.0.0.1:$PORT/v1/health >/dev/null && break; sleep 0.1; done
echo "registry on http://localhost:$PORT"
[ "$PORT" = 7431 ] && echo "signed-in proxies on 7441-7447"
exit 0
