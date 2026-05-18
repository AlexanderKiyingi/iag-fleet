package models

// Notification is one user-scoped signal in the bell.
//
// `Kind` is a stable string the frontend can switch on (e.g.
// "compliance_expired"); `RefType`+`RefID` point back at the underlying
// row so the UI can deep-link without a separate lookup.
//
// SeenAt and DismissedAt are RFC3339 strings (or empty when null) so the
// JSON shape matches the rest of the API — no nullable time pointers
// leaking through to the client.
type Notification struct {
	ID          string `json:"id"`
	UserID      string `json:"userId"`
	Kind        string `json:"kind"`
	RefType     string `json:"refType"`
	RefID       string `json:"refId"`
	Severity    string `json:"severity"` // crit | warn | info
	Title       string `json:"title"`
	Body        string `json:"body,omitempty"`
	Href        string `json:"href,omitempty"`
	CreatedAt   string `json:"createdAt"`
	SeenAt      string `json:"seenAt,omitempty"`
	DismissedAt string `json:"dismissedAt,omitempty"`
}

// NotificationPreferences is the per-user mute list. The handler hands
// the slice straight through; the frontend toggles entries in/out.
type NotificationPreferences struct {
	UserID     string   `json:"userId"`
	MutedKinds []string `json:"mutedKinds"`
}
