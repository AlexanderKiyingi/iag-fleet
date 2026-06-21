package handlers

import (
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/iag/fleet-tool/backend/internal/store"
)

// PM schedules must expose the same bulk surface as every other CRUD entity.
// Registration also must not panic on the static-vs-param route mix
// (/due, /search, /evaluate alongside /:id). No DB is touched at wiring time,
// so a zero-value repo is fine.
func TestPMSchedules_RegistersBulkRoutes(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	// Registration only reads Collection pointers off the repo; no DB needed.
	(&PMSchedules{Repo: store.NewRepository(nil)}).Register(r.Group("/api"))

	got := make(map[string]bool)
	for _, ri := range r.Routes() {
		got[ri.Method+" "+ri.Path] = true
	}
	want := []string{
		"POST /api/pm-schedules/bulk",
		"PATCH /api/pm-schedules/bulk",
		"DELETE /api/pm-schedules/bulk",
		// regression: the per-row + custom routes must survive the refactor
		"POST /api/pm-schedules",
		"PUT /api/pm-schedules/:id",
		"PATCH /api/pm-schedules/:id",
		"DELETE /api/pm-schedules/:id",
		"GET /api/pm-schedules/due",
		"POST /api/pm-schedules/evaluate",
	}
	for _, w := range want {
		if !got[w] {
			t.Errorf("missing route %q", w)
		}
	}
}
