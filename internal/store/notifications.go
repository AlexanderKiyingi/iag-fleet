package store

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/iag/fleet-tool/backend/internal/models"
)

// NotificationsStore owns reads + writes against the notifications and
// notification_preferences tables. Mounted on Repository.Notifications.
type NotificationsStore struct {
	pool *pgxpool.Pool
}

// NotificationInput is what the producer passes to Upsert.
type NotificationInput struct {
	UserID   string
	Kind     string
	RefType  string
	RefID    string
	Severity string
	Title    string
	Body     string
	Href     string
}

// Upsert inserts a notification if (user_id, kind, ref_type, ref_id) is new.
func (s *NotificationsStore) Upsert(ctx context.Context, in NotificationInput) (created bool, id string, err error) {
	id = newNotificationID()
	const q = `
        INSERT INTO notifications
            (id, user_id, kind, ref_type, ref_id, severity, title, body, href)
        VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
        ON CONFLICT (user_id, kind, ref_type, ref_id) DO NOTHING
        RETURNING id`
	var returned string
	err = s.pool.QueryRow(ctx, q,
		id, in.UserID, in.Kind, in.RefType, in.RefID,
		in.Severity, in.Title, in.Body, in.Href,
	).Scan(&returned)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, "", nil
	}
	if err != nil {
		return false, "", err
	}
	return true, returned, nil
}

func (s *NotificationsStore) List(ctx context.Context, userID string, limit int) ([]models.Notification, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	const q = `
        SELECT id, user_id, kind, ref_type, ref_id, severity, title, body, href,
               to_char(created_at AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS"Z"'),
               COALESCE(to_char(seen_at AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS"Z"'), ''),
               COALESCE(to_char(dismissed_at AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS"Z"'), '')
        FROM notifications
        WHERE user_id = $1 AND dismissed_at IS NULL
        ORDER BY created_at DESC
        LIMIT $2`
	rows, err := s.pool.Query(ctx, q, userID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]models.Notification, 0, limit)
	for rows.Next() {
		var n models.Notification
		if err := rows.Scan(
			&n.ID, &n.UserID, &n.Kind, &n.RefType, &n.RefID, &n.Severity,
			&n.Title, &n.Body, &n.Href, &n.CreatedAt, &n.SeenAt, &n.DismissedAt,
		); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

func (s *NotificationsStore) UnreadCount(ctx context.Context, userID string) (int, error) {
	var n int
	err := s.pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM notifications
         WHERE user_id = $1 AND seen_at IS NULL AND dismissed_at IS NULL`,
		userID,
	).Scan(&n)
	return n, err
}

func (s *NotificationsStore) MarkSeen(ctx context.Context, userID, id string) error {
	tag, err := s.pool.Exec(ctx,
		`UPDATE notifications SET seen_at = NOW()
         WHERE id = $1 AND user_id = $2 AND seen_at IS NULL`,
		id, userID,
	)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *NotificationsStore) DismissAll(ctx context.Context, userID string) (int, error) {
	tag, err := s.pool.Exec(ctx,
		`UPDATE notifications SET dismissed_at = NOW()
         WHERE user_id = $1 AND dismissed_at IS NULL`,
		userID,
	)
	if err != nil {
		return 0, err
	}
	return int(tag.RowsAffected()), nil
}

func (s *NotificationsStore) Preferences(ctx context.Context, userID string) (models.NotificationPreferences, error) {
	var muted []string
	err := s.pool.QueryRow(ctx,
		`SELECT muted_kinds FROM notification_preferences WHERE user_id = $1`,
		userID,
	).Scan(&muted)
	if errors.Is(err, pgx.ErrNoRows) {
		return models.NotificationPreferences{UserID: userID, MutedKinds: []string{}}, nil
	}
	if err != nil {
		return models.NotificationPreferences{}, err
	}
	if muted == nil {
		muted = []string{}
	}
	return models.NotificationPreferences{UserID: userID, MutedKinds: muted}, nil
}

func (s *NotificationsStore) PutPreferences(ctx context.Context, userID string, kinds []string) error {
	if kinds == nil {
		kinds = []string{}
	}
	_, err := s.pool.Exec(ctx, `
        INSERT INTO notification_preferences (user_id, muted_kinds, updated_at)
        VALUES ($1, $2, NOW())
        ON CONFLICT (user_id) DO UPDATE
            SET muted_kinds = EXCLUDED.muted_kinds,
                updated_at  = NOW()`,
		userID, kinds,
	)
	return err
}

// RecipientEmail returns the email for a registered platform user, if any.
func (s *NotificationsStore) RecipientEmail(ctx context.Context, userID string) (string, error) {
	var email string
	err := s.pool.QueryRow(ctx,
		`SELECT email FROM notification_recipients WHERE user_id = $1`, userID,
	).Scan(&email)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	return email, err
}

// RegisterRecipient records a platform user for in-app notification fan-out.
func (s *NotificationsStore) RegisterRecipient(ctx context.Context, userID, email string) error {
	_, err := s.pool.Exec(ctx, `
        INSERT INTO notification_recipients (user_id, email, registered_at)
        VALUES ($1, $2, NOW())
        ON CONFLICT (user_id) DO UPDATE
            SET email = CASE WHEN EXCLUDED.email <> '' THEN EXCLUDED.email ELSE notification_recipients.email END`,
		userID, email,
	)
	return err
}

// ActiveRecipientIDs lists platform users registered via GET /api/users/me.
func (s *NotificationsStore) ActiveRecipientIDs(ctx context.Context) ([]string, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT user_id FROM notification_recipients ORDER BY registered_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]string, 0, 16)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

func (s *NotificationsStore) MutedKindsByUser(ctx context.Context) (map[string]map[string]struct{}, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT user_id, muted_kinds FROM notification_preferences`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[string]map[string]struct{}, 16)
	for rows.Next() {
		var uid string
		var kinds []string
		if err := rows.Scan(&uid, &kinds); err != nil {
			return nil, err
		}
		set := make(map[string]struct{}, len(kinds))
		for _, k := range kinds {
			set[k] = struct{}{}
		}
		out[uid] = set
	}
	return out, rows.Err()
}

// LegacyMutedKindsByUser supports int64 keys during transition (unused after 0005).
func LegacyUserIDString(id int64) string {
	return strconv.FormatInt(id, 10)
}

func newNotificationID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "NTF-" + fmt.Sprintf("%016x", uint64(0))
	}
	return "NTF-" + hex.EncodeToString(b[:])
}
