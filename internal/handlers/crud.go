// Package handlers contains the HTTP layer. Generic CRUD lives here;
// resource-specific endpoints (ticker, audit, admin) live alongside.
package handlers

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/iag/fleet-tool/backend/internal/auth"
	"github.com/iag/fleet-tool/backend/internal/events"
	"github.com/iag/fleet-tool/backend/internal/store"
)

var errDriverNotFound = errors.New("driver not found")

// Resource binds a Collection to a route group with full CRUD.
//   GET    /<base>          → list
//   GET    /<base>/:id      → fetch
//   POST   /<base>          → create (server-generates ID if missing)
//   PUT    /<base>/:id      → full replace
//   PATCH  /<base>/:id      → partial update via JSON-merge into stored value
//   DELETE /<base>/:id      → remove
type Resource[T any, PT store.IdentifiablePtr[T]] struct {
	Repo       *store.Repository
	Collection *store.Collection[T, PT]
	// Entity is the noun used in audit logs and to derive permission codenames.
	Entity string
	// IDPrefix is used when generating IDs for items posted without one.
	IDPrefix string
	Events   *events.Bus
	// Optional hooks for entity-specific validation and side effects.
	BeforeCreate func(c *gin.Context, item *T) error
	BeforeUpdate func(c *gin.Context, item *T) error
	BeforeDelete func(ctx context.Context, id string) error
	AfterCreate  func(ctx context.Context, item T)
	AfterUpdate  func(ctx context.Context, before, after T)
	AfterDelete  func(ctx context.Context, id string)
}

func (r *Resource[T, PT]) Register(rg *gin.RouterGroup, base string) {
	g := rg.Group(base)
	view := auth.RequirePerm("view_" + r.Entity)
	add := auth.RequirePerm("add_" + r.Entity)
	change := auth.RequirePerm("change_" + r.Entity)
	del := auth.RequirePerm("delete_" + r.Entity)
	g.GET("", view, r.list)
	// /search is the Django-admin-style filter+pagination endpoint —
	// returns { items, total, limit, offset } and accepts arbitrary
	// `?<column>=value` query params plus limit/offset/orderBy.
	// /list keeps backwards compat with anything that wants the flat
	// array (the existing Next.js store, CSV exporters, etc.).
	g.GET("/search", view, r.search)
	g.GET("/:id", view, r.get)
	g.POST("", add, r.create)
	g.POST("/bulk", add, r.bulkCreate)
	// PUT does a strict full-replace — any field absent from the body is
	// reset to its zero value. PATCH below preserves the existing fields
	// and merges the body in. The Next.js frontend only ever uses PATCH
	// because its mutator pattern is `{ ...item, ...patch }`. PUT is kept
	// for non-Next clients (admin tooling, scripts, future mobile apps)
	// that legitimately want full-replace semantics — collapsing the two
	// would force those callers to read-then-PATCH for a behaviour the
	// HTTP standard already names.
	g.PUT("/:id", change, r.replace)
	g.PATCH("/:id", change, r.patch)
	g.PATCH("/bulk", change, r.bulkPatch)
	g.DELETE("/:id", del, r.remove)
	g.DELETE("/bulk", del, r.bulkDelete)
}

// maxBulkItems caps the array size accepted by /bulk. The whole batch is
// inserted under one transaction; very large batches hold locks for too
// long and exceed pgx's protocol-level memory ceilings. 1000 is well
// above any realistic CSV import while staying comfortable on the
// server side.
const maxBulkItems = 1000

func (r *Resource[T, PT]) list(c *gin.Context) {
	items, err := r.Collection.List(c.Request.Context())
	if err != nil {
		respondError(c, err)
		return
	}
	if items == nil {
		items = []T{}
	}
	c.JSON(http.StatusOK, items)
}

func (r *Resource[T, PT]) get(c *gin.Context) {
	item, err := r.Collection.Get(c.Request.Context(), c.Param("id"))
	if err != nil {
		respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, item)
}

func (r *Resource[T, PT]) create(c *gin.Context) {
	var item T
	if err := bindJSONCoerced(c, &item); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if id := PT(&item).GetID(); id == "" {
		PT(&item).SetID(generateID(r.IDPrefix))
	}
	if r.BeforeCreate != nil {
		if err := r.BeforeCreate(c, &item); err != nil {
			respondMutationError(c, err)
			return
		}
	}
	created, err := r.Collection.Add(c.Request.Context(), item)
	if err != nil {
		respondError(c, err)
		return
	}
	if r.AfterCreate != nil {
		r.AfterCreate(c.Request.Context(), created)
	}
	r.Repo.LogBest(c.Request.Context(), "create", r.Entity, PT(&created).GetID(), "", currentUser(c, r.Repo))
	c.JSON(http.StatusCreated, created)
}

// bulkCreate accepts an array of items and inserts them all in one tx.
// Items missing an `id` get one server-generated using the same prefix
// as the per-row POST. The whole batch rolls back on the first failure;
// the response includes the index where the failure happened so the
// caller can fix that row in their CSV without rerunning everything.
func (r *Resource[T, PT]) bulkCreate(c *gin.Context) {
	var items []T
	if err := bindJSONCoercedSlice(c, &items); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if len(items) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "empty batch"})
		return
	}
	if len(items) > maxBulkItems {
		c.JSON(http.StatusRequestEntityTooLarge, gin.H{
			"error":  "batch too large",
			"limit":  maxBulkItems,
			"actual": len(items),
		})
		return
	}
	for i := range items {
		if id := PT(&items[i]).GetID(); id == "" {
			PT(&items[i]).SetID(generateID(r.IDPrefix))
		}
	}
	ctx := c.Request.Context()
	for i := range items {
		if r.BeforeCreate != nil {
			if err := r.BeforeCreate(c, &items[i]); err != nil {
				respondMutationError(c, err)
				return
			}
		}
	}
	created, err := r.Collection.BulkAdd(ctx, items)
	if err != nil {
		respondError(c, err)
		return
	}
	user := currentUser(c, r.Repo)
	for i := range created {
		if r.AfterCreate != nil {
			r.AfterCreate(ctx, created[i])
		}
		r.Repo.LogBest(ctx, "create", r.Entity, PT(&created[i]).GetID(), "bulk", user)
	}
	c.JSON(http.StatusCreated, gin.H{
		"added": len(created),
		"items": created,
	})
}

// search is the Django-admin-style filterable + paginated list endpoint.
// Sibling of `list` to keep the unfiltered flat-array contract intact for
// existing callers (Next.js store, CSV exporters).
//
// Query params:
//
//	limit=N         page size, capped at the Collection's listMaxLimit
//	offset=N        starting row (0-based)
//	orderBy=<col>   sort column; unknown columns are ignored
//	orderDesc=true  default; set to false for ascending
//	<column>=value  exact-match filter on any model column. Unknown
//	                column names are dropped silently.
//
// Response:
//
//	{ "items": [...T], "total": <int>, "limit": <int>, "offset": <int> }
func (r *Resource[T, PT]) search(c *gin.Context) {
	f := store.ListFilter{
		Filters: make(map[string]string),
	}

	// All non-reserved query params become filter candidates. The
	// Collection silently drops unknown columns, which keeps the surface
	// forgiving without exposing SQL injection (only known db tags map
	// through to the WHERE clause).
	reserved := map[string]struct{}{
		"limit": {}, "offset": {}, "orderBy": {}, "orderDesc": {},
	}
	for key, vals := range c.Request.URL.Query() {
		if _, skip := reserved[key]; skip {
			continue
		}
		if len(vals) > 0 && vals[0] != "" {
			f.Filters[key] = vals[0]
		}
	}

	if v := c.Query("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			f.Limit = n
		}
	}
	if v := c.Query("offset"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			f.Offset = n
		}
	}
	f.OrderBy = c.Query("orderBy")
	// Default DESC matches the existing List() ordering. Pass
	// `orderDesc=false` to flip to ASC.
	f.OrderAsc = c.Query("orderDesc") == "false"

	items, total, err := r.Collection.ListFiltered(c.Request.Context(), f)
	if err != nil {
		respondError(c, err)
		return
	}
	if items == nil {
		items = []T{}
	}
	effLimit := f.Limit
	if effLimit <= 0 {
		effLimit = 100 // mirrors store.listDefaultLimit
	}
	c.JSON(http.StatusOK, gin.H{
		"items":  items,
		"total":  total,
		"limit":  effLimit,
		"offset": f.Offset,
	})
}

// bulkPatchBody applies one shared patch to many ids. Mirrors Django
// admin's "make selected active" pattern — a queryset and an action.
// For per-row variable patches, callers should issue PATCH /:id N times
// (or we add PATCH /bulk-rows later if the use case shows up).
type bulkPatchBody struct {
	IDs   []string        `json:"ids" binding:"required"`
	Patch json.RawMessage `json:"patch" binding:"required"`
}

func (r *Resource[T, PT]) bulkPatch(c *gin.Context) {
	var body bulkPatchBody
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if len(body.IDs) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "ids: empty"})
		return
	}
	if len(body.IDs) > maxBulkItems {
		c.JSON(http.StatusRequestEntityTooLarge, gin.H{
			"error":  "batch too large",
			"limit":  maxBulkItems,
			"actual": len(body.IDs),
		})
		return
	}
	if len(strings.TrimSpace(string(body.Patch))) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "patch: empty"})
		return
	}

	ctx := c.Request.Context()
	// Read each existing row, apply the merge, and collect the merged
	// items. We do the reads serially before the BulkReplace so a
	// missing-id error rolls back nothing — the tx hasn't started yet.
	merged := make([]T, 0, len(body.IDs))
	before := make([]T, 0, len(body.IDs))
	for _, id := range body.IDs {
		existing, err := r.Collection.Get(ctx, id)
		if err != nil {
			if errors.Is(err, store.ErrNotFound) {
				c.JSON(http.StatusNotFound, gin.H{"error": "not found", "id": id})
				return
			}
			respondError(c, err)
			return
		}
		m, err := mergeJSON(existing, body.Patch)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error(), "id": id})
			return
		}
		if r.BeforeUpdate != nil {
			if err := r.BeforeUpdate(c, &m); err != nil {
				respondMutationError(c, err)
				return
			}
		}
		before = append(before, existing)
		merged = append(merged, m)
	}

	updated, err := r.Collection.BulkReplace(ctx, merged)
	if err != nil {
		respondError(c, err)
		return
	}

	user := currentUser(c, r.Repo)
	for i := range updated {
		if r.AfterUpdate != nil {
			r.AfterUpdate(ctx, before[i], updated[i])
		}
		r.Repo.LogBest(ctx, "update", r.Entity, PT(&updated[i]).GetID(), "bulk", user)
	}
	c.JSON(http.StatusOK, gin.H{
		"updated": len(updated),
		"items":   updated,
	})
}

// bulkDeleteBody is `{ "ids": ["...", "..."] }`. Returns the count
// actually removed; rows that didn't exist are silently absent from the
// count, which matches what `DELETE /:id` does on a stale id (404) — the
// bulk endpoint just doesn't 404 if some are missing, since "delete
// what's there" is the common batch intent. Resources with a BeforeDelete
// guard may also return "blocked": [{id, error}] for rows the guard
// refused (see bulkDelete).
type bulkDeleteBody struct {
	IDs []string `json:"ids" binding:"required"`
}

func (r *Resource[T, PT]) bulkDelete(c *gin.Context) {
	var body bulkDeleteBody
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if len(body.IDs) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "ids: empty"})
		return
	}
	if len(body.IDs) > maxBulkItems {
		c.JSON(http.StatusRequestEntityTooLarge, gin.H{
			"error":  "batch too large",
			"limit":  maxBulkItems,
			"actual": len(body.IDs),
		})
		return
	}

	ctx := c.Request.Context()
	user := currentUser(c, r.Repo)

	// Fast path: with no per-row delete guard or side-effect, the whole
	// batch is one atomic DELETE … WHERE id = ANY. This covers every
	// entity that doesn't register BeforeDelete/AfterDelete.
	if r.BeforeDelete == nil && r.AfterDelete == nil {
		deleted, err := r.Collection.BulkDelete(ctx, body.IDs)
		if err != nil {
			respondError(c, err)
			return
		}
		for _, id := range body.IDs {
			r.Repo.LogBest(ctx, "delete", r.Entity, id, "bulk", user)
		}
		c.JSON(http.StatusOK, gin.H{"deleted": deleted})
		return
	}

	// Hook path: BeforeDelete/AfterDelete must run per id exactly as the
	// single-row remove() does — otherwise bulk delete would bypass the
	// referential guards (e.g. a vehicle/driver on a live journey) and skip
	// the domain-event emission. A row blocked by its guard is collected
	// into "blocked" and the rest still delete, matching the "delete what's
	// deletable" intent the missing-id behavior already implies. The batch
	// is capped at maxBulkItems and only two entities take this path, so the
	// per-row statements are bounded.
	deleted := 0
	blocked := make([]gin.H, 0)
	for _, id := range body.IDs {
		if r.BeforeDelete != nil {
			if err := r.BeforeDelete(ctx, id); err != nil {
				blocked = append(blocked, gin.H{"id": id, "error": err.Error()})
				continue
			}
		}
		if err := r.Collection.Delete(ctx, id); err != nil {
			if errors.Is(err, store.ErrNotFound) {
				// Stale id — silently absent from the count, same as a
				// single DELETE /:id 404 against a missing row.
				continue
			}
			respondError(c, err)
			return
		}
		if r.AfterDelete != nil {
			r.AfterDelete(ctx, id)
		}
		r.Repo.LogBest(ctx, "delete", r.Entity, id, "bulk", user)
		deleted++
	}

	resp := gin.H{"deleted": deleted}
	if len(blocked) > 0 {
		resp["blocked"] = blocked
	}
	c.JSON(http.StatusOK, resp)
}

func (r *Resource[T, PT]) replace(c *gin.Context) {
	var item T
	if err := bindJSONCoerced(c, &item); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	id := c.Param("id")
	if r.BeforeUpdate != nil {
		if err := r.BeforeUpdate(c, &item); err != nil {
			respondMutationError(c, err)
			return
		}
	}
	existing, err := r.Collection.Get(c.Request.Context(), id)
	if err != nil {
		respondError(c, err)
		return
	}
	updated, err := r.Collection.Replace(c.Request.Context(), id, item)
	if err != nil {
		respondError(c, err)
		return
	}
	if r.AfterUpdate != nil {
		r.AfterUpdate(c.Request.Context(), existing, updated)
	}
	r.Repo.LogBest(c.Request.Context(), "update", r.Entity, id, "", currentUser(c, r.Repo))
	c.JSON(http.StatusOK, updated)
}

// patch reads the existing item, marshals it, applies the request body as a
// JSON merge, and unmarshals the result back. This matches the frontend's
// `{ ...item, ...patch }` semantics regardless of which fields are present.
func (r *Resource[T, PT]) patch(c *gin.Context) {
	id := c.Param("id")
	ctx := c.Request.Context()
	existing, err := r.Collection.Get(ctx, id)
	if err != nil {
		respondError(c, err)
		return
	}

	patchBytes, err := c.GetRawData()
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if len(strings.TrimSpace(string(patchBytes))) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "empty body"})
		return
	}

	merged, err := mergeJSON(existing, patchBytes)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if r.BeforeUpdate != nil {
		if err := r.BeforeUpdate(c, &merged); err != nil {
			respondMutationError(c, err)
			return
		}
	}

	updated, err := r.Collection.Replace(ctx, id, merged)
	if err != nil {
		respondError(c, err)
		return
	}
	if r.AfterUpdate != nil {
		r.AfterUpdate(ctx, existing, updated)
	}
	r.Repo.LogBest(ctx, "update", r.Entity, id, "", currentUser(c, r.Repo))
	c.JSON(http.StatusOK, updated)
}

func (r *Resource[T, PT]) remove(c *gin.Context) {
	id := c.Param("id")
	if r.BeforeDelete != nil {
		if err := r.BeforeDelete(c.Request.Context(), id); err != nil {
			respondMutationError(c, err)
			return
		}
	}
	if err := r.Collection.Delete(c.Request.Context(), id); err != nil {
		respondError(c, err)
		return
	}
	if r.AfterDelete != nil {
		r.AfterDelete(c.Request.Context(), id)
	}
	r.Repo.LogBest(c.Request.Context(), "delete", r.Entity, id, "", currentUser(c, r.Repo))
	c.Status(http.StatusNoContent)
}

func mergeJSON[T any](existing T, patch []byte) (T, error) {
	base, err := json.Marshal(existing)
	if err != nil {
		return existing, err
	}
	var asMap map[string]any
	if err := json.Unmarshal(base, &asMap); err != nil {
		return existing, err
	}
	var patchMap map[string]any
	if err := json.Unmarshal(patch, &patchMap); err != nil {
		return existing, err
	}
	for k, v := range patchMap {
		asMap[k] = v
	}
	// The Next.js store submits form fields verbatim, so numeric/bool columns
	// can arrive as JSON strings ({"lat":"0.31"}). Coerce them to the target
	// struct field's scalar type before the round-trip; otherwise a plain
	// number column rejects the string with a 400 ("cannot unmarshal string
	// into Go struct field …"). Genuine string fields and unparseable input
	// are left untouched, so real bad input still surfaces its error below.
	coerceScalarStrings(reflect.TypeOf(existing), asMap)
	out, err := json.Marshal(asMap)
	if err != nil {
		return existing, err
	}
	var merged T
	if err := json.Unmarshal(out, &merged); err != nil {
		return existing, err
	}
	return merged, nil
}

// coerceScalarStrings rewrites string values in a merged patch map to the
// scalar Go type of the matching struct field, keyed by json tag. It exists
// because the frontend submits HTML form values as strings, so numeric and
// boolean columns (lat, lng, year, fuel, …) can arrive quoted and would
// otherwise fail the map→struct unmarshal. Only string values whose target
// field is a numeric/bool kind are touched (pointer fields are unwrapped):
// a blank "" is nulled out (so an unset lat/lng/year doesn't 400), and a
// non-empty value is parsed. Genuine string fields and unparseable non-empty
// values are left as-is so legitimately bad input still errors in the caller.
// bindJSONCoerced decodes a single JSON object request body into dst, first
// coercing string-encoded numeric/bool fields to dst's scalar field types —
// the same tolerance mergeJSON applies on PATCH. create (POST) and replace
// (PUT) bind whole models directly, and the Next.js store submits form values
// as strings, so without this a body like {"lat":"0.31"} would 400 here even
// though the equivalent PATCH succeeds. Domain validation still runs in the
// BeforeCreate/BeforeUpdate hooks; the models carry no gin `binding` tags, so
// nothing is lost by reading the raw body instead of ShouldBindJSON.
func bindJSONCoerced[T any](c *gin.Context, dst *T) error {
	raw, err := c.GetRawData()
	if err != nil {
		return err
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return err
	}
	coerceScalarStrings(reflect.TypeOf(*dst), m)
	out, err := json.Marshal(m)
	if err != nil {
		return err
	}
	return json.Unmarshal(out, dst)
}

// bindJSONCoercedSlice is the array-body form of bindJSONCoerced, used by the
// bulk-create / CSV-import path so each row gets the same string coercion.
func bindJSONCoercedSlice[T any](c *gin.Context, dst *[]T) error {
	raw, err := c.GetRawData()
	if err != nil {
		return err
	}
	var rows []map[string]any
	if err := json.Unmarshal(raw, &rows); err != nil {
		return err
	}
	var zero T
	rt := reflect.TypeOf(zero)
	for _, m := range rows {
		coerceScalarStrings(rt, m)
	}
	out, err := json.Marshal(rows)
	if err != nil {
		return err
	}
	return json.Unmarshal(out, dst)
}

func coerceScalarStrings(t reflect.Type, m map[string]any) {
	for t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct {
		return
	}
	for i := 0; i < t.NumField(); i++ {
		name, _, _ := strings.Cut(t.Field(i).Tag.Get("json"), ",")
		if name == "" || name == "-" {
			continue
		}
		s, ok := m[name].(string)
		if !ok {
			continue
		}
		ft := t.Field(i).Type
		for ft.Kind() == reflect.Ptr {
			ft = ft.Elem()
		}
		isScalar := false
		switch ft.Kind() {
		case reflect.Float32, reflect.Float64,
			reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
			reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64,
			reflect.Bool:
			isScalar = true
		}
		if !isScalar {
			continue
		}
		// A blank form value for a numeric/bool column arrives as "" and must
		// be nulled out: JSON null unmarshals cleanly into both pointer fields
		// (→ nil) and value fields (→ zero), whereas "" fails the map→struct
		// unmarshal with "cannot unmarshal string into … float64". This is the
		// common case of editing a record whose lat/lng/etc. is unset.
		if s == "" {
			m[name] = nil
			continue
		}
		switch ft.Kind() {
		case reflect.Float32, reflect.Float64:
			if v, err := strconv.ParseFloat(s, 64); err == nil {
				m[name] = v
			}
		case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
			if v, err := strconv.ParseInt(s, 10, 64); err == nil {
				m[name] = v
			}
		case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
			if v, err := strconv.ParseUint(s, 10, 64); err == nil {
				m[name] = v
			}
		case reflect.Bool:
			if v, err := strconv.ParseBool(s); err == nil {
				m[name] = v
			}
		}
	}
}

func respondError(c *gin.Context, err error) {
	if errors.Is(err, store.ErrNotFound) {
		c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
		return
	}
	if isUniqueViolation(err) {
		c.JSON(http.StatusConflict, gin.H{"error": "duplicate value"})
		return
	}
	c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
}

func respondMutationError(c *gin.Context, err error) {
	if errors.Is(err, errDriverDoubleBooked) || errors.Is(err, errVehicleDoubleBooked) ||
		errors.Is(err, errDriverAlreadyOnVehicle) || errors.Is(err, errVehicleNotDispatchable) ||
		errors.Is(err, errToolboxIncomplete) || errors.Is(err, errVehicleInUse) ||
		errors.Is(err, errDriverInUse) || errors.Is(err, errTyrePositionTaken) {
		c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
		return
	}
	if errors.Is(err, errDriverEligibility) || errors.Is(err, errInvalidFuelRecord) ||
		errors.Is(err, errInvalidFuelRequest) ||
		errors.Is(err, errVehicleNotFound) ||
		errors.Is(err, errDriverNotFound) || errors.Is(err, errDriverPermitInvalid) ||
		errors.Is(err, store.ErrNotFound) ||
		errors.Is(err, errInvalidPMSchedule) || errors.Is(err, errInvalidMaintenanceStatus) ||
		errors.Is(err, errInvalidComplianceDoc) || errors.Is(err, errInvalidComplianceExpiry) {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	respondError(c, err)
}

func generateID(prefix string) string {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	tail := strings.ToUpper(hex.EncodeToString(b))
	if prefix == "" {
		return tail
	}
	return prefix + "-" + tail
}

// generateYearID builds a year-prefixed id like "JMP-2026-A3F7B1". Matches
// the frontend's uidYear() shape so JMPs / cargo / requests created via
// workflow endpoints sort and display alongside client-generated ones.
func generateYearID(prefix string) string {
	b := make([]byte, 3)
	_, _ = rand.Read(b)
	tail := strings.ToUpper(hex.EncodeToString(b))
	year := time.Now().UTC().Year()
	return fmt.Sprintf("%s-%d-%s", prefix, year, tail)
}

// currentUser returns the username for audit purposes. Prefers the
// authenticated session user, then the X-Operator header (used by tooling /
// CSV imports running with a service account), then the stored ticker name.
// On DB error the ticker fallback returns empty; the audit row records
// "anonymous" rather than failing the request.
func currentUser(c *gin.Context, repo *store.Repository) string {
	if name := auth.OperatorName(c); name != "" {
		return name
	}
	if u := c.GetHeader("X-Operator"); u != "" {
		return u
	}
	t, err := repo.Ticker(c.Request.Context())
	if err != nil || t.Operator == "" {
		return "anonymous"
	}
	return t.Operator
}
