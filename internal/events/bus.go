// Package events publishes domain events to the IAG event bus (Kafka / Redpanda).
package events

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/segmentio/kafka-go"
)

const (
	SpecVersion = "1.0"
	Source      = "iag.fleet"

	TopicFleet         = "iag.fleet"
	TopicNotifications = "iag.notifications"
	TopicFinance       = "iag.finance"

	TypeNotificationRequested = "notification.requested"
	TypeFinanceFuelRecorded   = "fleet.fuel.recorded"

	TypeVehicleStatusChanged   = "fleet.vehicle.status_changed"
	TypeJMPCompleted           = "fleet.jmp.completed"
	TypeJMPCancelled           = "fleet.jmp.cancelled"
	TypeCargoStageAdvanced     = "fleet.cargo.stage_advanced"
	TypeCargoOffloaded         = "fleet.cargo.offloaded"
	TypeComplianceExpiring     = "fleet.compliance.expiring"
	TypeTelemetryRefuelDetected = "fleet.telemetry.refuel_detected"
	TypeTelemetryFuelAnomaly    = "fleet.telemetry.fuel_anomaly"
	TypeServiceRequestAssigned = "fleet.service_request.assigned"
)

// Bus publishes fleet domain events and optional notification requests.
type Bus struct {
	fleetWriter   *kafka.Writer
	notifWriter   *kafka.Writer
	financeWriter *kafka.Writer
	enabled       bool
}

// Config for optional Kafka publishing.
type Config struct {
	Brokers []string
	Enabled bool
}

// NewFromEnv builds a bus from EVENT_BUS_ENABLED and KAFKA_BROKERS.
func NewFromEnv() *Bus {
	return New(Config{
		Brokers: ParseBrokers(os.Getenv("KAFKA_BROKERS")),
		Enabled: strings.EqualFold(os.Getenv("EVENT_BUS_ENABLED"), "true"),
	})
}

func New(cfg Config) *Bus {
	if !cfg.Enabled || len(cfg.Brokers) == 0 {
		return &Bus{enabled: false}
	}
	transport := &kafka.Transport{ClientID: Source}
	return &Bus{
		enabled: true,
		fleetWriter: &kafka.Writer{
			Addr:         kafka.TCP(cfg.Brokers...),
			Topic:        TopicFleet,
			Balancer:     &kafka.LeastBytes{},
			RequiredAcks: kafka.RequireOne,
			Transport:    transport,
		},
		notifWriter: &kafka.Writer{
			Addr:         kafka.TCP(cfg.Brokers...),
			Topic:        TopicNotifications,
			Balancer:     &kafka.LeastBytes{},
			RequiredAcks: kafka.RequireOne,
			Transport:    transport,
		},
		financeWriter: &kafka.Writer{
			Addr:         kafka.TCP(cfg.Brokers...),
			Topic:        TopicFinance,
			Balancer:     &kafka.LeastBytes{},
			RequiredAcks: kafka.RequireOne,
			Transport:    transport,
		},
	}
}

func (b *Bus) Close() error {
	if b == nil || !b.enabled {
		return nil
	}
	var err error
	if e := b.fleetWriter.Close(); e != nil {
		err = e
	}
	if e := b.notifWriter.Close(); e != nil && err == nil {
		err = e
	}
	if e := b.financeWriter.Close(); e != nil && err == nil {
		err = e
	}
	return err
}

// Enabled reports whether Kafka publishing is active.
func (b *Bus) Enabled() bool {
	return b != nil && b.enabled
}

// PlatformEvent is the canonical IAG envelope (matches @iag/events).
type PlatformEvent struct {
	ID            string         `json:"id"`
	Type          string         `json:"type"`
	Time          string         `json:"time"`
	Source        string         `json:"source"`
	SpecVersion   string         `json:"specversion"`
	CorrelationID string         `json:"correlationId,omitempty"`
	CausationID   string         `json:"causationId,omitempty"`
	Data          map[string]any `json:"data"`
}

func (b *Bus) publish(ctx context.Context, writer *kafka.Writer, evt PlatformEvent, key string) error {
	if !b.enabled || writer == nil {
		return nil
	}
	if evt.ID == "" {
		evt.ID = uuid.NewString()
	}
	if evt.Time == "" {
		evt.Time = time.Now().UTC().Format(time.RFC3339Nano)
	}
	if evt.Source == "" {
		evt.Source = Source
	}
	if evt.SpecVersion == "" {
		evt.SpecVersion = SpecVersion
	}
	body, err := json.Marshal(evt)
	if err != nil {
		return err
	}
	if key == "" {
		key = evt.ID
	}
	return writer.WriteMessages(ctx, kafka.Message{
		Topic: writer.Topic,
		Key:   []byte(key),
		Value: body,
		Headers: []kafka.Header{
			{Key: "ce-type", Value: []byte(evt.Type)},
			{Key: "ce-source", Value: []byte(evt.Source)},
		},
	})
}

// PublishFleet emits a domain event on iag.fleet. Errors are logged; callers need not fail the HTTP request.
func (b *Bus) PublishFleet(ctx context.Context, eventType string, data map[string]any, key, correlationID string) {
	if !b.enabled {
		return
	}
	evt := PlatformEvent{
		Type:          eventType,
		Data:          data,
		CorrelationID: correlationID,
	}
	if err := b.publish(ctx, b.fleetWriter, evt, key); err != nil {
		slog.Warn("fleet event publish failed", "type", eventType, "err", err)
	}
}

// PublishNotificationRequested sends notification.requested to iag.notifications (async email/SMS/push).
func (b *Bus) PublishNotificationRequested(ctx context.Context, channel, recipient, templateID string, variables map[string]string) {
	if !b.enabled || recipient == "" || templateID == "" {
		return
	}
	vars := map[string]any{}
	for k, v := range variables {
		vars[k] = v
	}
	evt := PlatformEvent{
		Type: TypeNotificationRequested,
		Data: map[string]any{
			"channel":    channel,
			"recipient":  recipient,
			"templateId": templateID,
			"variables":  vars,
		},
	}
	if err := b.publish(ctx, b.notifWriter, evt, recipient); err != nil {
		slog.Warn("notification.requested publish failed", "recipient", recipient, "err", err)
	}
}

// PublishFuelRecorded posts fleet.fuel.recorded to iag.finance for the accounts ledger consumer.
func (b *Bus) PublishFuelRecorded(ctx context.Context, recordID string, amount float64, currency, vendorRef, vehicleID string, litres float64) {
	if !b.enabled || amount <= 0 || recordID == "" {
		return
	}
	evt := PlatformEvent{
		Type: TypeFinanceFuelRecorded,
		Data: map[string]any{
			"amount":      fmt.Sprintf("%.2f", amount),
			"currency":    currency,
			"vendorRef":   vendorRef,
			"documentRef": "fleet:" + recordID,
			"vehicleId":   vehicleID,
			"litres":      fmt.Sprintf("%.2f", litres),
		},
		CorrelationID: recordID,
	}
	if err := b.publish(ctx, b.financeWriter, evt, recordID); err != nil {
		slog.Warn("fleet.fuel.recorded publish failed", "id", recordID, "err", err)
	}
}

// ParseBrokers splits a comma-separated KAFKA_BROKERS value.
func ParseBrokers(raw string) []string {
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if t := strings.TrimSpace(p); t != "" {
			out = append(out, t)
		}
	}
	return out
}

// FleetEventData builds a minimal payload map with string values.
func FleetEventData(fields map[string]string) map[string]any {
	out := make(map[string]any, len(fields))
	for k, v := range fields {
		out[k] = v
	}
	return out
}

// ValidateEnvelope sanity-checks an event before publish (tests / debug).
func ValidateEnvelope(evt PlatformEvent) error {
	if evt.Type == "" || evt.Source == "" {
		return fmt.Errorf("type and source required")
	}
	return nil
}
