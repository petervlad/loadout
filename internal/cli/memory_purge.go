package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/petervlad/loadout/internal/memory"
	"github.com/petervlad/loadout/internal/registry"
)

// MemoryHost is what the operator's memory commands need on the registry
// host. *memory.Service is one. Neither method checks rights, so only code
// that runs on the host may hold one.
type MemoryHost interface {
	Inspect(ctx context.Context, id int64) (memory.Entry, int, error)
	Purge(ctx context.Context, id int64) (memory.Purged, error)
	WasPurged(ctx context.Context, id int64) (memory.Purged, bool, error)
}

var _ MemoryHost = (*memory.Service)(nil)

func newMemoryPurgeCmd(d Deps) *cobra.Command {
	var (
		id      int64
		dataDir string
		backups string
		yes     bool
	)
	cmd := &cobra.Command{
		Use:   "purge --id N --data DIR [--yes] [--backups DIR]",
		Short: "Remove a memory and all its history for good (on the registry host)",
		Long: `Remove a memory and every version of it, for text that should never have
been stored. Run it on the registry host. Without --yes it shows what would
go and stops. Afterwards it lists the nightly backups that still hold the
memory, from the backup folder (--backups, else LOADOUT_BACKUP_DIR).
Retracting a memory keeps its history, and every member can read that.

If a purge ends with a message that the database was busy, run the same
command again: the memory is already gone, and the second run only repeats
the cleanup of the database files.`,
		Example: "  loadout memory purge --id 42 --data /var/lib/loadout\n  loadout memory purge --id 42 --data /var/lib/loadout --yes --backups /var/backups/loadout",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if id <= 0 {
				return errors.New("name the memory to purge with --id N")
			}
			if backups == "" {
				backups = d.BackupDir
			}
			return withRegistry(cmd.Context(), d, dataDir, func(reg Registry) error {
				if reg.Memory == nil {
					return errors.New("this build has no team memory")
				}
				return purgeMemory(cmd.Context(), cmd.OutOrStdout(), reg.Memory, id, yes, backups)
			})
		},
	}
	f := cmd.Flags()
	f.Int64Var(&id, "id", 0, "id of the memory to purge")
	f.StringVar(&dataDir, "data", d.defaultDataDir(), "registry data folder (on the registry host)")
	f.StringVar(&backups, "backups", "", "folder of the nightly backups (default LOADOUT_BACKUP_DIR)")
	f.BoolVar(&yes, "yes", false, "purge, instead of only showing the memory")
	return cmd
}

// purgeMemory shows the memory and, with yes, purges it and lists the backups
// that still hold it. It prints no text of the memory.
func purgeMemory(ctx context.Context, out io.Writer, host MemoryHost, id int64, yes bool, backups string) error {
	e, versions, err := host.Inspect(ctx, id)
	if errors.Is(err, registry.ErrNotFound) {
		// A purge whose cleanup was busy leaves the entry gone, so a second
		// run has nothing to inspect. Its record says so.
		if prev, ok, perr := host.WasPurged(ctx, id); perr == nil && ok {
			return purgeAgain(ctx, out, host, prev, yes, backups)
		}
	}
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "Memory %d in %s, by %s, saved %s, last changed %s, %s.\n",
		id, e.Namespace, e.AuthorLabel, day(e.CreatedAt), day(e.UpdatedAt), plural(versions, "version", "versions"))
	if !yes {
		fmt.Fprintln(out, "Nothing was purged. To remove it and its history for good, run the same command with --yes.")
		return nil
	}
	p, err := host.Purge(ctx, id)
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "Purged memory %d and its %s.\n", id, plural(p.Versions, "version", "versions"))
	fmt.Fprintln(out, cacheNote)
	return printBackups(out, backups, p.CreatedAt, time.Now())
}

// cacheNote is true of the running registry, which keeps each team's index
// for 30 seconds.
const cacheNote = "A running registry keeps the purged name in its index cache for up to 30 seconds."

// purgeAgain handles a memory that is already purged: with yes it repeats the
// cleanup of the database files, which is what a busy first run left undone.
func purgeAgain(ctx context.Context, out io.Writer, host MemoryHost, prev memory.Purged, yes bool, backups string) error {
	fmt.Fprintf(out, "Memory %d was already purged on %s.\n", prev.EntryID, day(prev.At))
	if !yes {
		fmt.Fprintln(out, "Nothing was changed. To clean the database files again, for example after a purge that said the database was busy, run the same command with --yes.")
		return nil
	}
	if _, err := host.Purge(ctx, prev.EntryID); err != nil {
		return err
	}
	fmt.Fprintf(out, "Cleaned the database files again for memory %d.\n", prev.EntryID)
	fmt.Fprintln(out, cacheNote)
	// The record keeps no date of the entry, so every backup is listed.
	return printBackups(out, backups, time.Time{}, time.Now())
}

// printBackups says which backups made since the memory was saved still hold
// it, and what this list cannot know.
func printBackups(out io.Writer, dir string, from, to time.Time) error {
	if dir == "" {
		fmt.Fprintln(out, "No backup folder is set, so no backups were checked. Give --backups DIR or set LOADOUT_BACKUP_DIR.")
	} else {
		names, err := memory.BackupsHolding(dir, from, to)
		if err != nil {
			return fmt.Errorf("the memory is purged, but %w", err)
		}
		if len(names) == 0 {
			fmt.Fprintf(out, "No backup in %s holds it.\n", dir)
		} else {
			fmt.Fprintf(out, "These backups in %s still hold it:\n  %s\n", dir, strings.Join(names, "\n  "))
		}
	}
	fmt.Fprintln(out, "Copies made off this machine are not listed. Rotate the secret.")
	return nil
}

func day(t time.Time) string { return t.UTC().Format("2006-01-02") }
