// Package store is the data-access layer for the fleet domain entities.
// Every Collection[T] is backed by a Postgres table; the column list is
// derived reflectively from `db:"..."` struct tags on the model. Date and
// timestamp columns marked with `dbcast:"date"` or `dbcast:"timestamptz"`
// are cast to text on SELECT so the model's string fields round-trip
// cleanly without forcing a time.Time refactor across the API.
package store

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrNotFound = errors.New("not found")

// Identifiable is implemented by any model that can return / set its ID.
// Models in internal/models satisfy this via value-receiver GetID and
// pointer-receiver SetID.
type Identifiable interface {
	GetID() string
}

type IdentifiablePtr[T any] interface {
	*T
	Identifiable
	SetID(string)
}

// columnInfo is one entry of the reflective schema we extract from the
// model struct. fieldIdx is the struct-field index for reflect access;
// dbCast distinguishes plain text columns from DATE/TIMESTAMPTZ ones
// that need `to_char` formatting on read.
type columnInfo struct {
	name     string // db column name
	fieldIdx int    // reflect.StructField index
	dbCast   string
	// hasDefault marks a column the database can fill in. An INSERT that
	// names the column overrides that default even when the value is the
	// zero value, so such columns are omitted when nothing was supplied.
	hasDefault bool // "" | "date" | "timestamptz"
	// isString is true when the model field's Kind is reflect.String — we
	// wrap those columns in COALESCE(..., '') on SELECT so a NULL row
	// doesn't blow up the scan with `cannot scan NULL into *string`.
	// Pointer fields (*string, *int, *bool, ...) and JSONB Scanner-typed
	// fields handle NULL natively, so they don't get the wrapper.
	isString bool
}

// Collection wraps one database table. It owns the cached SQL fragments
// derived from the model's struct tags so the per-call cost is one
// pgx.Query plus the field-by-field scan.
type Collection[T any, PT IdentifiablePtr[T]] struct {
	pool    *pgxpool.Pool
	table   string
	columns []columnInfo
	idIndex int // index into columns[] of the "id" column

	selectExpr   string // "id, plate, ..., to_char(last_seen, '...') AS last_seen" — used in every SELECT
	insertCols   string // "id, plate, ..." for INSERT (excludes server-generated columns; we have none)
	insertParams string // "$1, $2, ..." matching insertCols
	// anyDefaults is true when at least one column is tagged dbdefault, which
	// is what makes the per-row insert plan necessary. Collections without one
	// keep using insertCols/insertParams unchanged.
	anyDefaults bool
	updateSet    string // "plate=$1, ..., mech_status=$N" for UPDATE (excludes id)
	updateIDIdx  int    // 1-based parameter index for the WHERE id = $N

	// colType maps column name → the Postgres type to cast a filter
	// PARAMETER to in ListFiltered. Casting the parameter keeps the column
	// bare so its index is usable; casting the column (the old `col::text
	// = $1`) made every filtered list a sequential scan.
	colType map[string]string
}

// NewCollection inspects T's `db` tags to build the SQL column list and
// constructs all the SQL fragments the methods reuse. Panics on programmer
// error (no `db:"id"` field, duplicate columns) — these are bugs at startup,
// not runtime conditions.
func NewCollection[T any, PT IdentifiablePtr[T]](pool *pgxpool.Pool, table string) *Collection[T, PT] {
	c := &Collection[T, PT]{
		pool: pool, table: table, idIndex: -1, updateIDIdx: 0,
		colType: map[string]string{},
	}

	var zero T
	rt := reflect.TypeOf(zero)
	if rt.Kind() != reflect.Struct {
		panic(fmt.Sprintf("store: NewCollection requires a struct type, got %s", rt.Kind()))
	}

	seen := make(map[string]struct{})
	for i := 0; i < rt.NumField(); i++ {
		f := rt.Field(i)
		col := f.Tag.Get("db")
		if col == "" || col == "-" {
			continue
		}
		if _, dup := seen[col]; dup {
			panic(fmt.Sprintf("store: duplicate db tag %q on %s", col, rt.Name()))
		}
		seen[col] = struct{}{}
		ci := columnInfo{
			name:     col,
			fieldIdx: i,
			dbCast:   f.Tag.Get("dbcast"),
			isString: f.Type.Kind() == reflect.String,
			// `dbdefault:"true"` means the column carries a database DEFAULT
			// that should win when the caller supplied nothing. See insertPlan.
			hasDefault: f.Tag.Get("dbdefault") == "true",
		}
		if ci.hasDefault {
			c.anyDefaults = true
		}
		if col == "id" {
			c.idIndex = len(c.columns)
		}
		c.columns = append(c.columns, ci)
		c.colType[col] = filterCastFor(f.Type, ci.dbCast)
	}
	if c.idIndex < 0 {
		panic(fmt.Sprintf("store: model %s has no `db:\"id\"` field", rt.Name()))
	}

	c.selectExpr = buildSelectExpr(c.columns)

	// INSERT preserves field order (callers may rely on it for COPY-style
	// bulk inserts in the future).
	insertCols := make([]string, len(c.columns))
	insertParams := make([]string, len(c.columns))
	for i, ci := range c.columns {
		insertCols[i] = ci.name
		insertParams[i] = "$" + strconv.Itoa(i+1)
	}
	c.insertCols = strings.Join(insertCols, ", ")
	c.insertParams = strings.Join(insertParams, ", ")

	// UPDATE: skip the id column; bind a final $N for WHERE id = $N.
	setParts := make([]string, 0, len(c.columns)-1)
	param := 1
	for _, ci := range c.columns {
		if ci.name == "id" {
			continue
		}
		setParts = append(setParts, fmt.Sprintf("%s = $%d", ci.name, param))
		param++
	}
	c.updateSet = strings.Join(setParts, ", ")
	c.updateIDIdx = param // next free param slot is for the WHERE clause

	return c
}

// buildSelectExpr emits the SELECT column list, casting DATE → 'YYYY-MM-DD'
// and TIMESTAMPTZ → 'YYYY-MM-DDTHH24:MI:SS"Z"' so the model's string
// fields receive the JSON-friendly text format directly. Plain string
// fields are wrapped in COALESCE(..., ”) so a nullable column with a
// NULL row doesn't break the scan into the model's plain `string` field.
func buildSelectExpr(cols []columnInfo) string {
	parts := make([]string, len(cols))
	for i, ci := range cols {
		var expr string
		switch ci.dbCast {
		case "date":
			expr = "to_char(" + ci.name + ", 'YYYY-MM-DD')"
		case "timestamptz":
			// Force UTC then format as RFC3339 (millisecond precision is
			// what JS Date.toISOString() produces; we match that).
			expr = "to_char(" + ci.name + " AT TIME ZONE 'UTC', 'YYYY-MM-DD\"T\"HH24:MI:SS.MS\"Z\"')"
		case "uuid":
			// UUID columns map to plain `string` model fields; cast to text so
			// the COALESCE(..., '') wrapper below type-checks (COALESCE(uuid, '')
			// would otherwise fail to resolve a common type and break the query).
			expr = ci.name + "::text"
		default:
			expr = ci.name
		}
		if ci.isString {
			expr = "COALESCE(" + expr + ", '')"
		}
		parts[i] = expr + " AS " + ci.name
	}
	return strings.Join(parts, ", ")
}

// filterCastFor returns the Postgres type a ListFiltered parameter is cast to
// for this model field, or "" for text-ish columns that need no cast at all.
//
// The parameter is cast, never the column: `speed_limit_kmh = $1::double
// precision` can use an index on that column, while the old
// `speed_limit_kmh::text = $1` could not and forced a sequential scan with a
// per-row cast on every filtered list in the service.
func filterCastFor(ft reflect.Type, dbCast string) string {
	switch dbCast {
	case "date":
		return "date"
	case "timestamptz":
		// Date and timestamp columns are read back through to_char, so callers
		// filter them as the text the API hands out, not as a timestamp.
		return ""
	case "uuid":
		return "uuid"
	}
	if ft.Kind() == reflect.Ptr {
		ft = ft.Elem()
	}
	switch ft.Kind() {
	case reflect.Bool:
		return "boolean"
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return "bigint"
	case reflect.Float32, reflect.Float64:
		return "double precision"
	default:
		// Strings, JSONB Scanner types and anything else compare as text.
		return ""
	}
}

// filterValueValid reports whether val can be cast to pgType. A filter the
// database would reject (`?axles=abc`) must return an empty page, not a 500 —
// and not silently widen the result by being dropped either.
func filterValueValid(pgType, val string) bool {
	switch pgType {
	case "boolean":
		_, err := strconv.ParseBool(val)
		return err == nil
	case "bigint":
		_, err := strconv.ParseInt(val, 10, 64)
		return err == nil
	case "double precision":
		_, err := strconv.ParseFloat(val, 64)
		return err == nil
	default:
		// date and uuid are left to Postgres: both accept several spellings
		// and duplicating their parsers here would reject valid input.
		return true
	}
}

// ───────────────────────────── CRUD methods ─────────────────────────────

// List returns every row in the table, unbounded. That is deliberate and it is
// for internal callers that genuinely need the whole set — the admin export
// snapshot, a migration, a reconciliation job.
//
// Do NOT reach for it from an HTTP read path. A request handler wants
// ListFiltered, which paginates; an endpoint that can return an unbounded
// response gets slower every month and is a denial-of-service surface besides.
func (c *Collection[T, PT]) List(ctx context.Context) ([]T, error) {
	rows, err := c.pool.Query(ctx,
		"SELECT "+c.selectExpr+" FROM "+c.table+" ORDER BY id DESC")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []T
	for rows.Next() {
		var item T
		if err := scanInto(rows, c.columns, &item); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

// ListCapped returns at most limit rows, newest id first, and runs no COUNT.
//
// This is what a flat-array read path wants: List's ordering and shape, a hard
// bound, and none of ListFiltered's pagination total — which the caller would
// have nowhere to put anyway. A caller that gets exactly limit rows back should
// assume there are more.
func (c *Collection[T, PT]) ListCapped(ctx context.Context, limit int) ([]T, error) {
	if limit <= 0 {
		limit = listDefaultLimit
	}
	rows, err := c.pool.Query(ctx,
		"SELECT "+c.selectExpr+" FROM "+c.table+" ORDER BY id DESC LIMIT $1", limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]T, 0, limit)
	for rows.Next() {
		var item T
		if err := scanInto(rows, c.columns, &item); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func (c *Collection[T, PT]) Get(ctx context.Context, id string) (T, error) {
	var zero T
	rows, err := c.pool.Query(ctx,
		"SELECT "+c.selectExpr+" FROM "+c.table+" WHERE id = $1", id)
	if err != nil {
		return zero, err
	}
	defer rows.Close()
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return zero, err
		}
		return zero, ErrNotFound
	}
	var item T
	if err := scanInto(rows, c.columns, &item); err != nil {
		return zero, err
	}
	return item, nil
}

// GetMany fetches every row whose id is in ids, keyed by id. Ids with no row
// are simply absent from the map — the caller decides whether that is an error.
//
// One query, not one per id: the serial Get loop this replaced cost a full
// round trip per element, so a 1000-id bulk patch spent 1000 sequential trips
// before the first write.
func (c *Collection[T, PT]) GetMany(ctx context.Context, ids []string) (map[string]T, error) {
	out := make(map[string]T, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := c.pool.Query(ctx,
		"SELECT "+c.selectExpr+" FROM "+c.table+" WHERE id = ANY($1)", ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var item T
		if err := scanInto(rows, c.columns, &item); err != nil {
			return nil, err
		}
		out[PT(&item).GetID()] = item
	}
	return out, rows.Err()
}

// insertPlan builds the column list, placeholders and args for one INSERT,
// omitting columns the database can fill in when the caller supplied nothing.
//
// The generic path names every column, which is correct for a full row and
// wrong for a partial one: naming a column overrides its DEFAULT, so a nil
// slice or an empty temporal string writes NULL and the column's own DEFAULT
// never applies. On a NOT NULL column that is a raw null-violation, and the
// caller sees a Postgres error rather than the row they asked for. It cost
// six adapters a hand-written seed value before this existed.
//
// Only columns tagged `dbdefault:"true"` participate; collections with none
// keep the precomputed fast path and behave exactly as before.
func (c *Collection[T, PT]) insertPlan(item T) (string, string, []any, error) {
	if !c.anyDefaults {
		args, err := c.bindArgs(item, false)
		return c.insertCols, c.insertParams, args, err
	}

	v := reflect.ValueOf(item)
	cols := make([]string, 0, len(c.columns))
	params := make([]string, 0, len(c.columns))
	args := make([]any, 0, len(c.columns))

	for _, ci := range c.columns {
		field := v.Field(ci.fieldIdx)
		// Let the database decide. IsZero covers "" for a timestamp string and
		// a nil slice for TEXT[]; an explicitly empty non-nil slice is a real
		// value and is still sent.
		if ci.hasDefault && field.IsZero() {
			continue
		}

		val := field.Interface()
		// Same translation bindArgs makes: Postgres cannot parse "" as a
		// temporal value, so an empty optional date becomes NULL.
		if ci.isString && (ci.dbCast == "date" || ci.dbCast == "timestamptz" || ci.dbCast == "uuid") {
			if str, ok := val.(string); ok && str == "" {
				cols = append(cols, ci.name)
				params = append(params, "$"+strconv.Itoa(len(args)+1))
				args = append(args, nil)
				continue
			}
		}

		cols = append(cols, ci.name)
		params = append(params, "$"+strconv.Itoa(len(args)+1))
		args = append(args, val)
	}

	return strings.Join(cols, ", "), strings.Join(params, ", "), args, nil
}

// Add inserts the supplied item and returns the row Postgres saw — preserving
// any DEFAULT-resolved values (timestamps, NULL → "" coalesces, etc.).
func (c *Collection[T, PT]) Add(ctx context.Context, item T) (T, error) {
	var zero T
	cols, params, args, err := c.insertPlan(item)
	if err != nil {
		return zero, err
	}
	rows, err := c.pool.Query(ctx,
		"INSERT INTO "+c.table+" ("+cols+") VALUES ("+params+
			") RETURNING "+c.selectExpr,
		args...)
	if err != nil {
		return zero, err
	}
	defer rows.Close()
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return zero, err
		}
		return zero, errors.New("insert returned no row")
	}
	var inserted T
	if err := scanInto(rows, c.columns, &inserted); err != nil {
		return zero, err
	}
	return inserted, nil
}

// Replace overwrites every non-id column on the row with the supplied
// value's fields. Returns ErrNotFound when no row matched.
func (c *Collection[T, PT]) Replace(ctx context.Context, id string, item T) (T, error) {
	var zero T
	PT(&item).SetID(id)
	args, err := c.bindArgs(item, true /* skipID = true: id is already known */)
	if err != nil {
		return zero, err
	}
	args = append(args, id)
	rows, err := c.pool.Query(ctx,
		"UPDATE "+c.table+" SET "+c.updateSet+
			" WHERE id = $"+strconv.Itoa(c.updateIDIdx)+
			" RETURNING "+c.selectExpr,
		args...)
	if err != nil {
		return zero, err
	}
	defer rows.Close()
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return zero, err
		}
		return zero, ErrNotFound
	}
	var updated T
	if err := scanInto(rows, c.columns, &updated); err != nil {
		return zero, err
	}
	return updated, nil
}

// Update applies the patch function inside a transaction. Used by workflow
// handlers that need to read-modify-write nested fields (toolbox toggles,
// stage_history append, etc).
func (c *Collection[T, PT]) Update(ctx context.Context, id string, patch func(*T)) (T, error) {
	var zero T
	// No Go-side lock here on purpose. The SELECT ... FOR UPDATE below already
	// serializes concurrent writers against this row, which is the guarantee
	// read-modify-write needs. A mutex on the Collection serialized every row
	// in the table — not just the contended one — for the full duration of a
	// four-round-trip transaction, capping the service's write throughput at
	// roughly 1/(4×RTT) per table regardless of how many rows were involved.
	tx, err := c.pool.Begin(ctx)
	if err != nil {
		return zero, err
	}
	defer tx.Rollback(ctx)

	rows, err := tx.Query(ctx,
		"SELECT "+c.selectExpr+" FROM "+c.table+" WHERE id = $1 FOR UPDATE", id)
	if err != nil {
		return zero, err
	}
	if !rows.Next() {
		rows.Close()
		if err := rows.Err(); err != nil {
			return zero, err
		}
		return zero, ErrNotFound
	}
	var current T
	if err := scanInto(rows, c.columns, &current); err != nil {
		rows.Close()
		return zero, err
	}
	rows.Close()

	patch(&current)
	args, err := c.bindArgs(current, true)
	if err != nil {
		return zero, err
	}
	args = append(args, id)
	rows, err = tx.Query(ctx,
		"UPDATE "+c.table+" SET "+c.updateSet+
			" WHERE id = $"+strconv.Itoa(c.updateIDIdx)+
			" RETURNING "+c.selectExpr,
		args...)
	if err != nil {
		return zero, err
	}
	if !rows.Next() {
		rows.Close()
		if err := rows.Err(); err != nil {
			return zero, err
		}
		return zero, ErrNotFound
	}
	var updated T
	if err := scanInto(rows, c.columns, &updated); err != nil {
		rows.Close()
		return zero, err
	}
	// Close before COMMIT, not via defer. An open pgx Rows owns the connection
	// for the rest of the function, so committing underneath it fails the whole
	// update with "conn busy" — every call, not just contended ones. BulkAdd and
	// BulkReplace already close explicitly at the end of each iteration; this
	// path was the one that didn't.
	rows.Close()
	if err := rows.Err(); err != nil {
		return zero, err
	}
	if err := tx.Commit(ctx); err != nil {
		return zero, err
	}
	return updated, nil
}

func (c *Collection[T, PT]) Delete(ctx context.Context, id string) error {
	tag, err := c.pool.Exec(ctx, "DELETE FROM "+c.table+" WHERE id = $1", id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// BulkAdd inserts every item in one transaction. On any per-row error
// the whole batch rolls back and the original error is returned (with
// the row index annotated). Server-supplied items keep their IDs; rows
// with empty IDs are the caller's problem — a NOT NULL violation will
// surface as a row error. Used by CSV bulk-import.
//
// Maximum batch size is enforced by the handler layer; this method
// happily accepts any slice you give it.
func (c *Collection[T, PT]) BulkAdd(ctx context.Context, items []T) ([]T, error) {
	if len(items) == 0 {
		return nil, nil
	}
	tx, err := c.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	out := make([]T, 0, len(items))
	for i, item := range items {
		// Built per row: two rows in one batch may leave different columns to
		// the database, so a single shared statement cannot serve both.
		cols, params, args, err := c.insertPlan(item)
		if err != nil {
			return nil, fmt.Errorf("row %d: bind: %w", i, err)
		}
		insertSQL := "INSERT INTO " + c.table + " (" + cols + ") VALUES (" +
			params + ") RETURNING " + c.selectExpr
		rows, err := tx.Query(ctx, insertSQL, args...)
		if err != nil {
			return nil, fmt.Errorf("row %d: %w", i, err)
		}
		if !rows.Next() {
			rows.Close()
			if rerr := rows.Err(); rerr != nil {
				return nil, fmt.Errorf("row %d: %w", i, rerr)
			}
			return nil, fmt.Errorf("row %d: insert returned no row", i)
		}
		var inserted T
		if err := scanInto(rows, c.columns, &inserted); err != nil {
			rows.Close()
			return nil, fmt.Errorf("row %d: scan: %w", i, err)
		}
		rows.Close()
		out = append(out, inserted)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return out, nil
}

// BulkDelete removes every row whose id appears in `ids`. Returns the
// count actually removed (caller can compare against len(ids) to detect
// rows that didn't exist). Single SQL statement, no transaction needed
// since DELETE … WHERE id = ANY(...) is already atomic.
func (c *Collection[T, PT]) BulkDelete(ctx context.Context, ids []string) (int, error) {
	if len(ids) == 0 {
		return 0, nil
	}
	tag, err := c.pool.Exec(ctx,
		"DELETE FROM "+c.table+" WHERE id = ANY($1)", ids)
	if err != nil {
		return 0, err
	}
	return int(tag.RowsAffected()), nil
}

// BulkReplace runs Replace for every item in one transaction. On any
// per-row error the whole batch rolls back and the returned error notes
// the row index. Used by the bulk PATCH handler after it merges the
// shared patch into each existing record.
func (c *Collection[T, PT]) BulkReplace(ctx context.Context, items []T) ([]T, error) {
	if len(items) == 0 {
		return nil, nil
	}
	tx, err := c.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	// Same UPDATE shape as Replace, but bound to the tx connection so
	// every row is covered by a single COMMIT.
	updateSQL := "UPDATE " + c.table + " SET " + c.updateSet +
		" WHERE id = $" + strconv.Itoa(c.updateIDIdx) +
		" RETURNING " + c.selectExpr

	out := make([]T, 0, len(items))
	for i, item := range items {
		args, err := c.bindArgs(item, true)
		if err != nil {
			return nil, fmt.Errorf("row %d: bind: %w", i, err)
		}
		args = append(args, PT(&item).GetID())
		rows, err := tx.Query(ctx, updateSQL, args...)
		if err != nil {
			return nil, fmt.Errorf("row %d: %w", i, err)
		}
		if !rows.Next() {
			rows.Close()
			return nil, fmt.Errorf("row %d (id=%s): %w", i, PT(&item).GetID(), ErrNotFound)
		}
		var updated T
		if err := scanInto(rows, c.columns, &updated); err != nil {
			rows.Close()
			return nil, fmt.Errorf("row %d: scan: %w", i, err)
		}
		rows.Close()
		out = append(out, updated)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return out, nil
}

// ListFilter is the input shape for ListFiltered. Filters is keyed by db
// column name (validated against the model's known columns) and matches
// exact values. Limit is clamped to [1, listMaxLimit] by the method;
// pass 0 for the default. OrderBy must match an existing column or it's
// silently ignored. Sort direction defaults to DESC to match List().
type ListFilter struct {
	Filters  map[string]string
	Limit    int
	Offset   int
	OrderBy  string
	OrderAsc bool
}

const (
	listDefaultLimit = 100
	listMaxLimit     = 1000

	// CountCap bounds the pagination total. ListFiltered stops counting here,
	// so a total equal to CountCap means "at least this many" rather than
	// "exactly this many" — TotalIsCapped reports which.
	CountCap = 10000
)

// TotalIsCapped reports whether a total returned by ListFiltered hit CountCap
// and is therefore a floor rather than an exact count. Handlers surface this
// so a client can render "10,000+" instead of a wrong exact figure.
func TotalIsCapped(total int) bool { return total >= CountCap }

// ListFiltered runs a paginated, filtered query against the collection.
// Returns (page, total) where total is the count of rows matching the
// filter regardless of pagination — same shape as the audit search
// endpoint. Unknown filter keys are dropped silently rather than 400'd
// so an honest typo in a frontend doesn't 500 the page; pass through
// only validated columns prevents SQL injection.
func (c *Collection[T, PT]) ListFiltered(ctx context.Context, f ListFilter) ([]T, int, error) {
	if f.Limit <= 0 {
		f.Limit = listDefaultLimit
	}
	if f.Limit > listMaxLimit {
		f.Limit = listMaxLimit
	}
	if f.Offset < 0 {
		f.Offset = 0
	}

	known := make(map[string]struct{}, len(c.columns))
	for _, ci := range c.columns {
		known[ci.name] = struct{}{}
	}

	conds := make([]string, 0, len(f.Filters))
	args := make([]any, 0, len(f.Filters)+2)
	for col, val := range f.Filters {
		if _, ok := known[col]; !ok {
			continue // silently drop unknown filter keys
		}
		cast := c.colType[col]
		if !filterValueValid(cast, val) {
			// The value cannot be the column's type, so nothing can match it.
			// Answering empty beats a 500 from a failed cast, and beats
			// dropping the filter — which would return MORE rows than asked for.
			return []T{}, 0, nil
		}
		args = append(args, val)
		if cast == "" {
			conds = append(conds, fmt.Sprintf("%s = $%d", col, len(args)))
		} else {
			conds = append(conds, fmt.Sprintf("%s = $%d::%s", col, len(args), cast))
		}
	}
	where := ""
	if len(conds) > 0 {
		where = " WHERE " + strings.Join(conds, " AND ")
	}

	// COUNT(*) always scans in Postgres — index-only at best, and a full
	// sequential scan on an unfiltered append-only table like api_audit.
	// Stop counting at CountCap: the scan is bounded, and a pager cannot
	// use "2.4 million" for anything a "10000+" does not also serve.
	// total == CountCap means "at least this many" — see TotalIsCapped.
	var total int
	if err := c.pool.QueryRow(ctx,
		"SELECT COUNT(*) FROM (SELECT 1 FROM "+c.table+where+
			" LIMIT "+strconv.Itoa(CountCap)+") capped", args...,
	).Scan(&total); err != nil {
		return nil, 0, err
	}

	orderCol := "id"
	if _, ok := known[f.OrderBy]; ok {
		orderCol = f.OrderBy
	}
	dir := "DESC"
	if f.OrderAsc {
		dir = "ASC"
	}

	pageArgs := append(append([]any{}, args...), f.Limit, f.Offset)
	pageQ := fmt.Sprintf(
		"SELECT %s FROM %s%s ORDER BY %s %s LIMIT $%d OFFSET $%d",
		c.selectExpr, c.table, where, orderCol, dir,
		len(args)+1, len(args)+2,
	)
	rows, err := c.pool.Query(ctx, pageQ, pageArgs...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	out := make([]T, 0, f.Limit)
	for rows.Next() {
		var item T
		if err := scanInto(rows, c.columns, &item); err != nil {
			return nil, 0, err
		}
		out = append(out, item)
	}
	return out, total, rows.Err()
}

// SetAll replaces the entire table contents. Used by admin import / reset.
func (c *Collection[T, PT]) SetAll(ctx context.Context, items []T) error {
	tx, err := c.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, "DELETE FROM "+c.table); err != nil {
		return err
	}
	insertSQL := "INSERT INTO " + c.table + " (" + c.insertCols + ") VALUES (" + c.insertParams + ")"
	for _, item := range items {
		args, err := c.bindArgs(item, false)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, insertSQL, args...); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

// ──────────────────────────── reflection plumbing ───────────────────────

// bindArgs extracts each column's value from the struct via reflection.
// skipID = true omits the id column (used for UPDATE).
func (c *Collection[T, PT]) bindArgs(item T, skipID bool) ([]any, error) {
	v := reflect.ValueOf(item)
	args := make([]any, 0, len(c.columns))
	for _, ci := range c.columns {
		if skipID && ci.name == "id" {
			continue
		}
		val := v.Field(ci.fieldIdx).Interface()
		// The model carries DATE/TIMESTAMPTZ columns as plain JSON-friendly
		// strings, but Postgres can't parse "" as a temporal value. Translate
		// the empty zero value to NULL on write so optional date/timestamp
		// fields (last_received, last_consumed, warehouse_synced_at, …) insert
		// cleanly instead of erroring with "invalid input syntax".
		if ci.isString && (ci.dbCast == "date" || ci.dbCast == "timestamptz" || ci.dbCast == "uuid") {
			if s, ok := val.(string); ok && s == "" {
				args = append(args, nil)
				continue
			}
		}
		args = append(args, val)
	}
	return args, nil
}

// scanInto reads one row into *item by walking the column list in order.
// We use Scan (positional) over RowToStructByName so JSONB Scanner-typed
// fields and dbcast string fields all get the right destination kind.
func scanInto[T any](rows pgx.Rows, cols []columnInfo, item *T) error {
	v := reflect.ValueOf(item).Elem()
	dests := make([]any, len(cols))
	for i, ci := range cols {
		dests[i] = v.Field(ci.fieldIdx).Addr().Interface()
	}
	return rows.Scan(dests...)
}

// ColumnSpec is the table and column list one Collection reads and writes.
//
// Every statement a Collection issues names each of these columns, so a column
// the database does not have breaks that whole table — not the one field.
type ColumnSpec struct {
	Table   string
	Columns []string
}

// Spec reports what this collection expects of its table, so a deploy can check
// the schema before it serves rather than discovering the gap per request.
func (c *Collection[T, PT]) Spec() ColumnSpec {
	cols := make([]string, len(c.columns))
	for i, ci := range c.columns {
		cols[i] = ci.name
	}
	return ColumnSpec{Table: c.table, Columns: cols}
}

// VerifySchema reports columns the models select that the database lacks,
// as "table.column", sorted.
//
// The failure this exists to catch: ship a model field whose migration has not
// been applied and every read of that table fails with `column "x" does not
// exist`, as a 500 per request with nothing at boot to say why. That has now
// happened twice — 7873109 ("vehicles was 502 in production") and again when a
// JMP.Notes field was deployed ahead of migration 0045.
//
// One query for every table rather than one per table: this runs on a deploy
// path, and the answer is the same either way.
func VerifySchema(ctx context.Context, pool *pgxpool.Pool, specs []ColumnSpec) ([]string, error) {
	tables := make([]string, 0, len(specs))
	for _, sp := range specs {
		tables = append(tables, sp.Table)
	}
	// Resolved through to_regclass, not information_schema.table_schema.
	//
	// Fleet shares one database with the rest of the platform and runs with
	// search_path = "iag_fleet, public", so an unqualified table name may
	// resolve to either schema. Restricting the lookup to current_schema()
	// would report a table that lives in public as missing — a false alarm on a
	// deploy gate, which is the one thing it must not produce.
	//
	// to_regclass applies the same search_path the queries themselves use, and
	// returns NULL rather than erroring for a name that resolves nowhere, so a
	// genuinely absent table is reported instead of failing the whole check.
	rows, err := pool.Query(ctx,
		`SELECT t.name, a.attname
		   FROM unnest($1::text[]) AS t(name)
		   LEFT JOIN pg_attribute a
		     ON a.attrelid = to_regclass(t.name)
		    AND a.attnum > 0
		    AND NOT a.attisdropped`, tables)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	have := map[string]map[string]bool{}
	for rows.Next() {
		var t string
		var col *string // NULL when the table resolves nowhere
		if err := rows.Scan(&t, &col); err != nil {
			return nil, err
		}
		if have[t] == nil {
			have[t] = map[string]bool{}
		}
		if col != nil {
			have[t][*col] = true
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	var missing []string
	for _, sp := range specs {
		cols := have[sp.Table]
		if len(cols) == 0 {
			// The table itself is absent — report it once rather than listing
			// every column as missing.
			missing = append(missing, sp.Table+" (table missing)")
			continue
		}
		for _, col := range sp.Columns {
			if !cols[col] {
				missing = append(missing, sp.Table+"."+col)
			}
		}
	}
	sort.Strings(missing)
	return missing, nil
}
