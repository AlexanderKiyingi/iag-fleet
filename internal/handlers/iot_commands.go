package handlers

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/iag/fleet-iot/iot"
	"github.com/iag/fleet-tool/backend/internal/auth"
)

// Device command HTTP surface (queue only — delivery stays on the TCP gateway
// when the unit next connects).
//
//	GET  /api/iot/command-models              models with a verified encoder
//	GET  /api/iot/devices/:id/commands       recent command history
//	POST /api/iot/devices/:id/commands       queue immobilize | mobilize
//	GET  /api/vehicles/:id/commands          history via the vehicle's active device
//	POST /api/vehicles/:id/commands          queue via the vehicle's active device
//
// Enqueue refuses immediately when the device model has no registered encoder,
// so the operator is not left with a pending row that can never leave the
// server. Delivery-time interlocks (speed / fix age) still run in Fleet_IoT.

func (h *IoT) registerCommands(rg *gin.RouterGroup) {
	// Catalogue of models that can actually receive a command. No store needed.
	rg.GET("/iot/command-models", auth.RequireAnyPerm("manage_iot_device", "view_telemetry"), h.commandModels)

	dev := rg.Group("/iot/devices", auth.RequirePerm("manage_iot_device"), h.requireStore)
	dev.GET("/:id/commands", h.listDeviceCommands)
	dev.POST("/:id/commands", h.enqueueDeviceCommand)

	veh := rg.Group("/vehicles", auth.RequirePerm("manage_iot_device"), h.requireStore)
	veh.GET("/:id/commands", h.listVehicleCommands)
	veh.POST("/:id/commands", h.enqueueVehicleCommand)
}

func (h *IoT) commandModels(c *gin.Context) {
	models := iot.SupportedCommandModels()
	if models == nil {
		models = []string{}
	}
	c.JSON(http.StatusOK, gin.H{
		"models":  models,
		"kinds":   []string{iot.CommandImmobilize, iot.CommandMobilize},
		"enabled": len(models) > 0,
		"note": "Immobilise is offered only for models with a verified wire encoder. " +
			"An empty list means no production encoder is registered yet — queueing would never reach a device.",
	})
}

type enqueueCommandBody struct {
	Kind string `json:"kind" binding:"required"`
}

type commandJSON struct {
	ID          int64     `json:"id"`
	DeviceID    int64     `json:"deviceId"`
	VehicleID   string    `json:"vehicleId"`
	Kind        string    `json:"kind"`
	Status      string    `json:"status"`
	RequestedBy string    `json:"requestedBy"`
	RequestedAt time.Time `json:"requestedAt"`
	ExpiresAt   time.Time `json:"expiresAt"`
}

func commandToJSON(c *iot.DeviceCommand) commandJSON {
	return commandJSON{
		ID: c.ID, DeviceID: c.DeviceID, VehicleID: c.VehicleID,
		Kind: c.Kind, Status: c.Status, RequestedBy: c.RequestedBy,
		RequestedAt: c.RequestedAt, ExpiresAt: c.ExpiresAt,
	}
}

func (h *IoT) listDeviceCommands(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}
	limit, _ := strconv.Atoi(c.Query("limit"))
	cmds, err := h.Store.ListDeviceCommands(c.Request.Context(), id, limit)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	out := make([]commandJSON, 0, len(cmds))
	for i := range cmds {
		out = append(out, commandToJSON(&cmds[i]))
	}
	c.JSON(http.StatusOK, out)
}

func (h *IoT) enqueueDeviceCommand(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}
	d, err := h.Store.GetDevice(c.Request.Context(), id)
	if err != nil {
		respondIotError(c, err)
		return
	}
	h.enqueueForDevice(c, d)
}

func (h *IoT) listVehicleCommands(c *gin.Context) {
	vehicleID := strings.TrimSpace(c.Param("id"))
	if vehicleID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "vehicle id required"})
		return
	}
	d, err := h.Store.FindActiveByVehicleID(c.Request.Context(), vehicleID)
	if err != nil {
		respondIotError(c, err)
		return
	}
	limit, _ := strconv.Atoi(c.Query("limit"))
	cmds, err := h.Store.ListDeviceCommands(c.Request.Context(), d.ID, limit)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	out := make([]commandJSON, 0, len(cmds))
	for i := range cmds {
		out = append(out, commandToJSON(&cmds[i]))
	}
	c.JSON(http.StatusOK, gin.H{"deviceId": d.ID, "model": d.Model, "commands": out})
}

func (h *IoT) enqueueVehicleCommand(c *gin.Context) {
	vehicleID := strings.TrimSpace(c.Param("id"))
	if vehicleID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "vehicle id required"})
		return
	}
	d, err := h.Store.FindActiveByVehicleID(c.Request.Context(), vehicleID)
	if err != nil {
		respondIotError(c, err)
		return
	}
	h.enqueueForDevice(c, d)
}

func (h *IoT) enqueueForDevice(c *gin.Context, d *iot.Device) {
	var body enqueueCommandBody
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	kind := strings.ToLower(strings.TrimSpace(body.Kind))
	if kind != iot.CommandImmobilize && kind != iot.CommandMobilize {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "kind must be \"immobilize\" or \"mobilize\"",
		})
		return
	}
	if !d.IsActive {
		c.JSON(http.StatusBadRequest, gin.H{"error": "device is disabled"})
		return
	}
	if d.VehicleID == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "assign this device to a vehicle before queuing a command",
		})
		return
	}
	// Refuse at queue time when no encoder exists — otherwise the row sits
	// pending forever and the operator believes the engine was cut.
	if _, err := iot.EncodeCommand(d.Model, kind); err != nil {
		status := http.StatusUnprocessableEntity
		c.JSON(status, gin.H{
			"error": err.Error(),
			"model": d.Model,
			"hint":  "No verified command encoder is registered for this model. Immobilise stays unavailable until one is confirmed against real hardware.",
		})
		return
	}

	requestedBy := h.commandActor(c)
	cmd, err := h.Store.EnqueueCommand(
		c.Request.Context(), d.ID, d.VehicleID, kind, requestedBy, 0,
	)
	if err != nil {
		if errors.Is(err, iot.ErrCommandPending) {
			c.JSON(http.StatusConflict, gin.H{
				"error": "this device already has a pending command; wait for delivery or expiry",
			})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusAccepted, gin.H{
		"command": commandToJSON(cmd),
		"note":    "Queued. Delivery happens on the device's next TCP session, subject to speed/fix interlocks.",
	})
}

func (h *IoT) commandActor(c *gin.Context) string {
	if h.Repo != nil {
		return currentUser(c, h.Repo)
	}
	if name := auth.OperatorName(c); name != "" {
		return name
	}
	return "anonymous"
}
