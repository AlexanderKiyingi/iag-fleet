package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/iag/fleet-tool/backend/internal/auth"
	jmpplan "github.com/iag/fleet-tool/backend/internal/jmp"
	"github.com/iag/fleet-tool/backend/internal/models"
	"github.com/iag/fleet-tool/backend/internal/store"
)

// JMPs is CRUD for journey plans with server-side budget and distance enrichment.
type JMPs struct {
	inner          Resource[models.JMP, *models.JMP]
	RoutingOSRMURL string
}

func NewJMPs(repo *store.Repository, osrmBaseURL string) *JMPs {
	return &JMPs{
		inner: Resource[models.JMP, *models.JMP]{
			Repo:       repo,
			Collection: repo.JMPs,
			Entity:     "jmp",
			IDPrefix:   "JMP",
		},
		RoutingOSRMURL: osrmBaseURL,
	}
}

func (j *JMPs) Register(rg *gin.RouterGroup, base string) {
	g := rg.Group(base)
	view := auth.RequirePerm("view_" + j.inner.Entity)
	add := auth.RequirePerm("add_" + j.inner.Entity)
	change := auth.RequirePerm("change_" + j.inner.Entity)
	del := auth.RequirePerm("delete_" + j.inner.Entity)

	g.GET("", view, j.inner.list)
	g.GET("/search", view, j.inner.search)
	g.GET("/:id", view, j.inner.get)
	g.POST("", add, j.create)
	g.POST("/bulk", add, j.inner.bulkCreate)
	g.PUT("/:id", change, j.replace)
	g.PATCH("/:id", change, j.patch)
	g.PATCH("/bulk", change, j.inner.bulkPatch)
	g.DELETE("/:id", del, j.inner.remove)
	g.DELETE("/bulk", del, j.inner.bulkDelete)
}

func (j *JMPs) normalize(c *gin.Context, jmp *models.JMP) {
	jmpplan.Enrich(c.Request.Context(), jmp, j.RoutingOSRMURL)
}

func (j *JMPs) create(c *gin.Context) {
	var item models.JMP
	if err := c.ShouldBindJSON(&item); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if item.ID == "" {
		item.ID = generateID(j.inner.IDPrefix)
	}
	j.normalize(c, &item)
	created, err := j.inner.Collection.Add(c.Request.Context(), item)
	if err != nil {
		respondError(c, err)
		return
	}
	j.inner.Repo.LogBest(c.Request.Context(), "create", j.inner.Entity, created.ID, "", currentUser(c, j.inner.Repo))
	c.JSON(http.StatusCreated, created)
}

func (j *JMPs) replace(c *gin.Context) {
	id := c.Param("id")
	ctx := c.Request.Context()
	var item models.JMP
	if err := c.ShouldBindJSON(&item); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	item.ID = id
	j.normalize(c, &item)
	updated, err := j.inner.Collection.Replace(ctx, id, item)
	if err != nil {
		respondError(c, err)
		return
	}
	j.inner.Repo.LogBest(ctx, "update", j.inner.Entity, id, "", currentUser(c, j.inner.Repo))
	c.JSON(http.StatusOK, updated)
}

func (j *JMPs) patch(c *gin.Context) {
	id := c.Param("id")
	ctx := c.Request.Context()
	existing, err := j.inner.Collection.Get(ctx, id)
	if err != nil {
		respondError(c, err)
		return
	}
	patchBytes, err := c.GetRawData()
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	merged, err := mergeJSON(existing, patchBytes)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	j.normalize(c, &merged)
	updated, err := j.inner.Collection.Replace(ctx, id, merged)
	if err != nil {
		respondError(c, err)
		return
	}
	j.inner.Repo.LogBest(ctx, "update", j.inner.Entity, id, "", currentUser(c, j.inner.Repo))
	c.JSON(http.StatusOK, updated)
}
