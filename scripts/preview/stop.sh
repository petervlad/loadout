#!/bin/bash
# Stops the preview registry on $LOADOUT_PREVIEW_PORT (7431), and the cookie
# proxy when that is the default port (the only one it serves).
PORT=${LOADOUT_PREVIEW_PORT:-7431}
pkill -f "loadout serve --addr 127.0.0.1:$PORT" || true
if [ "$PORT" = 7431 ]; then
  pkill -f "preview/proxy.py" || true
fi
