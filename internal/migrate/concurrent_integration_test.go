package migrate

import (
	"context"
	"os"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	fleetdb "github.com/iag/fleet-tool/backend/db"
)

// Replicas booting together with AUTO_MIGRATE=true each run Up against the same
// database. Every one must succeed and each migration must be applied exactly
// once. Needs a database that has never been migrated, so it skips on one that
// has — it is meant for a throwaway instance:
//
//	TEST_DATABASE_URL=postgres://...&search_path=iag_fleet,public \
//	  go test ./internal/migrate/... -run Integration_UpConcurrent -p 1
func TestIntegration_UpConcurrentReplicas(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set — skipping concurrent migrate integration test")
	}
	ctx := context.Background()
	const replicas = 5
	pools := make([]*pgxpool.Pool, replicas)
	for i := range pools {
		p, err := pgxpool.New(ctx, url)
		if err != nil {
			t.Fatalf("connect: %v", err)
		}
		t.Cleanup(p.Close)
		pools[i] = p
	}

	var migrated bool
	if err := pools[0].QueryRow(ctx,
		`SELECT to_regclass('iag_fleet.schema_migrations') IS NOT NULL`).Scan(&migrated); err != nil {
		t.Fatal(err)
	}
	if migrated {
		t.Skip("iag_fleet.schema_migrations already exists — this test needs an empty database")
	}

	var wg sync.WaitGroup
	errs := make([]error, replicas)
	start := make(chan struct{})
	for i := range pools {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			_, errs[i] = Up(ctx, pools[i], fleetdb.Migrations())
		}(i)
	}
	close(start)
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Errorf("replica %d: %v", i, err)
		}
	}

	migs, err := load(fleetdb.Migrations())
	if err != nil {
		t.Fatal(err)
	}
	var got int
	if err := pools[0].QueryRow(ctx,
		`SELECT count(*) FROM iag_fleet.schema_migrations`).Scan(&got); err != nil {
		t.Fatal(err)
	}
	if got != len(migs) {
		t.Fatalf("ledger holds %d migrations, want %d", got, len(migs))
	}
}
