package handlers

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/iag/fleet-tool/backend/internal/auth"
	"github.com/iag/fleet-tool/backend/internal/store"
)

// Calendar exposes cross-module schedule events (v7 calCollect parity).
type Calendar struct {
	Repo *store.Repository
}

func (cal *Calendar) Register(rg *gin.RouterGroup) {
	rg.GET("/calendar/events", auth.RequireAnyFleetView(), cal.events)
}

type calendarEvent struct {
	Kind      string `json:"kind"` // jmp | svc | cmp | pm | req | cargo | fuel
	Start     string `json:"start"`
	End       string `json:"end"`
	Title     string `json:"title"`
	Subtitle  string `json:"subtitle,omitempty"`
	RefID     string `json:"refId"`
	Row       string `json:"row,omitempty"`
	SingleDay bool   `json:"singleDay,omitempty"`
}

func (cal *Calendar) events(c *gin.Context) {
	fromStr := c.Query("from")
	toStr := c.Query("to")
	if fromStr == "" || toStr == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "from and to query params required (YYYY-MM-DD)"})
		return
	}
	start, err := time.Parse("2006-01-02", fromStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid from date"})
		return
	}
	end, err := time.Parse("2006-01-02", toStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid to date"})
		return
	}
	end = end.Add(24*time.Hour - time.Nanosecond)

	want := func(k string) bool {
		v := c.DefaultQuery(k, "true")
		return v == "1" || strings.EqualFold(v, "true")
	}
	includeJMP := want("jmp")
	includeSvc := want("svc")
	includeCmp := want("cmp")
	includeReq := want("req")
	includeCargo := want("cargo")
	includeFuel := want("fuel")
	includePM := want("pm")

	ctx := c.Request.Context()
	vehicles, _ := cal.Repo.Vehicles.List(ctx)
	drivers, _ := cal.Repo.Drivers.List(ctx)
	vehPlate := map[string]string{}
	for _, v := range vehicles {
		vehPlate[v.ID] = v.Plate
	}
	drvName := map[string]string{}
	for _, d := range drivers {
		drvName[d.ID] = d.Name
	}

	overlaps := func(ds, de time.Time) bool {
		return !de.Before(start) && !ds.After(end)
	}
	parseDay := func(s string) time.Time {
		if t, err := time.Parse("2006-01-02", s); err == nil {
			return t
		}
		if t, err := time.Parse(time.RFC3339, s); err == nil {
			return t
		}
		return time.Time{}
	}

	var out []calendarEvent

	if includeJMP {
		jmps, _ := cal.Repo.JMPs.List(ctx)
		for _, j := range jmps {
			ds := parseDay(j.StartDate)
			de := parseDay(j.ExpectedReturn)
			if de.IsZero() {
				de = ds
			}
			if ds.IsZero() || !overlaps(ds, de) {
				continue
			}
			out = append(out, calendarEvent{
				Kind: "jmp", Start: ds.Format("2006-01-02"), End: de.Format("2006-01-02"),
				Title: j.ID + " · " + trunc(j.Purpose, 40), Subtitle: vehPlate[j.VehicleID],
				RefID: j.ID, Row: vehPlate[j.VehicleID],
			})
		}
	}
	if includeSvc {
		mx, _ := cal.Repo.Maintenance.List(ctx)
		for _, m := range mx {
			d := parseDay(m.Date)
			if d.IsZero() || d.Before(start) || d.After(end) {
				continue
			}
			out = append(out, calendarEvent{
				Kind: "svc", Start: d.Format("2006-01-02"), End: d.Format("2006-01-02"),
				Title: m.Type + " · " + trunc(m.Service, 30), Subtitle: vehPlate[m.VehicleID],
				RefID: m.ID, Row: vehPlate[m.VehicleID], SingleDay: true,
			})
		}
	}
	if includePM {
		schedules, _ := cal.Repo.PMSchedules.List(ctx)
		for _, s := range schedules {
			if !s.Active || s.NextDueDate == "" {
				continue
			}
			d := parseDay(s.NextDueDate)
			if d.IsZero() || d.Before(start) || d.After(end) {
				continue
			}
			out = append(out, calendarEvent{
				Kind: "pm", Start: d.Format("2006-01-02"), End: d.Format("2006-01-02"),
				Title: s.Name + " · " + s.ServiceType, Subtitle: vehPlate[s.VehicleID],
				RefID: s.ID, Row: vehPlate[s.VehicleID], SingleDay: true,
			})
		}
	}
	if includeCmp {
		cmp, _ := cal.Repo.Compliance.List(ctx)
		for _, ci := range cmp {
			if ci.Expiry == "" || ci.Status == "valid" {
				continue
			}
			d := parseDay(ci.Expiry)
			if d.IsZero() || d.Before(start) || d.After(end) {
				continue
			}
			sub := ""
			row := "Compliance"
			if ci.DriverID != "" {
				sub = drvName[ci.DriverID]
				row = sub
			} else if ci.VehicleID != "" {
				sub = vehPlate[ci.VehicleID]
				row = sub
			}
			out = append(out, calendarEvent{
				Kind: "cmp", Start: d.Format("2006-01-02"), End: d.Format("2006-01-02"),
				Title: ci.DocType + " expires", Subtitle: sub,
				RefID: ci.ID, Row: row, SingleDay: true,
			})
		}
	}
	if includeReq {
		reqs, _ := cal.Repo.Requests.List(ctx)
		for _, rq := range reqs {
			ds := parseDay(rq.StartDate)
			de := parseDay(rq.EndDate)
			if de.IsZero() {
				de = ds
			}
			if ds.IsZero() || !overlaps(ds, de) {
				continue
			}
			out = append(out, calendarEvent{
				Kind: "req", Start: ds.Format("2006-01-02"), End: de.Format("2006-01-02"),
				Title: trunc(rq.Purpose, 38), Subtitle: rq.RequesterDept,
				RefID: rq.ID, Row: rq.RequesterDept,
			})
		}
	}
	if includeCargo {
		crg, _ := cal.Repo.Cargo.List(ctx)
		for _, cg := range crg {
			if cg.DepartureMombasa == "" {
				continue
			}
			ds := parseDay(cg.DepartureMombasa)
			de := parseDay(cg.OffloadingDate)
			if de.IsZero() {
				de = parseDay(cg.ArrivalAcp)
			}
			if de.IsZero() {
				de = parseDay(cg.DepartureKampala)
			}
			if de.IsZero() {
				de = parseDay(cg.DepartureMalaba)
			}
			if de.IsZero() {
				de = ds
			}
			if ds.IsZero() || !overlaps(ds, de) {
				continue
			}
			out = append(out, calendarEvent{
				Kind: "cargo", Start: ds.Format("2006-01-02"), End: de.Format("2006-01-02"),
				Title: cg.CargoNature + " · " + cg.TruckPlate, Subtitle: cg.Transporter,
				RefID: cg.ID, Row: cg.TruckPlate,
			})
		}
	}
	if includeFuel {
		fuel, _ := cal.Repo.Fuel.List(ctx)
		for _, f := range fuel {
			d := parseDay(f.Date)
			if d.IsZero() || d.Before(start) || d.After(end) {
				continue
			}
			out = append(out, calendarEvent{
				Kind: "fuel", Start: d.Format("2006-01-02"), End: d.Format("2006-01-02"),
				Title: formatLitres(f.Litres) + "L · " + formatUGX(f.Total),
				Subtitle: vehPlate[f.VehicleID], RefID: f.ID, Row: vehPlate[f.VehicleID], SingleDay: true,
			})
		}
	}

	c.JSON(http.StatusOK, gin.H{"items": out, "from": fromStr, "to": toStr})
}

func trunc(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

func formatLitres(l float64) string {
	return fmt.Sprintf("%.0f", l)
}

func formatUGX(v float64) string {
	return fmt.Sprintf("%.0f UGX", v)
}
