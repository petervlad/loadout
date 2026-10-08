# Backups and a restore test

The registry is one folder, `/var/lib/loadout` (a SQLite database `registry.db` and the files beside it). `loadout-backup.timer` runs nightly at about 03:15 and writes `/var/backups/loadout/loadout-TIME.tar.gz` (UTC time), using `sqlite3 .backup`, so the copy is consistent while the server runs, and keeps 14 days. Those archives live on the same disk, so a lost machine takes them along. Off-host means a copy somewhere else, and a copy counts only after a restore has worked.

## Two layers, use both

1. **The provider's snapshots or backups of the machine**, as the provider file says: simplest, whole machine, but taken at the provider's time and with the provider's retention.
2. **A pull of the nightly archive to the person's own computer or another storage they choose**, which is a file the person can restore anywhere.

## Pull the newest archive

The backup folder is private to the `loadout` user, so read it through sudo. From the person's computer (not as a long-lived job; ask before creating a cron entry):

```sh
mkdir -p ~/loadout-backups
latest=$(ssh admin@IP sudo ls -1 /var/backups/loadout | grep '^loadout-' | tail -1)
ssh admin@IP sudo cat "/var/backups/loadout/$latest" > ~/loadout-backups/"$latest"
```

The names sort by time. Check the file is not empty and `tar -tzf ~/loadout-backups/"$latest" | head` lists files. The person decides where else it goes (an encrypted disk, an object store they already use); the archive holds every account's data, so it stays private. If the person wants it automatic, show them the two lines above in a script and the schedule they pick, and let them install it; do not store a login for them.

## Restore test into a scratch folder

Run it after the first deploy and again after any big change. It touches nothing live:

```sh
mkdir -p /tmp/loadout-restore-test
tar -C /tmp/loadout-restore-test -xzf ~/loadout-backups/"$latest"
sqlite3 /tmp/loadout-restore-test/registry.db 'PRAGMA integrity_check;'
```

The last line must print `ok`. Delete the scratch folder after. If `sqlite3` is missing on the person's computer, run the same three lines on the host instead (it has it): copy the archive there, extract under `/tmp`, check, delete.

## Restoring for real

Only when the person asks and after a fresh copy of what is there now:

```sh
sudo systemctl stop loadout
sudo find /var/lib/loadout -mindepth 1 -delete
sudo tar -C /var/lib/loadout -xzf /var/backups/loadout/loadout-TIME.tar.gz
sudo chown -R loadout:loadout /var/lib/loadout
sudo systemctl start loadout
```

The container shape has no archive unit: use the platform's volume snapshots (see the provider file) and restore into a new volume.
