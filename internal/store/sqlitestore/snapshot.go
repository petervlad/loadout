package sqlitestore

import (
	"context"
	"fmt"
)

// SnapshotTo writes a consistent copy of the database to path with SQLite's
// VACUUM INTO. It reads in one transaction, so in WAL mode publishes keep
// going while it runs, and it refuses a file that already has content, so a
// copy never overwrites an earlier one.
func (s *Store) SnapshotTo(ctx context.Context, path string) error {
	if _, err := s.db.ExecContext(ctx, `VACUUM INTO ?`, path); err != nil {
		return fmt.Errorf("copy the database to %s: %w", path, err)
	}
	return nil
}
