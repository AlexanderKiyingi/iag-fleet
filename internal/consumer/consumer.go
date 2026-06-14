// Package consumer subscribes fleet to warehouse ("stores") domain events so
// fleet's local parts.stock stays a faithful projection of the warehouse
// system-of-record under the stock-delegation model.
//
// It is the first inbound event consumer in fleet (the events package only
// produces to iag.fleet). It listens on iag.operations and reacts to
// warehouse.movement.posted by refreshing the affected part's projected stock
// from the authoritative warehouse on-hand. The refresh is a SET (not a
// delta), so it is idempotent and immune to ordering/duplication — the
// dedupe table is a belt-and-suspenders guard plus an audit trail.
package consumer

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/segmentio/kafka-go"
)

const (
	// TopicOperations is the cross-domain bus topic warehouse publishes to.
	TopicOperations = "iag.operations"

	typeMovementPosted    = "warehouse.movement.posted"
	typeStockBelowMinimum = "warehouse.stock.below_minimum"
)

// onHandReader is the slice of the warehouse client the consumer needs.
type onHandReader interface {
	OnHandTotal(ctx context.Context, itemID string) (float64, error)
}

// Config configures the consumer.
type Config struct {
	Brokers []string
	GroupID string
	Topic   string
}

// Consumer refreshes fleet's parts-stock projection from warehouse events.
type Consumer struct {
	cfg       Config
	pool      *pgxpool.Pool
	warehouse onHandReader
}

func New(cfg Config, pool *pgxpool.Pool, warehouse onHandReader) *Consumer {
	if cfg.Topic == "" {
		cfg.Topic = TopicOperations
	}
	if cfg.GroupID == "" {
		cfg.GroupID = "iag-fleet-warehouse-projection"
	}
	return &Consumer{cfg: cfg, pool: pool, warehouse: warehouse}
}

// Run blocks until ctx is cancelled. A nil/empty broker list is a no-op so the
// service still boots in environments without Kafka.
func (c *Consumer) Run(ctx context.Context) {
	if len(c.cfg.Brokers) == 0 {
		slog.Info("fleet warehouse consumer: KAFKA_BROKERS unset — skipping")
		return
	}
	r := kafka.NewReader(kafka.ReaderConfig{
		Brokers:     c.cfg.Brokers,
		GroupID:     c.cfg.GroupID,
		GroupTopics: []string{c.cfg.Topic},
		MinBytes:    1,
		MaxBytes:    10e6,
	})
	defer r.Close()

	slog.Info("fleet warehouse consumer listening", "topic", c.cfg.Topic, "group", c.cfg.GroupID)
	for {
		msg, err := r.FetchMessage(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			slog.Warn("fleet warehouse consumer fetch", "err", err)
			continue
		}
		if err := c.handleMessage(ctx, msg); err != nil {
			slog.Warn("fleet warehouse consumer handle", "topic", msg.Topic, "err", err)
			// Don't commit a failed message — let it be redelivered.
			continue
		}
		if err := r.CommitMessages(ctx, msg); err != nil {
			slog.Warn("fleet warehouse consumer commit", "err", err)
		}
	}
}

type platformEvent struct {
	ID   string         `json:"id"`
	Type string         `json:"type"`
	Data map[string]any `json:"data"`
}

func (c *Consumer) handleMessage(ctx context.Context, msg kafka.Message) error {
	var env platformEvent
	if err := json.Unmarshal(msg.Value, &env); err != nil {
		// Poison message: log and skip (commit) rather than blocking the
		// partition forever.
		slog.Warn("fleet warehouse consumer: undecodable message; skipping", "err", err)
		return nil
	}

	// Only the event types we act on are worth deduping/processing.
	if env.Type != typeMovementPosted && env.Type != typeStockBelowMinimum {
		return nil
	}

	eventID := env.ID
	if eventID == "" {
		eventID = string(msg.Key)
	}
	if eventID != "" {
		tag, err := c.pool.Exec(ctx, `
			INSERT INTO warehouse_event_dedupe (event_id, topic) VALUES ($1, $2)
			ON CONFLICT (event_id) DO NOTHING`, eventID, msg.Topic)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return nil // already processed
		}
	}

	switch env.Type {
	case typeMovementPosted:
		return c.handleMovementPosted(ctx, env.Data)
	case typeStockBelowMinimum:
		c.handleBelowMinimum(env.Data)
		return nil
	}
	return nil
}

// handleMovementPosted refreshes the projected stock of the fleet part mapped
// to the moved warehouse item. Items fleet doesn't carry are silently ignored.
func (c *Consumer) handleMovementPosted(ctx context.Context, data map[string]any) error {
	itemID, _ := data["item_id"].(string)
	sku, _ := data["sku"].(string)
	if itemID == "" && sku == "" {
		return nil
	}

	// Is this a part fleet tracks? Match on the stored warehouse item id or,
	// before reconciliation has backfilled it, on SKU.
	var partID string
	err := c.pool.QueryRow(ctx, `
		SELECT id FROM parts
		WHERE (warehouse_item_id = $1 AND $1 <> '')
		   OR (sku = $2 AND $2 <> '')
		LIMIT 1`, itemID, sku).Scan(&partID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if itemID == "" {
		// Can't fetch authoritative on-hand without the warehouse id; nothing
		// to do until reconciliation backfills warehouse_item_id.
		return nil
	}

	total, err := c.warehouse.OnHandTotal(ctx, itemID)
	if err != nil {
		return err
	}

	_, err = c.pool.Exec(ctx, `
		UPDATE parts
		   SET stock = $1,
		       warehouse_synced_at = now(),
		       warehouse_item_id = CASE
		           WHEN warehouse_item_id IS NULL OR warehouse_item_id = '' THEN $2
		           ELSE warehouse_item_id END
		 WHERE (warehouse_item_id = $2 AND $2 <> '')
		    OR (sku = $3 AND $3 <> '')`,
		int(total), itemID, sku)
	if err != nil {
		return err
	}
	slog.Debug("fleet stock projection refreshed", "part", partID, "item", itemID, "onHand", total)
	return nil
}

func (c *Consumer) handleBelowMinimum(data map[string]any) {
	sku, _ := data["sku"].(string)
	slog.Info("warehouse reported stock below minimum", "sku", sku)
	// Fleet's notifications producer already raises low-stock alerts off the
	// refreshed projection, so no extra fan-out is needed here.
}
