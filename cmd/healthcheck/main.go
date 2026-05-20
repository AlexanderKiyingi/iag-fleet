// Command healthcheck probes GET /ready for container HEALTHCHECK (distroless-safe).
package main

import (
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/iag/fleet-tool/backend/internal/config"
)

func main() {
	addr := config.ListenAddr()
	host := addr
	if strings.HasPrefix(host, ":") {
		host = "127.0.0.1" + host
	} else if !strings.Contains(host, "://") {
		host = "http://" + host
	}
	url := strings.TrimSuffix(host, "/") + "/ready"

	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		os.Exit(1)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		os.Exit(1)
	}
}
