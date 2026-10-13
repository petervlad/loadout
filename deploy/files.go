// Package deploy holds the files that set up a loadout registry host, so the
// program can hand them to whoever sets one up without a second copy that
// could drift from these. The directory's own README.md describes them.
package deploy

import "embed"

// FS is every file a host needs, at the paths used in this folder:
// prepare.sh and deploy.sh, the systemd units and backup script, the Caddy
// files, install.sh, cloud-init.yaml, loadout.env.example, and container/.
//
//go:embed prepare.sh deploy.sh cloud-init.yaml Caddyfile site.caddy downloads.routes install.sh loadout.service loadout-backup.service loadout-backup.timer loadout-backup.sh loadout.env.example container/Dockerfile container/fly.toml.example
var FS embed.FS
