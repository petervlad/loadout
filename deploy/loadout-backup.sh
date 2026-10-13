#!/bin/sh
# Back up the loadout data dir to a timestamped tar.gz and prune old ones.
# SQLite files are copied with `sqlite3 .backup`, which is consistent while
# the server is running (WAL mode); everything else is copied as is.
# Installed to /usr/local/lib/loadout/ and run by loadout-backup.service.
set -eu

DATA="${LOADOUT_DATA:-/var/lib/loadout}"
DEST="${LOADOUT_BACKUP_DIR:-/var/backups/loadout}"
KEEP_DAYS="${LOADOUT_BACKUP_KEEP_DAYS:-14}"

command -v sqlite3 >/dev/null 2>&1 || {
	echo "loadout-backup: sqlite3 is not installed" >&2
	exit 1
}
[ -d "$DATA" ] || {
	echo "loadout-backup: $DATA does not exist" >&2
	exit 1
}

stamp=$(date -u +%Y%m%dT%H%M%SZ)
work=$(mktemp -d "$DEST/.work.XXXXXX")
trap 'rm -rf "$work"' EXIT INT TERM

# Plain files first, without SQLite databases, their WAL/SHM files, or
# loadout's own log dir.
tar -C "$DATA" \
	--exclude='*.db' --exclude='*.sqlite' --exclude='*.sqlite3' \
	--exclude='*-wal' --exclude='*-shm' --exclude='*-journal' \
	--exclude='./.loadout' \
	-cf - . | tar -C "$work" -xf -

# Then a consistent copy of each database.
find "$DATA" -type f \( -name '*.db' -o -name '*.sqlite' -o -name '*.sqlite3' \) |
	while IFS= read -r db; do
		rel="${db#"$DATA"/}"
		mkdir -p "$work/$(dirname "$rel")"
		sqlite3 "$db" ".backup '$work/$rel'"
		# A self-contained file: no -wal/-shm next to the copy. The server
		# switches back to WAL when it opens a restored database.
		sqlite3 "$work/$rel" 'PRAGMA journal_mode=DELETE;' >/dev/null
		result=$(sqlite3 "$work/$rel" 'PRAGMA integrity_check;')
		[ "$result" = ok ] || {
			echo "loadout-backup: integrity check failed for $rel: $result" >&2
			exit 1
		}
		# Closed and in DELETE mode, so any leftover -shm/-wal is empty.
		rm -f "$work/$rel-shm" "$work/$rel-wal"
	done

out="$DEST/loadout-$stamp.tar.gz"
tar -C "$work" -czf "$out.tmp" .
mv "$out.tmp" "$out"
echo "loadout-backup: wrote $out ($(du -h "$out" | cut -f 1))"

find "$DEST" -maxdepth 1 -type f -name 'loadout-*.tar.gz' -mtime +"$KEEP_DAYS" -print -delete
# The server's copies from before a package delete, kept as long as the nightly archives.
find "$DEST" -maxdepth 1 -type f -name 'before-delete-*.db' -mtime +"$KEEP_DAYS" -print -delete
