package models

// UserProfile is the fleet-scoped, editable profile that hangs off a platform
// user account. GET /api/users/me returns it alongside the JWT-derived claims;
// PUT /api/users/me/profile upserts it. UserID is the platform JWT subject.
//
// UpdatedAt is an RFC3339 string (empty on a never-saved profile) to match the
// rest of the API's timestamp convention — see models.Notification.
type UserProfile struct {
	UserID       string `json:"userId"`
	DisplayName  string `json:"displayName"`
	Role         string `json:"role"`
	Department   string `json:"department"`
	Phone        string `json:"phone"`
	ContactEmail string `json:"contactEmail"`
	Bio          string `json:"bio"`
	Avatar       string `json:"avatar"`
	UpdatedAt    string `json:"updatedAt,omitempty"`
}
