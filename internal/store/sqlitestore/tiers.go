package sqlitestore

import (
	"context"
	"errors"
	"fmt"

	"github.com/petervlad/loadout/internal/agent"
	"github.com/petervlad/loadout/internal/registry"
	"github.com/petervlad/loadout/internal/tier"
)

// ModelTiers implements registry.NamespaceStore.
func (s *Store) ModelTiers(ctx context.Context, name string) (tier.Table, error) {
	if _, err := s.Namespace(ctx, name); err != nil {
		return tier.Table{}, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT tool, hint, model, effort FROM model_tiers WHERE namespace = ?`, name)
	if err != nil {
		return tier.Table{}, fmt.Errorf("model tiers of %s: %w", name, err)
	}
	defer rows.Close()
	var cells []tier.Cell
	for rows.Next() {
		var c tier.Cell
		var hint, effort string
		if err := rows.Scan(&c.Tool, &hint, &c.Model, &effort); err != nil {
			return tier.Table{}, fmt.Errorf("model tiers of %s: %w", name, err)
		}
		c.Hint, c.Effort = agent.Model(hint), tier.Effort(effort)
		cells = append(cells, c)
	}
	if err := rows.Err(); err != nil {
		return tier.Table{}, fmt.Errorf("model tiers of %s: %w", name, err)
	}
	t, err := tier.FromCells(cells)
	if err != nil {
		return tier.Table{}, fmt.Errorf("stored model tiers of %s: %w", name, err)
	}
	return t, nil
}

// SetModelTiers implements registry.NamespaceStore: the namespace's cells
// are replaced in one transaction.
func (s *Store) SetModelTiers(ctx context.Context, name string, t tier.Table) (err error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("set model tiers of %s: %w", name, err)
	}
	defer func() {
		if err != nil {
			err = errors.Join(err, tx.Rollback())
		}
	}()
	var exists bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM namespaces WHERE name = ?)`, name).Scan(&exists); err != nil {
		return fmt.Errorf("set model tiers of %s: %w", name, err)
	}
	if !exists {
		return fmt.Errorf("set model tiers of %s: %w", name, registry.ErrNotFound)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM model_tiers WHERE namespace = ?`, name); err != nil {
		return fmt.Errorf("set model tiers of %s: %w", name, err)
	}
	for _, c := range t.Cells() {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO model_tiers (namespace, tool, hint, model, effort) VALUES (?, ?, ?, ?, ?)`,
			name, c.Tool, string(c.Hint), c.Model, string(c.Effort)); err != nil {
			return fmt.Errorf("set model tiers of %s: %w", name, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("set model tiers of %s: %w", name, err)
	}
	return nil
}
