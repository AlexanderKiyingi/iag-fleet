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
	"strconv"
	"strings"
	"sync"

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
	dbCast   string // "" | "date" | "timestamptz"
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
	updateSet    string // "plate=$1, ..., mech_status=$N" for UPDATE (excludes id)
	updateIDIdx  int    // 1-based parameter index for the WHERE id = $N

	// muTx serializes concurrent Update calls on the same key to keep the
	// in-memory patch + write atomic. Read-modify-write goes through a
	// real DB transaction with SELECT FOR UPDATE; this mutex just makes
	// the surrounding Go code simpler.
	muTx sync.Mutex
}

// NewCollection inspects T's `db` tags to build the SQL column list and
// constructs all the SQL fragments the methods reuse. Panics on programmer
// error (no `db:"id"` field, duplicate columns) — these are bugs at startup,
// not runtime conditions.
func NewCollection[T any, PT IdentifiablePtr[T]](pool *pgxpool.Pool, table string) *Collection[T, PT] {
	c := &Collection[T, PT]{pool: pool, table: table, idIndex: -1, updateIDIdx: 0}

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
		}
		if col == "id" {
			c.idIndex = len(c.columns)
		}
		c.columns = append(c.columns, ci)
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
// fields are wrapped in COALESCE(..., '') so a nullable column with a
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

// ───────────────────────────── CRUD methods ─────────────────────────────

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

// Add inserts the supplied item and returns the row Postgres saw — preserving
// any DEFAULT-resolved values (timestamps, NULL → "" coalesces, etc.).
func (c *Collection[T, PT]) Add(ctx context.Context, item T) (T, error) {
	var zero T
	args, err := c.bindArgs(item, false /* skipID = false: full insert */)
	if err != nil {
		return zero, err
	}
	rows, err := c.pool.Query(ctx,
		"INSERT INTO "+c.table+" ("+c.insertCols+") VALUES ("+c.insertParams+
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
	c.muTx.Lock()
	defer c.muTx.Unlock()

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

	insertSQL := "INSERT INTO " + c.table + " (" + c.insertCols + ") VALUES (" +
		c.insertParams + ") RETURNING " + c.selectExpr

	out := make([]T, 0, len(items))
	for i, item := range items {
		args, err := c.bindArgs(item, false)
		if err != nil {
			return nil, fmt.Errorf("row %d: bind: %w", i, err)
		}
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
)

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
		args = append(args, val)
		conds = append(conds, fmt.Sprintf("%s::text = $%d", col, len(args)))
	}
	where := ""
	if len(conds) > 0 {
		where = " WHERE " + strings.Join(conds, " AND ")
	}

	// Count first (cheap with the existing indexes; same args, no
	// limit/offset). The same predicate gates both queries so total
	// reflects the filter even when the caller paginates.
	var total int
	if err := c.pool.QueryRow(ctx,
		"SELECT COUNT(*) FROM "+c.table+where, args...,
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
		if ci.isString && (ci.dbCast == "date" || ci.dbCast == "timestamptz") {
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
