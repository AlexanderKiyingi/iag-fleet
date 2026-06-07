package store

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/iag/fleet-tool/backend/internal/models"
)

// SyncDriverComplianceDocs mirrors driver permit/cert fields onto compliance_items.
func (r *Repository) SyncDriverComplianceDocs(ctx context.Context, d models.Driver) error {
	today := time.Now().UTC()
	docs := []struct {
		docType   string
		docNumber string
		expiry    string
		active    bool
	}{
		{"Driving Permit", d.PermitNo, d.PermitExpiry, d.PermitNo != "" || d.PermitExpiry != ""},
		{"First Aid", d.PermitNo, d.FirstAidExpiry, d.FirstAid && d.FirstAidExpiry != ""},
		{"Defensive Driving", d.PermitNo, d.DefensiveExpiry, d.Defensive && d.DefensiveExpiry != ""},
		{"Medical", d.PermitNo, d.MedicalExpiry, d.MedicalExpiry != ""},
	}
	for _, doc := range docs {
		if !doc.active {
			continue
		}
		status := ComplianceStatusFromExpiry(doc.expiry, today, ComplianceExpiringWithinDays)
		existing, found, err := r.findComplianceByDriverDoc(ctx, d.ID, doc.docType)
		if err != nil {
			return err
		}
		if found {
			_, err = r.Compliance.Update(ctx, existing.ID, func(ci *models.ComplianceItem) {
				ci.DocNumber = doc.docNumber
				ci.Expiry = doc.expiry
				ci.Status = status
			})
		} else {
			_, err = r.Compliance.Add(ctx, models.ComplianceItem{
				ID:        newStoreID("CMP"),
				DriverID:  d.ID,
				DocType:   doc.docType,
				DocNumber: doc.docNumber,
				Expiry:    doc.expiry,
				Status:    status,
			})
		}
		if err != nil {
			return fmt.Errorf("sync %s: %w", doc.docType, err)
		}
	}
	return nil
}

func (r *Repository) findComplianceByDriverDoc(ctx context.Context, driverID, docType string) (models.ComplianceItem, bool, error) {
	const q = `
		SELECT id FROM compliance_items
		WHERE driver_id = $1 AND doc_type = $2
		LIMIT 1`
	var id string
	err := r.pool.QueryRow(ctx, q, driverID, docType).Scan(&id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return models.ComplianceItem{}, false, nil
		}
		return models.ComplianceItem{}, false, err
	}
	item, err := r.Compliance.Get(ctx, id)
	if err != nil {
		return models.ComplianceItem{}, false, err
	}
	return item, true, nil
}

func newStoreID(prefix string) string {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	tail := strings.ToUpper(hex.EncodeToString(b))
	if prefix == "" {
		return tail
	}
	return prefix + "-" + tail
}

// DriverPermitOK returns false when the driver's permit is expired or missing.
func DriverPermitOK(d models.Driver, today time.Time) bool {
	if d.PermitExpiry == "" {
		return false
	}
	exp, err := time.Parse("2006-01-02", d.PermitExpiry)
	if err != nil {
		return false
	}
	return !exp.Before(today.UTC().Truncate(24 * time.Hour))
}
