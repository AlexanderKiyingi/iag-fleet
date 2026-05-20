package config

import (
	"os"
	"strings"
)

// ListenAddr returns the HTTP bind address.
// PORT wins (Railway and other PaaS set this for healthchecks), then ADDR / HTTP_PORT.
func ListenAddr() string {
	if p := strings.TrimSpace(os.Getenv("PORT")); p != "" {
		return normalizeListenAddr(p)
	}
	if v := strings.TrimSpace(os.Getenv("ADDR")); v != "" {
		return normalizeListenAddr(v)
	}
	if v := strings.TrimSpace(os.Getenv("HTTP_PORT")); v != "" {
		return normalizeListenAddr(v)
	}
	return ":4008"
}

func normalizeListenAddr(addr string) string {
	if !strings.HasPrefix(addr, ":") && !strings.Contains(addr, ":") {
		return ":" + addr
	}
	return addr
}
