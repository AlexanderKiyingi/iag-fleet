package handlers

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/iag/fleet-tool/backend/internal/auth"
	"github.com/iag/fleet-tool/backend/internal/notifications"
	"github.com/iag/fleet-tool/backend/internal/store"
)

// Notifications wires the per-user bell endpoints.
type Notifications struct {
	Repo   *store.Repository
	Broker *notifications.Broker
}

func (h *Notifications) Register(rg *gin.RouterGroup) {
	g := rg.Group("/notifications")
	g.GET("", auth.RequirePerm("view_notification"), h.list)
	g.POST("/dismiss-all", auth.RequirePerm("change_notification"), h.dismissAll)
	g.POST("/:id/seen", auth.RequirePerm("change_notification"), h.markSeen)
	g.GET("/preferences", auth.RequireUser(), h.getPrefs)
	g.PUT("/preferences", auth.RequireUser(), h.putPrefs)
	g.GET("/stream", auth.RequirePerm("view_notification"), h.stream)
}

type listResponse struct {
	Items  any `json:"items"`
	Unread int `json:"unread"`
}

func (h *Notifications) actorID(c *gin.Context) (string, bool) {
	return auth.ActorUserKey(c)
}

func (h *Notifications) list(c *gin.Context) {
	uid, ok := h.actorID(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "authentication required"})
		return
	}
	items, err := h.Repo.Notifications.List(c.Request.Context(), uid, 0)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "list notifications"})
		return
	}
	unread, err := h.Repo.Notifications.UnreadCount(c.Request.Context(), uid)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "unread count"})
		return
	}
	c.JSON(http.StatusOK, listResponse{Items: items, Unread: unread})
}

func (h *Notifications) markSeen(c *gin.Context) {
	uid, ok := h.actorID(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "authentication required"})
		return
	}
	id := c.Param("id")
	err := h.Repo.Notifications.MarkSeen(c.Request.Context(), uid, id)
	if errors.Is(err, store.ErrNotFound) {
		c.JSON(http.StatusNotFound, gin.H{"error": "notification not found"})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "mark seen"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

func (h *Notifications) dismissAll(c *gin.Context) {
	uid, ok := h.actorID(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "authentication required"})
		return
	}
	n, err := h.Repo.Notifications.DismissAll(c.Request.Context(), uid)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "dismiss-all"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"dismissed": n})
}

func (h *Notifications) getPrefs(c *gin.Context) {
	uid, ok := h.actorID(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "authentication required"})
		return
	}
	prefs, err := h.Repo.Notifications.Preferences(c.Request.Context(), uid)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "load preferences"})
		return
	}
	c.JSON(http.StatusOK, prefs)
}

type putPrefsRequest struct {
	MutedKinds []string `json:"mutedKinds"`
}

func (h *Notifications) putPrefs(c *gin.Context) {
	uid, ok := h.actorID(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "authentication required"})
		return
	}
	var body putPrefsRequest
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid body"})
		return
	}
	if err := h.Repo.Notifications.PutPreferences(c.Request.Context(), uid, body.MutedKinds); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "save preferences"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true, "mutedKinds": body.MutedKinds})
}

func (h *Notifications) stream(c *gin.Context) {
	uid, ok := h.actorID(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "authentication required"})
		return
	}

	c.Writer.Header().Set("Content-Type", "text/event-stream")
	c.Writer.Header().Set("Cache-Control", "no-cache")
	c.Writer.Header().Set("Connection", "keep-alive")
	c.Writer.Header().Set("X-Accel-Buffering", "no")
	c.Writer.WriteHeader(http.StatusOK)
	flusher, ok := c.Writer.(http.Flusher)
	if !ok {
		return
	}

	wake, cancel := h.Broker.Subscribe(uid)
	defer cancel()

	ctx := c.Request.Context()

	emit := func() {
		items, err := h.Repo.Notifications.List(ctx, uid, 0)
		if err != nil {
			return
		}
		unread, err := h.Repo.Notifications.UnreadCount(ctx, uid)
		if err != nil {
			return
		}
		payload, err := json.Marshal(struct {
			Items       any    `json:"items"`
			Unread      int    `json:"unread"`
			GeneratedAt string `json:"generatedAt"`
		}{
			Items:       items,
			Unread:      unread,
			GeneratedAt: time.Now().UTC().Format(time.RFC3339),
		})
		if err != nil {
			return
		}
		fmt.Fprintf(c.Writer, "event: bell\ndata: %s\n\n", payload)
		flusher.Flush()
	}

	emit()

	keepAlive := time.NewTicker(25 * time.Second)
	defer keepAlive.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case _, open := <-wake:
			if !open {
				return
			}
			emit()
		case <-keepAlive.C:
			fmt.Fprintf(c.Writer, ": keep-alive\n\n")
			flusher.Flush()
		}
	}
}
