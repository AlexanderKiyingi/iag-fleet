package store

import (
	"context"
	"time"
)

func (r *Repository) LogAPIRequest(ctx context.Context, method, path string, statusCode int, userName string, durationMs int, clientIP string) error {
	if r == nil || r.pool == nil {
		return nil
	}
	_, err := r.pool.Exec(ctx, `
		INSERT INTO fleet_api_audit (method, path, status_code, user_name, duration_ms, client_ip)
		VALUES ($1, $2, $3, $4, $5, $6)
	`, method, path, statusCode, userName, durationMs, clientIP)
	return err
}

func (r *Repository) ListAPIAuditLogs(ctx context.Context, limit int) ([]map[string]any, int, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	var total int
	if err := r.pool.QueryRow(ctx, `SELECT COUNT(*)::int FROM fleet_api_audit`).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := r.pool.Query(ctx, `
		SELECT method, path, status_code, user_name, duration_ms, logged_at, client_ip
		FROM fleet_api_audit ORDER BY logged_at DESC LIMIT $1
	`, limit)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var out []map[string]any
	for rows.Next() {
		var method, path, user, ip string
		var status, dur int
		var at time.Time
		if err := rows.Scan(&method, &path, &status, &user, &dur, &at, &ip); err != nil {
			return nil, 0, err
		}
		out = append(out, map[string]any{
			"method": method, "path": path, "status": status,
			"user": user, "duration_ms": dur, "logged_at": at, "client_ip": ip,
		})
	}
	return out, total, rows.Err()
}
