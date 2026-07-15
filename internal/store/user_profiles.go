package store

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/iag/fleet-tool/backend/internal/models"
)

// UserProfilesStore owns reads + writes against the user_profiles table.
// Mounted on Repository.UserProfiles. Like NotificationsStore it sits outside
// the generic Collection pattern because every access is implicitly scoped to
// a single platform user (the JWT subject), not addressed by a domain ID.
type UserProfilesStore struct {
	pool *pgxpool.Pool
}

// UserProfileInput carries the editable fields for an Upsert. UserID is passed
// separately (from the authenticated claims) so a caller can never write to a
// different user's row.
type UserProfileInput struct {
	DisplayName  string
	Role         string
	Department   string
	Phone        string
	ContactEmail string
	Bio          string
	Avatar       string
}

// Get returns the stored profile for userID. A user who has never saved a
// profile is not an error: we return a zero-value UserProfile with UserID set
// (and an empty UpdatedAt) so the handler can surface a blank-but-valid shape.
func (s *UserProfilesStore) Get(ctx context.Context, userID string) (models.UserProfile, error) {
	const q = `
        SELECT user_id, display_name, role, department, phone, contact_email, bio, avatar,
               to_char(updated_at AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS"Z"')
        FROM user_profiles
        WHERE user_id = $1`
	var p models.UserProfile
	err := s.pool.QueryRow(ctx, q, userID).Scan(
		&p.UserID, &p.DisplayName, &p.Role, &p.Department,
		&p.Phone, &p.ContactEmail, &p.Bio, &p.Avatar, &p.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return models.UserProfile{UserID: userID}, nil
	}
	if err != nil {
		return models.UserProfile{}, err
	}
	return p, nil
}

// Upsert inserts or updates the profile row for userID and returns the stored
// result (with the DB-stamped updated_at).
func (s *UserProfilesStore) Upsert(ctx context.Context, userID string, in UserProfileInput) (models.UserProfile, error) {
	const q = `
        INSERT INTO user_profiles
            (user_id, display_name, role, department, phone, contact_email, bio, avatar, updated_at)
        VALUES ($1, $2, $3, $4, $5, $6, $7, $8, now())
        ON CONFLICT (user_id) DO UPDATE SET
            display_name  = EXCLUDED.display_name,
            role          = EXCLUDED.role,
            department    = EXCLUDED.department,
            phone         = EXCLUDED.phone,
            contact_email = EXCLUDED.contact_email,
            bio           = EXCLUDED.bio,
            avatar        = EXCLUDED.avatar,
            updated_at    = now()
        RETURNING user_id, display_name, role, department, phone, contact_email, bio, avatar,
                  to_char(updated_at AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS"Z"')`
	var p models.UserProfile
	err := s.pool.QueryRow(ctx, q,
		userID, in.DisplayName, in.Role, in.Department,
		in.Phone, in.ContactEmail, in.Bio, in.Avatar,
	).Scan(
		&p.UserID, &p.DisplayName, &p.Role, &p.Department,
		&p.Phone, &p.ContactEmail, &p.Bio, &p.Avatar, &p.UpdatedAt,
	)
	if err != nil {
		return models.UserProfile{}, err
	}
	return p, nil
}
