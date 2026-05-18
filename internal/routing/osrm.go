package routing

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// OSRMBaseResponse is the subset of OSRM route/v1 JSON we need (geometries=geojson).
type OSRMBaseResponse struct {
	Code   string `json:"code"`
	Routes []struct {
		Distance float64 `json:"distance"`
		Duration float64 `json:"duration"`
		Geometry struct {
			Type        string          `json:"type"`
			Coordinates json.RawMessage `json:"coordinates"`
		} `json:"geometry"`
	} `json:"routes"`
}

// OSRMRoute is a normalized usable route from OSRM.
type OSRMRoute struct {
	DistanceM    float64
	DurationS    float64
	Coordinates [][2]float64 // GeoJSON order: lng, lat
}

// RequestOSRM calls an OSRM-compatible /route/v1/{profile}/... service.
// points are (lat, lng) in order.
func RequestOSRM(ctx context.Context, baseURL, profile string, points [][2]float64) (*OSRMRoute, error) {
	if len(points) < 2 {
		return nil, fmt.Errorf("need at least 2 points")
	}
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if baseURL == "" {
		return nil, fmt.Errorf("empty OSRM base URL")
	}

	var path strings.Builder
	fmt.Fprintf(&path, "%s/route/v1/%s/", baseURL, profile)
	for i, p := range points {
		if i > 0 {
			path.WriteString(";")
		}
		// OSRM path order: lon,lat
		fmt.Fprintf(&path, "%.7f,%.7f", p[1], p[0])
	}
	path.WriteString("?overview=full&geometries=geojson&steps=false")

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, path.String(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "iag-fleet-tool-api/1.0")

	client := &http.Client{Timeout: 20 * time.Second}
	res, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	body, err := io.ReadAll(io.LimitReader(res.Body, 1<<22))
	if err != nil {
		return nil, err
	}
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("OSRM HTTP %d: %s", res.StatusCode, string(body[:trimErr(len(body), 512)]))
	}

	var parsed OSRMBaseResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("osrm json: %w", err)
	}
	if parsed.Code != "Ok" || len(parsed.Routes) == 0 {
		return nil, fmt.Errorf("osrm code %q or no routes", parsed.Code)
	}

	r0 := parsed.Routes[0]
	// Decode coordinates — LineString is [[lng,lat],...]
	var ring [][]float64
	if err := json.Unmarshal(r0.Geometry.Coordinates, &ring); err != nil {
		return nil, fmt.Errorf("geometry: %w", err)
	}
	coords := make([][2]float64, 0, len(ring))
	for _, c := range ring {
		if len(c) < 2 {
			continue
		}
		coords = append(coords, [2]float64{c[0], c[1]})
	}
	if len(coords) < 2 {
		return nil, fmt.Errorf("degenerate geometry")
	}

	return &OSRMRoute{
		DistanceM:    r0.Distance,
		DurationS:    r0.Duration,
		Coordinates: coords,
	}, nil
}

func trimErr(n, cap int) int {
	if n < cap {
		return n
	}
	return cap
}
