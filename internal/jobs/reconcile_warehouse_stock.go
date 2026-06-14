package jobs

import (
	"context"
	"log/slog"

	"github.com/jackc/pgx/v5/pgxpool"
)

// WarehouseOnHand reads the authoritative on-hand for a warehouse item.
// Satisfied by *warehouseclient.Client.
type WarehouseOnHand interface {
	OnHandTotal(ctx context.Context, itemID string) (float64, error)
}

// ReconcileWarehouseStockResult summarizes one reconcile pass.
type ReconcileWarehouseStockResult struct {
	Checked int
	Updated int
	Errors  int
}

// ReconcileWarehouseStock refreshes parts.stock from the authoritative
// iag-warehouse on-hand for every part mapped to a warehouse item. It is the
// drift-correcting complement to the event consumer: where the consumer reacts
// to individual movements, this sweeps every mapped part so a missed or
// out-of-order event self-heals on the next pass. The update is a SET (not a
// delta), so it is idempotent and safe to run on any cadence.
func ReconcileWarehouseStock(ctx context.Context, pool *pgxpool.Pool, wh WarehouseOnHand) (ReconcileWarehouseStockResult, error) {
	var res ReconcileWarehouseStockResult

	rows, err := pool.Query(ctx,
		`SELECT id, warehouse_item_id FROM parts
		  WHERE warehouse_item_id IS NOT NULL AND warehouse_item_id <> ''`)
	if err != nil {
		return res, err
	}
	type partRef struct{ id, itemID string }
	var refs []partRef
	for rows.Next() {
		var p partRef
		if err := rows.Scan(&p.id, &p.itemID); err != nil {
			rows.Close()
			return res, err
		}
		refs = append(refs, p)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return res, err
	}

	for _, p := range refs {
		if ctx.Err() != nil {
			return res, ctx.Err()
		}
		res.Checked++
		total, err := wh.OnHandTotal(ctx, p.itemID)
		if err != nil {
			res.Errors++
			slog.Warn("reconcile warehouse stock: on-hand fetch failed", "part", p.id, "item", p.itemID, "err", err)
			continue
		}
		tag, err := pool.Exec(ctx,
			`UPDATE parts SET stock = $1, warehouse_synced_at = now() WHERE id = $2`,
			int(total), p.id)
		if err != nil {
			res.Errors++
			slog.Warn("reconcile warehouse stock: update failed", "part", p.id, "err", err)
			continue
		}
		res.Updated += int(tag.RowsAffected())
	}
	return res, nil
}
