// Command migrate applies the embedded schema migrations out of band.
//
// Production sets ENVIRONMENT=production, and config.Validate then refuses to
// start the API unless AUTO_MIGRATE=false — correct, because several replicas
// racing to migrate on boot is how a schema ends up half-applied. But the
// runbook's other half, "run migrations out of band", had nothing to run:
// migrate.Up was reachable only from the API's own boot path. The documented
// production configuration was therefore a catch-22, and the practical escape
// was to leave ENVIRONMENT unset — which is exactly how the fail-open RBAC
// window stayed open.
//
//	migrate -dry-run    # report what would be applied, change nothing
//	migrate             # apply pending migrations
//
// Deploy order is: run this, then start the API. It is safe to run when there
// is nothing to do, and safe to run twice — migrations are recorded by version
// with a checksum, so a second run applies nothing.
//
// DATABASE_URL is read directly rather than through config.Load, because the
// full validator demands a service client secret, a CORS allowlist and a JWKS
// URL, none of which a migration run has any business needing and all of which
// would block it for no reason.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	fleetdb "github.com/iag/fleet-tool/backend/db"
	"github.com/iag/fleet-tool/backend/internal/migrate"
)

func main() {
	dryRun := flag.Bool("dry-run", false, "list pending migrations and exit without applying anything")
	timeout := flag.Duration("timeout", 10*time.Minute, "overall time limit for the run")
	flag.Parse()

	databaseURL := strings.TrimSpace(os.Getenv("DATABASE_URL"))
	if databaseURL == "" {
		log.Fatal("DATABASE_URL is required")
	}

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()

	// Deliberately a small pool: this is a one-shot task, and on a shared
	// database a deploy step should not hold connections a running service needs.
	cfg, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		log.Fatalf("parse DATABASE_URL: %v", err)
	}
	cfg.MaxConns = 2
	// Match the API's schema isolation, or the ledger and the tables would land
	// in different schemas depending on which binary touched the database first.
	if cfg.ConnConfig.RuntimeParams == nil {
		cfg.ConnConfig.RuntimeParams = map[string]string{}
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = "iag_fleet, public"

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		log.Fatalf("database: %v", err)
	}
	defer pool.Close()
	if err := pool.Ping(ctx); err != nil {
		log.Fatalf("database unreachable: %v", err)
	}

	// The iag_fleet schema has to exist before the ledger can be created in it.
	if _, err := pool.Exec(ctx, `CREATE SCHEMA IF NOT EXISTS iag_fleet`); err != nil {
		log.Fatalf("ensure schema iag_fleet: %v", err)
	}

	applied, pending, err := migrate.Status(ctx, pool, fleetdb.Migrations())
	if err != nil {
		log.Fatalf("status: %v", err)
	}

	fmt.Printf("%s: %d applied, %d pending\n", redact(databaseURL), len(applied), len(pending))
	for _, v := range pending {
		fmt.Printf("  pending  %s\n", v)
	}
	if len(pending) == 0 {
		fmt.Println("nothing to do")
		return
	}
	if *dryRun {
		fmt.Println("dry run — nothing was applied")
		return
	}

	start := time.Now()
	done, err := migrate.Up(ctx, pool, fleetdb.Migrations())
	if err != nil {
		// Each migration applies in its own transaction, so the failing one has
		// rolled back — but earlier ones in this run are already committed. Say
		// so plainly: "run it again" is the right move, and pretending nothing
		// happened would be worse than an awkward message.
		if len(done) > 0 {
			log.Fatalf("migrate failed after applying %v: %v\n"+
				"those are committed; re-run to continue from %s", done, err, pending[len(done)])
		}
		log.Fatalf("migrate failed (nothing applied): %v", err)
	}
	for _, v := range done {
		fmt.Printf("  applied  %s\n", v)
	}

	// Verify rather than trust the return value: the point of this command is to
	// be able to say the schema is ready before starting the API.
	_, stillPending, err := migrate.Status(ctx, pool, fleetdb.Migrations())
	if err != nil {
		log.Fatalf("verify: %v", err)
	}
	if len(stillPending) > 0 {
		log.Fatalf("reported success but %d migration(s) are still pending: %v",
			len(stillPending), stillPending)
	}
	fmt.Printf("applied %d migration(s) in %s\n", len(done), time.Since(start).Round(time.Millisecond))
}

// redact strips credentials from the connection string before printing, since
// this command's output is the sort of thing pasted into a deploy log.
func redact(databaseURL string) string {
	at := strings.LastIndex(databaseURL, "@")
	scheme := strings.Index(databaseURL, "://")
	if at < 0 || scheme < 0 || at < scheme {
		return databaseURL
	}
	return databaseURL[:scheme+3] + "***@" + databaseURL[at+1:]
}
