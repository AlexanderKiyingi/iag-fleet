package ctxkeys

// Gin context keys for platform authentication (gateway / JWT modes).
const (
	UserID      = "platform.user_id"
	Claims      = "platform.claims"
	Permissions = "platform.permissions"
)
