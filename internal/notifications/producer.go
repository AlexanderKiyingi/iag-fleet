package notifications

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/iag/fleet-tool/backend/internal/events"
	"github.com/iag/fleet-tool/backend/internal/models"
	"github.com/iag/fleet-tool/backend/internal/store"
)

// Kinds the producer emits. Keeping these as constants means a typo in
// one place doesn't silently route a notification past the user's mute
// list (the mute set keys on these strings).
const (
	KindComplianceExpired  = "compliance_expired"
	KindComplianceMissing  = "compliance_missing"
	KindComplianceExpiring = "compliance_expiring"
	KindSafetyCritical     = "safety_crit"
	KindFuelAnomaly        = "fuel_anomaly"
	KindPartsStockOut      = "parts_stockout"
	KindPartsLowStock      = "parts_low"
	KindRequestSubmitted   = "request_submitted"
	KindRequestReviewed    = "request_reviewed"
	KindCargoAtAcp         = "cargo_at_acp"
)

// Producer scans the same conditions the bell used to recompute on
// every render in the frontend (Topbar.tsx). One pass per tick:
// gather the set of "currently signalable" events, fan them out to
// every active user, and let the unique constraint on the notifications
// table decide which are net-new.
type Producer struct {
	Repo       *store.Repository
	Broker     *Broker
	Events *events.Bus

	// Now is injectable for tests; production callers leave it nil and
	// the producer uses time.Now.UTC.
	Now func() time.Time
}

// Run blocks for the lifetime of ctx, ticking every interval. A first
// scan fires immediately so the bell isn't empty for `interval` after
// boot. Errors during a scan are logged and the loop continues — a
// transient DB hiccup shouldn't stop notifications forever.
func (p *Producer) Run(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = time.Minute
	}
	if err := p.Scan(ctx); err != nil {
		slog.Warn("notifications: initial scan failed", "err", err)
	}
	tick := time.NewTicker(interval)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			if err := p.Scan(ctx); err != nil {
				slog.Warn("notifications: scan failed", "err", err)
			}
		}
	}
}

// Scan runs one full pass. Returns the number of notifications actually
// inserted across all users.
func (p *Producer) Scan(ctx context.Context) error {
	users, err := p.Repo.Notifications.ActiveRecipientIDs(ctx)
	if err != nil {
		return fmt.Errorf("active recipients: %w", err)
	}
	if len(users) == 0 {
		return nil
	}

	muted, err := p.Repo.Notifications.MutedKindsByUser(ctx)
	if err != nil {
		return fmt.Errorf("muted prefs: %w", err)
	}

	signals, err := p.collectSignals(ctx)
	if err != nil {
		return err
	}
	if len(signals) == 0 {
		return nil
	}

	totalNew := 0
	wakeUsers := make(map[string]struct{})
	for _, sig := range signals {
		for _, uid := range users {
			if isMuted(muted, uid, sig.Kind) {
				continue
			}
			created, _, err := p.Repo.Notifications.Upsert(ctx, store.NotificationInput{
				UserID:   uid,
				Kind:     sig.Kind,
				RefType:  sig.RefType,
				RefID:    sig.RefID,
				Severity: sig.Severity,
				Title:    sig.Title,
				Body:     sig.Body,
				Href:     sig.Href,
			})
			if err != nil {
				// Single failed insert shouldn't abort the whole scan;
				// log and keep going so other users still get fanned.
				slog.Warn("notifications: upsert failed",
					"err", err, "kind", sig.Kind, "ref", sig.RefID, "userID", uid)
				continue
			}
			if created {
				totalNew++
				wakeUsers[uid] = struct{}{}
				p.maybePublishSignal(ctx, uid, sig)
			}
		}
	}

	if p.Broker != nil {
		for uid := range wakeUsers {
			p.Broker.Publish(uid)
		}
	}
	if totalNew > 0 {
		slog.Info("notifications: scan inserted", "count", totalNew, "wokeUsers", len(wakeUsers))
	}
	return nil
}

// signal is one (kind, ref) pair the producer wants to fan out. Plain
// struct so collectSignals can append from each source slice without
// caring about user ids — those are filled in later.
type signal struct {
	Kind     string
	RefType  string
	RefID    string
	Severity string
	Title    string
	Body     string
	Href     string
}

func (p *Producer) collectSignals(ctx context.Context) ([]signal, error) {
	var out []signal

	// ── Compliance: expired / missing → crit; expiring → warn.
	compliance, err := p.Repo.Compliance.List(ctx)
	if err == nil {
		for _, c := range compliance {
			switch c.Status {
			case "expired":
				out = append(out, signal{
					Kind:     KindComplianceExpired,
					RefType:  "compliance",
					RefID:    c.ID,
					Severity: "crit",
					Title:    fmt.Sprintf("%s expired", c.DocType),
					Body:     complianceSubject(c),
					Href:     "/compliance",
				})
			case "missing":
				out = append(out, signal{
					Kind:     KindComplianceMissing,
					RefType:  "compliance",
					RefID:    c.ID,
					Severity: "crit",
					Title:    fmt.Sprintf("%s missing", c.DocType),
					Body:     complianceSubject(c),
					Href:     "/compliance",
				})
			case "expiring":
				out = append(out, signal{
					Kind:     KindComplianceExpiring,
					RefType:  "compliance",
					RefID:    c.ID,
					Severity: "warn",
					Title:    fmt.Sprintf("%s expiring", c.DocType),
					Body:     complianceSubject(c) + " · " + c.Expiry,
					Href:     "/compliance",
				})
			}
		}
	}

	// ── Safety: critical events still open or under investigation.
	safety, err := p.Repo.Safety.List(ctx)
	if err == nil {
		for _, s := range safety {
			if s.Severity != "crit" {
				continue
			}
			if s.Status != "open" && s.Status != "investigating" {
				continue
			}
			out = append(out, signal{
				Kind:     KindSafetyCritical,
				RefType:  "safety",
				RefID:    s.ID,
				Severity: "crit",
				Title:    "Safety · " + s.Type,
				Body:     truncate(s.Description, 120),
				Href:     "/safety",
			})
		}
	}

	// ── Fuel: anomaly flagged on a record.
	fuel, err := p.Repo.Fuel.List(ctx)
	if err == nil {
		for _, f := range fuel {
			if f.Anomaly == nil || !*f.Anomaly {
				continue
			}
			body := f.AnomalyReason
			if body == "" {
				body = fmt.Sprintf("%.0f L · %s", f.Litres, f.Date)
			}
			out = append(out, signal{
				Kind:     KindFuelAnomaly,
				RefType:  "fuel",
				RefID:    f.ID,
				Severity: "warn",
				Title:    "Fuel anomaly",
				Body:     body,
				Href:     "/fuel",
			})
		}
	}

	// ── Parts: stock-out → crit, low-stock → warn.
	parts, err := p.Repo.Parts.List(ctx)
	if err == nil {
		for _, prt := range parts {
			switch {
			case prt.Stock <= 0:
				out = append(out, signal{
					Kind:     KindPartsStockOut,
					RefType:  "parts",
					RefID:    prt.ID,
					Severity: "crit",
					Title:    "Stock-out · " + prt.Name,
					Body:     "SKU " + prt.SKU,
					Href:     "/inventory",
				})
			case prt.Stock <= prt.ReorderPoint:
				out = append(out, signal{
					Kind:     KindPartsLowStock,
					RefType:  "parts",
					RefID:    prt.ID,
					Severity: "warn",
					Title:    "Low stock · " + prt.Name,
					Body:     fmt.Sprintf("SKU %s · %d left", prt.SKU, prt.Stock),
					Href:     "/inventory",
				})
			}
		}
	}

	// ── Service requests: submitted → warn (needs review), reviewed → info.
	requests, err := p.Repo.Requests.List(ctx)
	if err == nil {
		for _, r := range requests {
			switch r.Status {
			case "submitted":
				out = append(out, signal{
					Kind:     KindRequestSubmitted,
					RefType:  "requests",
					RefID:    r.ID,
					Severity: "warn",
					Title:    "New vehicle request",
					Body:     r.RequesterDept,
					Href:     "/requests/" + r.ID,
				})
			case "reviewed":
				out = append(out, signal{
					Kind:     KindRequestReviewed,
					RefType:  "requests",
					RefID:    r.ID,
					Severity: "info",
					Title:    "Request reviewed",
					Body:     r.RequesterDept,
					Href:     "/requests/" + r.ID,
				})
			}
		}
	}

	// ── Cargo: trucks parked at ACP yard.
	cargo, err := p.Repo.Cargo.List(ctx)
	if err == nil {
		for _, c := range cargo {
			if c.Stage != "at-acp" {
				continue
			}
			out = append(out, signal{
				Kind:     KindCargoAtAcp,
				RefType:  "cargo",
				RefID:    c.ID,
				Severity: "warn",
				Title:    "Cargo at ACP",
				Body:     c.TruckPlate,
				Href:     "/cargo/" + c.ID,
			})
		}
	}

	return out, nil
}

// complianceSubject is the entity behind a compliance row, used as the
// notification body. We don't have the joined name here without a
// second query; the bell deep-links to /compliance which displays it.
func complianceSubject(c models.ComplianceItem) string {
	if c.DriverID != "" {
		return "Driver " + c.DriverID
	}
	if c.VehicleID != "" {
		return "Vehicle " + c.VehicleID
	}
	return c.DocNumber
}

func isMuted(muted map[string]map[string]struct{}, userID, kind string) bool {
	set, ok := muted[userID]
	if !ok {
		return false
	}
	_, isMuted := set[kind]
	return isMuted
}

func (p *Producer) maybePublishSignal(ctx context.Context, userID string, sig signal) {
	if p.Events == nil || !p.Events.Enabled() {
		return
	}
	eventType := "fleet.notification." + sig.Kind
	p.Events.PublishFleet(ctx, eventType, events.FleetEventData(map[string]string{
		"kind":    sig.Kind,
		"refType": sig.RefType,
		"refId":   sig.RefID,
		"userId":  userID,
	}), sig.RefID, sig.RefID)

	if sig.Severity != "crit" {
		return
	}
	email, err := p.Repo.Notifications.RecipientEmail(ctx, userID)
	if err != nil || email == "" {
		return
	}
	p.Events.PublishNotificationRequested(ctx, "email", email, "fleet.alert", map[string]string{
		"title": sig.Title,
		"body":  sig.Body,
		"href":  sig.Href,
	})
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max-1] + "…"
}
