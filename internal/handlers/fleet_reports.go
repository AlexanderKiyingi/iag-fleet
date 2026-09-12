package handlers

import (
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/iag/fleet-tool/backend/internal/auth"
)

// Fleet reports derived from raw telemetry.
//
// Raw, not telemetry_daily, deliberately: the rollup table is empty in
// production because the maintenance worker was never deployed, so a report
// reading it would render blank and look like a fleet that does nothing. These
// read telemetry_timeseries directly and stay correct whether or not the
// aggregator is running.
//
// The thresholds are the ones the map trail already uses, and that matters more
// than the particular numbers: a vehicle drawn as stopped on the map and
// counted as moving in a report is worse than either choice on its own.
const (
	// Below this a vehicle is stationary. Not zero — a parked unit still
	// reports a km/h or two of GPS jitter.
	reportMovingKmh = 3.0
	// Longer than this between fixes and the time is UNOBSERVED rather than
	// moving or stopped. Counting a silent stretch as idle would credit a
	// vehicle with hours it may have spent driving, which is how a utilisation
	// report quietly becomes fiction.
	reportMaxGapMinutes = 10
)

func (r *Reports) registerFleetReports(rg *gin.RouterGroup) {
	rg.GET("/reports/telemetry-coverage", auth.RequireAnyFleetView(), r.telemetryCoverage)
	rg.GET("/reports/utilisation", auth.RequireAnyFleetView(), r.utilisation)
}

// reportWindow reads ?days= with a sane bound.
func reportWindow(c *gin.Context, def, max int) (time.Time, time.Time, int) {
	days := def
	if v := c.Query("days"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			days = n
		}
	}
	if days > max {
		days = max
	}
	to := time.Now().UTC()
	return to.AddDate(0, 0, -days), to, days
}

type coverageRow struct {
	VehicleID   string   `json:"vehicleId"`
	Plate       string   `json:"plate"`
	Pings       int64    `json:"pings"`
	FirstFix    *string  `json:"firstFix"`
	LastFix     *string  `json:"lastFix"`
	QuietHours  *float64 `json:"quietHours"`
	LongestGapH *float64 `json:"longestGapHours"`
	// ObservedPct is the share of the window in which fixes were arriving at
	// all — the honest ceiling on how much any other report about this vehicle
	// can be trusted.
	ObservedPct float64 `json:"observedPct"`
	State       string  `json:"state"`
}

// telemetryCoverage answers "which of the fleet can we actually see?".
//
// It exists because the answer is currently "one of thirty-seven", and because
// every other fleet report is quietly conditional on it. A utilisation report
// showing 36 vehicles at zero hours reads as an idle fleet; the truth is an
// unobserved one, and those call for completely different actions.
func (r *Reports) telemetryCoverage(c *gin.Context) {
	ctx := c.Request.Context()
	from, to, days := reportWindow(c, 7, 90)

	pool := r.Repo.FuelEventsPool()
	if pool == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "telemetry is not configured on this deployment"})
		return
	}

	const q = `
WITH pings AS (
    SELECT vehicle_id, ts,
           ts - lag(ts) OVER (PARTITION BY vehicle_id ORDER BY ts) AS gap
      FROM telemetry_timeseries
     WHERE ts BETWEEN $1 AND $2
),
agg AS (
    SELECT vehicle_id,
           count(*)                                    AS pings,
           min(ts)                                     AS first_fix,
           max(ts)                                     AS last_fix,
           EXTRACT(EPOCH FROM max(gap)) / 3600.0       AS longest_gap_h,
           -- Observed time: every inter-fix interval short enough to count as
           -- continuous reporting. Longer ones are silence, not coverage.
           SUM(CASE WHEN gap <= make_interval(mins => $3) THEN EXTRACT(EPOCH FROM gap) ELSE 0 END)
                                                       AS observed_seconds
      FROM pings
     GROUP BY vehicle_id
)
SELECT v.id::text, COALESCE(v.plate,''),
       COALESCE(a.pings, 0),
       a.first_fix, a.last_fix, a.longest_gap_h,
       COALESCE(a.observed_seconds, 0)
  FROM vehicles v
  LEFT JOIN agg a ON a.vehicle_id = v.id::text
 ORDER BY COALESCE(a.pings, 0) ASC, v.plate ASC`

	rows, err := pool.Query(ctx, q, from, to, reportMaxGapMinutes)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	defer rows.Close()

	windowSeconds := to.Sub(from).Seconds()
	out := []coverageRow{}
	var reporting, silent, never int

	for rows.Next() {
		var row coverageRow
		var first, last *time.Time
		var gapH *float64
		var observed float64
		if err := rows.Scan(&row.VehicleID, &row.Plate, &row.Pings, &first, &last, &gapH, &observed); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		row.LongestGapH = gapH
		if first != nil {
			s := first.UTC().Format(time.RFC3339)
			row.FirstFix = &s
		}
		if last != nil {
			s := last.UTC().Format(time.RFC3339)
			row.LastFix = &s
			q := time.Since(*last).Hours()
			row.QuietHours = &q
		}
		if windowSeconds > 0 {
			row.ObservedPct = observed / windowSeconds * 100
			if row.ObservedPct > 100 {
				row.ObservedPct = 100
			}
		}
		// Three states, because they call for three different actions: chase
		// the vehicle, chase the SIM, or check the serial was ever right.
		switch {
		case row.Pings == 0:
			row.State = "never-reported"
			never++
		case row.QuietHours != nil && *row.QuietHours > 24:
			row.State = "silent"
			silent++
		default:
			row.State = "reporting"
			reporting++
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"generatedAt": time.Now().UTC().Format(time.RFC3339),
		"windowDays":  days,
		"windowStart": from.Format(time.RFC3339),
		"windowEnd":   to.Format(time.RFC3339),
		"vehicles":    out,
		"summary": gin.H{
			"total":         len(out),
			"reporting":     reporting,
			"silent":        silent,
			"neverReported": never,
		},
	})
}

type utilisationRow struct {
	VehicleID    string  `json:"vehicleId"`
	Plate        string  `json:"plate"`
	DistanceKm   float64 `json:"distanceKm"`
	MovingHours  float64 `json:"movingHours"`
	StoppedHours float64 `json:"stoppedHours"`
	// UnobservedHours is the rest of the window. Named rather than folded into
	// "stopped", because a vehicle that was not reporting is not a vehicle that
	// was parked.
	UnobservedHours float64 `json:"unobservedHours"`
	MaxSpeedKmh     float64 `json:"maxSpeedKmh"`
	ActiveDays      int     `json:"activeDays"`
}

// utilisation answers "what did each vehicle actually do?".
//
// Distance is the sum of great-circle hops between consecutive fixes, which
// under-reads a winding road and is the honest figure available from point
// samples — it is not an odometer and the client should not present it as one.
// Hops across a reporting gap are excluded rather than counted: a vehicle that
// went quiet in Kampala and reappeared in Jinja did not teleport, and adding
// that straight line would inflate the distance with a journey nobody observed.
func (r *Reports) utilisation(c *gin.Context) {
	ctx := c.Request.Context()
	from, to, days := reportWindow(c, 7, 90)

	pool := r.Repo.FuelEventsPool()
	if pool == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "telemetry is not configured on this deployment"})
		return
	}

	const q = `
WITH steps AS (
    SELECT vehicle_id, ts, lat, lng, speed_kmh,
           ts   - lag(ts)   OVER w AS gap,
           lat  - lag(lat)  OVER w AS dlat,
           lng  - lag(lng)  OVER w AS dlng,
           lag(lat) OVER w         AS plat
      FROM telemetry_timeseries
     WHERE ts BETWEEN $1 AND $2
       AND NOT (lat = 0 AND lng = 0)
    WINDOW w AS (PARTITION BY vehicle_id ORDER BY ts)
),
scored AS (
    SELECT vehicle_id, ts, speed_kmh, gap,
           -- Equirectangular approximation: exact enough over a sub-10-minute
           -- hop and far cheaper than haversine per row. cos(lat) is what keeps
           -- a degree of longitude honest away from the equator.
           CASE WHEN gap IS NULL OR gap > make_interval(mins => $3) THEN 0
                ELSE 111.32 * sqrt(
                       (dlat)^2 +
                       (dlng * cos(radians((lat + plat) / 2)))^2)
           END AS hop_km,
           CASE WHEN gap IS NULL OR gap > make_interval(mins => $3) THEN 0
                ELSE EXTRACT(EPOCH FROM gap) END AS observed_s,
           CASE WHEN gap IS NULL OR gap > make_interval(mins => $3) THEN 0
                WHEN COALESCE(speed_kmh, 0) >= $4 THEN EXTRACT(EPOCH FROM gap)
                ELSE 0 END AS moving_s
      FROM steps
)
SELECT v.id::text, COALESCE(v.plate,''),
       COALESCE(SUM(s.hop_km), 0),
       COALESCE(SUM(s.moving_s), 0) / 3600.0,
       COALESCE(SUM(s.observed_s), 0) / 3600.0,
       COALESCE(MAX(s.speed_kmh), 0),
       COUNT(DISTINCT date_trunc('day', s.ts))
  FROM vehicles v
  LEFT JOIN scored s ON s.vehicle_id = v.id::text
 GROUP BY v.id, v.plate
 ORDER BY COALESCE(SUM(s.hop_km), 0) DESC, v.plate ASC`

	rows, err := pool.Query(ctx, q, from, to, reportMaxGapMinutes, reportMovingKmh)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	defer rows.Close()

	windowHours := to.Sub(from).Hours()
	out := []utilisationRow{}
	var totalKm, totalMoving float64

	for rows.Next() {
		var row utilisationRow
		var observedHours float64
		if err := rows.Scan(&row.VehicleID, &row.Plate, &row.DistanceKm,
			&row.MovingHours, &observedHours, &row.MaxSpeedKmh, &row.ActiveDays); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		row.StoppedHours = observedHours - row.MovingHours
		if row.StoppedHours < 0 {
			row.StoppedHours = 0
		}
		row.UnobservedHours = windowHours - observedHours
		if row.UnobservedHours < 0 {
			row.UnobservedHours = 0
		}
		totalKm += row.DistanceKm
		totalMoving += row.MovingHours
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"generatedAt":  time.Now().UTC().Format(time.RFC3339),
		"windowDays":   days,
		"windowStart":  from.Format(time.RFC3339),
		"windowEnd":    to.Format(time.RFC3339),
		"movingKmh":    reportMovingKmh,
		"maxGapMins":   reportMaxGapMinutes,
		"vehicles":     out,
		"totalKm":      totalKm,
		"totalMovingH": totalMoving,
	})
}
