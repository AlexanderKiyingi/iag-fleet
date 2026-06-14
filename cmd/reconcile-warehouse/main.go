// Command reconcile-warehouse compares fleet's spare-parts catalogue against
// the iag-warehouse ("stores") item master ahead of, or during, the stock
// delegation rollout. It is the Phase 0 instrument: quantify the SKU overlap
// before warehouse becomes the system-of-record for parts stock.
//
// It connects directly to BOTH databases (read-only by default) and reports:
//   - matched     : SKUs present in both fleet.parts and warehouse.wh_items
//   - fleet-only   : parts fleet carries that warehouse has no item for
//   - warehouse-only: warehouse items with no matching fleet part
//
// With --backfill it writes the resolved warehouse item id onto the matching
// fleet part (parts.warehouse_item_id), which the issue path and the event
// consumer both prefer over a live SKU lookup.
//
// Usage:
//
//	DATABASE_URL=postgres://…/fleet \
//	WAREHOUSE_DATABASE_URL=postgres://…/warehouse \
//	go run ./cmd/reconcile-warehouse [--backfill] [--json]
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"sort"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

type partRow struct {
	ID  string
	SKU string
}

type report struct {
	Matched        []matchRow `json:"matched"`
	FleetOnly      []string   `json:"fleet_only"`
	WarehouseOnly  []string   `json:"warehouse_only"`
	BackfilledRows int        `json:"backfilled_rows"`
}

type matchRow struct {
	SKU         string `json:"sku"`
	FleetPartID string `json:"fleet_part_id"`
	WarehouseID string `json:"warehouse_item_id"`
}

func main() {
	backfill := flag.Bool("backfill", false, "write resolved warehouse item ids onto matching fleet parts")
	asJSON := flag.Bool("json", false, "emit the report as JSON")
	flag.Parse()

	fleetURL := os.Getenv("DATABASE_URL")
	whURL := os.Getenv("WAREHOUSE_DATABASE_URL")
	if fleetURL == "" || whURL == "" {
		fmt.Fprintln(os.Stderr, "DATABASE_URL (fleet) and WAREHOUSE_DATABASE_URL are both required")
		os.Exit(2)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	fleetPool, err := pgxpool.New(ctx, fleetURL)
	if err != nil {
		fatal("connect fleet db", err)
	}
	defer fleetPool.Close()
	whPool, err := pgxpool.New(ctx, whURL)
	if err != nil {
		fatal("connect warehouse db", err)
	}
	defer whPool.Close()

	// Fleet parts keyed by SKU.
	fleetBySKU := map[string]partRow{}
	rows, err := fleetPool.Query(ctx, `SELECT id, COALESCE(sku,'') FROM parts`)
	if err != nil {
		fatal("query fleet parts", err)
	}
	for rows.Next() {
		var p partRow
		if err := rows.Scan(&p.ID, &p.SKU); err != nil {
			fatal("scan fleet part", err)
		}
		if p.SKU != "" {
			fleetBySKU[p.SKU] = p
		}
	}
	rows.Close()

	// Warehouse items keyed by SKU.
	whBySKU := map[string]string{} // sku -> item id
	wrows, err := whPool.Query(ctx, `SELECT id::text, sku FROM wh_items`)
	if err != nil {
		fatal("query warehouse items", err)
	}
	for wrows.Next() {
		var id, sku string
		if err := wrows.Scan(&id, &sku); err != nil {
			fatal("scan warehouse item", err)
		}
		whBySKU[sku] = id
	}
	wrows.Close()

	var rep report
	for sku, part := range fleetBySKU {
		if whID, ok := whBySKU[sku]; ok {
			rep.Matched = append(rep.Matched, matchRow{SKU: sku, FleetPartID: part.ID, WarehouseID: whID})
		} else {
			rep.FleetOnly = append(rep.FleetOnly, sku)
		}
	}
	for sku := range whBySKU {
		if _, ok := fleetBySKU[sku]; !ok {
			rep.WarehouseOnly = append(rep.WarehouseOnly, sku)
		}
	}
	sort.Slice(rep.Matched, func(i, j int) bool { return rep.Matched[i].SKU < rep.Matched[j].SKU })
	sort.Strings(rep.FleetOnly)
	sort.Strings(rep.WarehouseOnly)

	if *backfill {
		for _, m := range rep.Matched {
			tag, err := fleetPool.Exec(ctx,
				`UPDATE parts SET warehouse_item_id = $1 WHERE id = $2 AND (warehouse_item_id IS NULL OR warehouse_item_id = '')`,
				m.WarehouseID, m.FleetPartID)
			if err != nil {
				fatal("backfill part "+m.FleetPartID, err)
			}
			rep.BackfilledRows += int(tag.RowsAffected())
		}
	}

	if *asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(rep)
		return
	}

	fmt.Printf("Fleet parts with SKU : %d\n", len(fleetBySKU))
	fmt.Printf("Warehouse items      : %d\n", len(whBySKU))
	fmt.Printf("Matched              : %d\n", len(rep.Matched))
	fmt.Printf("Fleet-only (no wh)   : %d\n", len(rep.FleetOnly))
	fmt.Printf("Warehouse-only       : %d\n", len(rep.WarehouseOnly))
	if *backfill {
		fmt.Printf("Backfilled parts     : %d\n", rep.BackfilledRows)
	}
	if len(rep.FleetOnly) > 0 {
		fmt.Println("\nFleet-only SKUs (need a warehouse item before delegation):")
		for _, s := range rep.FleetOnly {
			fmt.Printf("  - %s\n", s)
		}
	}
}

func fatal(msg string, err error) {
	fmt.Fprintf(os.Stderr, "reconcile-warehouse: %s: %v\n", msg, err)
	os.Exit(1)
}
