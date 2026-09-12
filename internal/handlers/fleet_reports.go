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
	rg.GET("/reports/driver-behaviour", auth.RequireAnyFleetView(), r.driverBehaviour)
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

// Driver behaviour thresholds.
//
// GPS speed is noisy — a fix every ten seconds is a sample, not a
// speedometer — so these sit deliberately above the values a vehicle-bus
// telematics unit would use. The cost of a threshold set too low is not a
// slightly wrong number: it is a scorecard that flags a careful driver, gets
// argued with once, and is never trusted again.
const (
	// Sustained speed above this counts as speeding. Uganda's national limit
	// for goods vehicles is 80 km/h; this is deliberately the legal figure
	// rather than a tuned one, so the report says something defensible.
	reportSpeedingKmh = 80.0
	// Change in speed per second treated as harsh. ~10 km/h/s is about 2.8
	// m/s², firmly outside normal driving and beyond the range GPS jitter
	// produces between two fixes.
	reportHarshKmhPerSec = 10.0
	// Consecutive fixes further apart than this say nothing about
	// acceleration: the vehicle could have done anything in between.
	reportHarshMaxGapSec = 30
	// Night driving window, local time. Fatigue risk and, for a goods fleet,
	// often a policy breach in itself.
	reportNightFromHour = 22
	reportNightToHour   = 5
)

type driverScoreRow struct {
	VehicleID   string  `json:"vehicleId"`
	Plate       string  `json:"plate"`
	DriverID    string  `json:"driverId"`
	DriverName  string  `json:"driverName"`
	DistanceKm  float64 `json:"distanceKm"`
	MaxSpeedKmh float64 `json:"maxSpeedKmh"`
	// Fixes recorded above the speed limit, and the share of moving fixes they
	// represent — a count alone punishes whoever drove furthest.
	SpeedingFixes int     `json:"speedingFixes"`
	SpeedingPct   float64 `json:"speedingPct"`
	HarshBraking  int     `json:"harshBraking"`
	HarshAccel    int     `json:"harshAccel"`
	NightHours    float64 `json:"nightHours"`
	// EventsPer100Km is what makes drivers comparable. Raw counts rank the
	// busiest driver worst no matter how they drove.
	EventsPer100Km float64 `json:"eventsPer100Km"`
}

// driverBehaviour scores how each vehicle was driven.
//
// Scored per vehicle and attributed to its currently assigned driver, which is
// the honest limit of what the data supports: pings carry a vehicle, not a
// person, and the vehicle→driver binding is current rather than historical. A
// vehicle that changed hands mid-window attributes the whole window to
// whoever holds it now, and the response says so rather than leaving the
// caller to assume otherwise.
func (r *Reports) driverBehaviour(c *gin.Context) {
	ctx := c.Request.Context()
	from, to, days := reportWindow(c, 7, 90)

	pool := r.Repo.FuelEventsPool()
	if pool == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "telemetry is not configured on this deployment"})
		return
	}

	const q = `
WITH steps AS (
    SELECT vehicle_id, ts, lat, lng, COALESCE(speed_kmh, 0) AS speed,
           ts  - lag(ts)  OVER w AS gap,
           lat - lag(lat) OVER w AS dlat,
           lng - lag(lng) OVER w AS dlng,
           lag(lat) OVER w        AS plat,
           COALESCE(speed_kmh,0) - lag(COALESCE(speed_kmh,0)) OVER w AS dspeed
      FROM telemetry_timeseries
     WHERE ts BETWEEN $1 AND $2
       AND NOT (lat = 0 AND lng = 0)
    WINDOW w AS (PARTITION BY vehicle_id ORDER BY ts)
),
scored AS (
    SELECT vehicle_id, speed,
           CASE WHEN gap IS NULL OR gap > make_interval(mins => $3) THEN 0
                ELSE 111.32 * sqrt((dlat)^2 + (dlng * cos(radians((lat + plat)/2)))^2)
           END AS hop_km,
           (speed >= $4) AS speeding,
           (speed >= $5) AS moving,
           -- Acceleration only means something between two fixes close enough
           -- together to describe the same manoeuvre.
           CASE WHEN gap IS NOT NULL
                 AND EXTRACT(EPOCH FROM gap) BETWEEN 1 AND $6
                 AND dspeed / EXTRACT(EPOCH FROM gap) <= -$7 THEN 1 ELSE 0 END AS harsh_brake,
           CASE WHEN gap IS NOT NULL
                 AND EXTRACT(EPOCH FROM gap) BETWEEN 1 AND $6
                 AND dspeed / EXTRACT(EPOCH FROM gap) >= $7 THEN 1 ELSE 0 END AS harsh_accel,
           CASE WHEN gap IS NULL OR gap > make_interval(mins => $3) THEN 0
                WHEN EXTRACT(HOUR FROM ts) >= $8 OR EXTRACT(HOUR FROM ts) < $9
                     THEN EXTRACT(EPOCH FROM gap)
                ELSE 0 END AS night_s
      FROM steps
)
SELECT v.id::text, COALESCE(v.plate,''), COALESCE(v.driver_id::text,''), COALESCE(d.name,''),
       COALESCE(SUM(s.hop_km), 0),
       COALESCE(MAX(s.speed), 0),
       COALESCE(SUM(CASE WHEN s.speeding THEN 1 ELSE 0 END), 0),
       COALESCE(SUM(CASE WHEN s.moving THEN 1 ELSE 0 END), 0),
       COALESCE(SUM(s.harsh_brake), 0),
       COALESCE(SUM(s.harsh_accel), 0),
       COALESCE(SUM(s.night_s), 0) / 3600.0
  FROM vehicles v
  LEFT JOIN scored s ON s.vehicle_id = v.id::text
  LEFT JOIN drivers d ON d.id = v.driver_id
 GROUP BY v.id, v.plate, v.driver_id, d.name
 ORDER BY COALESCE(SUM(s.hop_km), 0) DESC, v.plate ASC`

	rows, err := pool.Query(ctx, q,
		from, to, reportMaxGapMinutes,
		reportSpeedingKmh, reportMovingKmh,
		reportHarshMaxGapSec, reportHarshKmhPerSec,
		reportNightFromHour, reportNightToHour)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	defer rows.Close()

	out := []driverScoreRow{}
	for rows.Next() {
		var row driverScoreRow
		var movingFixes int
		if err := rows.Scan(&row.VehicleID, &row.Plate, &row.DriverID, &row.DriverName,
			&row.DistanceKm, &row.MaxSpeedKmh, &row.SpeedingFixes, &movingFixes,
			&row.HarshBraking, &row.HarshAccel, &row.NightHours); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		if movingFixes > 0 {
			row.SpeedingPct = float64(row.SpeedingFixes) / float64(movingFixes) * 100
		}
		// Rate, not count. Below a few kilometres the ratio is noise, so it is
		// left at zero rather than reporting an enormous number from one event
		// on a short trip.
		if row.DistanceKm >= 5 {
			row.EventsPer100Km = float64(row.HarshBraking+row.HarshAccel) / row.DistanceKm * 100
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
		"thresholds": gin.H{
			"speedingKmh":    reportSpeedingKmh,
			"harshKmhPerSec": reportHarshKmhPerSec,
			"nightFromHour":  reportNightFromHour,
			"nightToHour":    reportNightToHour,
		},
		// Said in the payload, not just in a comment: pings carry a vehicle,
		// not a person, and the binding is current rather than historical.
		"attribution": "Scored per vehicle and attributed to its currently assigned driver. A vehicle that changed driver during the window attributes the whole window to the current one.",
		"vehicles":    out,
	})
}
