package handlers

import (
	"time"

	"github.com/iag/fleet-tool/backend/internal/models"
)

func appendStatusHistory(h *models.StatusHistory, status, by, note string) {
	*h = append(*h, models.StatusHistoryEvent{
		At:     time.Now().UTC().Format(time.RFC3339),
		Status: status,
		By:     by,
		Note:   note,
	})
}

func appendAnomalyEvent(h *models.AnomalyHistory, event, by, note string) {
	*h = append(*h, models.AnomalyHistoryEvent{
		At:    time.Now().UTC().Format(time.RFC3339),
		Event: event,
		By:    by,
		Note:  note,
	})
}
