package store

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/iag/fleet-tool/backend/internal/models"
)

// Repository is the application-wide handle to all collections plus the
// auxiliary state (audit log, ticker) that doesn't fit the generic
// Collection pattern. It owns the pgx pool but does not close it; the
// caller owns the pool lifecycle.
type Repository struct {
	pool *pgxpool.Pool

	Vehicles    *Collection[models.Vehicle, *models.Vehicle]
	Drivers     *Collection[models.Driver, *models.Driver]
	JMPs        *Collection[models.JMP, *models.JMP]
	Cargo       *Collection[models.Cargo, *models.Cargo]
	CargoDocs   *Collection[models.CargoDoc, *models.CargoDoc]
	Fuel        *Collection[models.FuelRecord, *models.FuelRecord]
	Maintenance *Collection[models.MaintenanceItem, *models.MaintenanceItem]
	Parts       *Collection[models.Part, *models.Part]
	Tyres       *Collection[models.Tyre, *models.Tyre]
	Trips       *Collection[models.Trip, *models.Trip]
	Safety      *Collection[models.SafetyEvent, *models.SafetyEvent]
	Compliance  *Collection[models.ComplianceItem, *models.ComplianceItem]
	Requests    *Collection[models.ServiceRequest, *models.ServiceRequest]
	Tasks       *Collection[models.TaskItem, *models.TaskItem]
	Deployment  *Collection[models.DeploymentDay, *models.DeploymentDay]

	// Notifications is the per-user signal log surfaced by the bell.
	// Doesn't follow the generic Collection pattern because every read /
	// write is implicitly user-scoped.
	Notifications *NotificationsStore
}

// NewRepository wires every Collection to its Postgres table.
func NewRepository(pool *pgxpool.Pool) *Repository {
	return &Repository{
		pool:        pool,
		Vehicles:    NewCollection[models.Vehicle, *models.Vehicle](pool, "vehicles"),
		Drivers:     NewCollection[models.Driver, *models.Driver](pool, "drivers"),
		JMPs:        NewCollection[models.JMP, *models.JMP](pool, "jmps"),
		Cargo:       NewCollection[models.Cargo, *models.Cargo](pool, "cargo"),
		CargoDocs:   NewCollection[models.CargoDoc, *models.CargoDoc](pool, "cargo_docs"),
		Fuel:        NewCollection[models.FuelRecord, *models.FuelRecord](pool, "fuel_records"),
		Maintenance: NewCollection[models.MaintenanceItem, *models.MaintenanceItem](pool, "maintenance_items"),
		Parts:       NewCollection[models.Part, *models.Part](pool, "parts"),
		Tyres:       NewCollection[models.Tyre, *models.Tyre](pool, "tyres"),
		Trips:       NewCollection[models.Trip, *models.Trip](pool, "trips"),
		Safety:      NewCollection[models.SafetyEvent, *models.SafetyEvent](pool, "safety_events"),
		Compliance:  NewCollection[models.ComplianceItem, *models.ComplianceItem](pool, "compliance_items"),
		Requests:    NewCollection[models.ServiceRequest, *models.ServiceRequest](pool, "service_requests"),
		Tasks:       NewCollection[models.TaskItem, *models.TaskItem](pool, "task_items"),
		Deployment:  NewCollection[models.DeploymentDay, *models.DeploymentDay](pool, "deployment_days"),

		Notifications: &NotificationsStore{pool: pool},
	}
}

// ────────────────────────────── Audit ──────────────────────────────────

func (r *Repository) Audit(ctx context.Context) ([]models.AuditEntry, error) {
	const q = `
        SELECT to_char(ts AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS.MS"Z"') AS ts,
               action, entity, ref_id, COALESCE(details, ''), "user"
        FROM audit_entries
        ORDER BY ts DESC
        LIMIT 500`
	rows, err := r.pool.Query(ctx, q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []models.AuditEntry
	for rows.Next() {
		var e models.AuditEntry
		if err := rows.Scan(&e.TS, &e.Action, &e.Entity, &e.ID, &e.Details, &e.User); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// Log appends one audit entry. user_id is resolved by username lookup if
// the username matches a registered account; otherwise the row records
// the username string only.
func (r *Repository) Log(ctx context.Context, action, entity, id, details, user string) (models.AuditEntry, error) {
	if user == "" {
		user = "anonymous"
	}
	const q = `
        INSERT INTO audit_entries (action, entity, ref_id, details, user_id, "user")
        VALUES ($1, $2, $3, NULLIF($4, ''),
                (SELECT id FROM users WHERE username = $5),
                $5)
        RETURNING to_char(ts AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS.MS"Z"') AS ts,
                  action, entity, ref_id, COALESCE(details, ''), "user"`
	var e models.AuditEntry
	err := r.pool.QueryRow(ctx, q, action, entity, id, details, user).Scan(
		&e.TS, &e.Action, &e.Entity, &e.ID, &e.Details, &e.User,
	)
	return e, err
}

// LogBest is the fire-and-forget variant the handler layer uses on the
// happy path: it swallows any audit-write error so a failed log never
// fails the underlying domain operation. Tradeoff: rare audit gaps in
// exchange for never short-circuiting the user request on a logging blip.
func (r *Repository) LogBest(ctx context.Context, action, entity, id, details, user string) {
	_, _ = r.Log(ctx, action, entity, id, details, user)
}

// AuditFilter is the query shape for AuditSearch. Empty fields are not
// applied — pass only what you want to filter on. Time fields use UTC and
// inclusive lower / exclusive upper bounds (`ts >= From AND ts < To`),
// which avoids double-counting an entry that lands exactly on a boundary
// when paging by day.
type AuditFilter struct {
	Entity string
	RefID  string
	// User matches against the username string snapshot. ILIKE so
	// callers can pass partials ("ali" matches "alice"). Case-insensitive.
	User string
	// Action ILIKE — useful for finding workflow transitions like
	// "advance:%" or "stage:in-transit".
	Action string
	// Q is a free-text search across action, details, and ref_id.
	// Implemented as ILIKE %q% so it doesn't need full-text indexes.
	Q string
	// From / To are inclusive lower / exclusive upper bounds.
	From *time.Time
	To   *time.Time
	// Limit is capped server-side; see auditMaxLimit.
	Limit  int
	Offset int
}

const (
	auditDefaultLimit = 50
	auditMaxLimit     = 500
)

// AuditSearch runs a filtered, paginated audit query. Returns the page of
// entries plus the total row count matching the filter (so the UI can
// render "1-50 of 1,243" without a second round-trip).
func (r *Repository) AuditSearch(ctx context.Context, f AuditFilter) ([]models.AuditEntry, int, error) {
	if f.Limit <= 0 {
		f.Limit = auditDefaultLimit
	}
	if f.Limit > auditMaxLimit {
		f.Limit = auditMaxLimit
	}
	if f.Offset < 0 {
		f.Offset = 0
	}

	// Build the WHERE clause incrementally. We share the predicate between
	// the count and the page query so a row that matches one matches the
	// other — important for accurate pagination.
	conds, args := auditWhere(f)
	where := ""
	if len(conds) > 0 {
		where = " WHERE " + strings.Join(conds, " AND ")
	}

	// Count first. Cheap on a 500-row legacy cap or even 1M+ with the
	// existing indexes; we still pay for it but it's a single round-trip.
	var total int
	countQ := "SELECT COUNT(*) FROM audit_entries" + where
	if err := r.pool.QueryRow(ctx, countQ, args...).Scan(&total); err != nil {
		return nil, 0, err
	}

	// Page query — same args, plus limit/offset appended last.
	pageArgs := append(append([]any{}, args...), f.Limit, f.Offset)
	pageQ := fmt.Sprintf(`
        SELECT to_char(ts AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS.MS"Z"') AS ts,
               action, entity, ref_id, COALESCE(details, ''), "user"
        FROM audit_entries%s
        ORDER BY ts DESC
        LIMIT $%d OFFSET $%d`, where, len(args)+1, len(args)+2)

	rows, err := r.pool.Query(ctx, pageQ, pageArgs...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	out := make([]models.AuditEntry, 0, f.Limit)
	for rows.Next() {
		var e models.AuditEntry
		if err := rows.Scan(&e.TS, &e.Action, &e.Entity, &e.ID, &e.Details, &e.User); err != nil {
			return nil, 0, err
		}
		out = append(out, e)
	}
	return out, total, rows.Err()
}

// auditWhere returns the dynamic WHERE clauses + placeholder args for an
// AuditFilter. Kept as a free function so AuditSearch can share the
// predicate between count and page queries without duplicating logic.
func auditWhere(f AuditFilter) ([]string, []any) {
	conds := make([]string, 0, 7)
	args := make([]any, 0, 7)
	add := func(cond string, val any) {
		args = append(args, val)
		conds = append(conds, fmt.Sprintf(cond, len(args)))
	}
	if f.Entity != "" {
		add("entity = $%d", f.Entity)
	}
	if f.RefID != "" {
		add("ref_id = $%d", f.RefID)
	}
	if f.User != "" {
		add(`"user" ILIKE $%d`, "%"+f.User+"%")
	}
	if f.Action != "" {
		add("action ILIKE $%d", "%"+f.Action+"%")
	}
	if f.Q != "" {
		// One arg, three predicates — combine into a single OR group so
		// the AND-join with other filters stays correct.
		args = append(args, "%"+f.Q+"%")
		i := len(args)
		conds = append(conds,
			fmt.Sprintf("(action ILIKE $%d OR COALESCE(details,'') ILIKE $%d OR ref_id ILIKE $%d)", i, i, i))
	}
	if f.From != nil {
		add("ts >= $%d", f.From.UTC())
	}
	if f.To != nil {
		add("ts < $%d", f.To.UTC())
	}
	return conds, args
}

// ─────────────────────────── Operator ticker ───────────────────────────

func (r *Repository) Ticker(ctx context.Context) (models.OperatorTicker, error) {
	var t models.OperatorTicker
	err := r.pool.QueryRow(ctx,
		`SELECT diesel, ugx, operator, role FROM operator_ticker WHERE id = 'singleton'`,
	).Scan(&t.Diesel, &t.Ugx, &t.Operator, &t.Role)
	if errors.Is(err, pgx.ErrNoRows) {
		return models.OperatorTicker{}, nil
	}
	return t, err
}

// PatchTicker applies non-zero fields onto the singleton row. Mirrors the
// previous in-memory semantics so the existing handler doesn't change.
func (r *Repository) PatchTicker(ctx context.Context, patch models.OperatorTicker) (models.OperatorTicker, error) {
	const q = `
        UPDATE operator_ticker SET
            diesel   = CASE WHEN $1 <> 0  THEN $1 ELSE diesel   END,
            ugx      = CASE WHEN $2 <> 0  THEN $2 ELSE ugx      END,
            operator = CASE WHEN $3 <> '' THEN $3 ELSE operator END,
            role     = CASE WHEN $4 <> '' THEN $4 ELSE role     END
        WHERE id = 'singleton'
        RETURNING diesel, ugx, operator, role`
	var t models.OperatorTicker
	err := r.pool.QueryRow(ctx, q, patch.Diesel, patch.Ugx, patch.Operator, patch.Role).Scan(
		&t.Diesel, &t.Ugx, &t.Operator, &t.Role,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		// First-run: row missing. Insert from the patch (use sensible
		// defaults for unspecified fields).
		ins := patch
		if ins.Operator == "" {
			ins.Operator = "Operator"
		}
		if ins.Role == "" {
			ins.Role = "Fleet"
		}
		_, err = r.pool.Exec(ctx,
			`INSERT INTO operator_ticker (id, diesel, ugx, operator, role) VALUES ('singleton', $1, $2, $3, $4)`,
			ins.Diesel, ins.Ugx, ins.Operator, ins.Role,
		)
		return ins, err
	}
	return t, err
}

// ─────────────────────────── helpers ────────────────────────────────────

// Pool returns the underlying pool for handlers that need to run their
// own ad-hoc queries without going through a Collection.
func (r *Repository) Pool() *pgxpool.Pool { return r.pool }

// SilenceUnused keeps the time import live when audit-row iteration is
// bypassed by a direct LIMIT clause.
var _ = time.Now