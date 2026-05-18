// Command iot-gateway is the native Teltonika Codec 8 / 8E TCP listener.
//
// It runs as its own process (or sidecar) so the HTTP API can be scaled
// independently. Devices connect on :5027 (configurable via IOT_ADDR),
// announce their IMEI, and stream AVL packets. We parse them, persist
// each record into telemetry_pings, sync the vehicle's hot-state row,
// and ACK the device with the record count it expects.
//
// Authentication: by IMEI/serial against iot_devices.serial. A device
// not in the registry is rejected at the handshake.
//
// Usage:
//   DATABASE_URL=postgres://... IOT_ADDR=:5027 go run ./cmd/iot-gateway
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"strconv"
	"sync"
	"syscall"
	"time"

	"github.com/iag/fleet-tool/backend/internal/db"
	"github.com/iag/fleet-tool/backend/internal/iot"
)

func main() {
	configureLogger()

	addr := os.Getenv("IOT_ADDR")
	if addr == "" {
		addr = ":5027"
	}

	connectCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	pool, err := db.Connect(connectCtx, "")
	cancel()
	if err != nil {
		slog.Error("connect Postgres", "err", err)
		os.Exit(1)
	}
	defer pool.Close()

	store := iot.NewStore(pool)

	listener, err := net.Listen("tcp", addr)
	if err != nil {
		slog.Error("listen failed", "addr", addr, "err", err)
		os.Exit(1)
	}
	slog.Info("iot-gateway listening", "addr", addr)

	srv := &gateway{store: store}

	// Run accept loop in its own goroutine; main waits for shutdown.
	go srv.serve(listener)

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	sig := <-stop
	slog.Info("shutdown signal received", "signal", sig.String())

	// Closing the listener interrupts Accept() with net.ErrClosed. Open
	// connections continue until they finish their current AVL packet or
	// hit the per-connection read deadline, then drop cleanly.
	_ = listener.Close()

	done := make(chan struct{})
	go func() {
		srv.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		slog.Info("graceful shutdown complete")
	case <-time.After(30 * time.Second):
		slog.Warn("shutdown timeout reached; some connections were force-closed")
	}
}

func configureLogger() {
	var h slog.Handler
	if os.Getenv("LOG_FORMAT") == "json" {
		h = slog.NewJSONHandler(os.Stderr, nil)
	} else {
		h = slog.NewTextHandler(os.Stderr, nil)
	}
	slog.SetDefault(slog.New(h))
}

type gateway struct {
	store *iot.Store
	wg    sync.WaitGroup
}

func (g *gateway) serve(l net.Listener) {
	for {
		conn, err := l.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return
			}
			slog.Error("accept failed", "err", err)
			continue
		}
		g.wg.Add(1)
		go func() {
			defer g.wg.Done()
			g.handle(conn)
		}()
	}
}

// handle runs the per-connection state machine: handshake, then loop on
// AVL packets until the device hangs up.
func (g *gateway) handle(conn net.Conn) {
	defer conn.Close()

	remote := conn.RemoteAddr().String()
	_ = conn.SetReadDeadline(time.Now().Add(30 * time.Second))

	r := bufio.NewReader(conn)
	imei, err := iot.ReadHandshake(r)
	if err != nil {
		slog.Warn("handshake read failed", "remote", remote, "err", err)
		return
	}

	logger := slog.With("remote", remote, "imei", imei)

	ctx := context.Background()
	device, err := g.store.FindBySerial(ctx, imei)
	if err != nil {
		logger.Warn("handshake rejected", "err", err)
		_ = iot.WriteHandshakeResponse(conn, false)
		return
	}
	if err := iot.WriteHandshakeResponse(conn, true); err != nil {
		logger.Warn("handshake reply failed", "err", err)
		return
	}
	_ = g.store.MarkSeen(ctx, device.ID, ipOnly(remote))
	logger.Info("device connected", "deviceId", device.ID, "vehicleId", device.VehicleID)

	for {
		// Each AVL packet must arrive within the read window. Active
		// devices typically send every 30-60s; the 5-minute deadline
		// below catches dead sockets without false positives.
		_ = conn.SetReadDeadline(time.Now().Add(5 * time.Minute))
		codec, records, err := iot.ReadAVLPacket(r)
		if err != nil {
			if !errors.Is(err, io.EOF) {
				logger.Warn("packet read failed", "err", err)
			}
			return
		}

		pings := make([]iot.Ping, 0, len(records))
		for _, rec := range records {
			pings = append(pings, recordToPing(rec, device))
		}
		n, err := g.store.InsertPings(ctx, pings)
		if err != nil {
			logger.Error("insert pings failed", "err", err)
			return
		}
		if device.VehicleID != "" && len(pings) > 0 {
			newest := pings[0]
			for _, p := range pings[1:] {
				if p.TS.After(newest.TS) {
					newest = p
				}
			}
			_ = g.store.SyncVehicleFromPing(ctx, newest)
		}
		_ = g.store.MarkSeen(ctx, device.ID, ipOnly(remote))
		if err := iot.WriteACK(conn, len(records)); err != nil {
			logger.Warn("ack failed", "err", err)
			return
		}
		logger.Info("pings persisted", "count", len(records), "codec", codec, "rows", n)
	}
}

// recordToPing translates a Codec 8 AVL record into our storage shape.
// IO IDs of interest are extracted into typed columns; the rest of the
// IO map flows through `raw` JSONB so future analytics can reach back
// for any field (CAN frames, driver-id reads, accelerometer, ...).
//
// Common Teltonika IO IDs (FMC650 / FMB920 / FMC130 — same defaults):
//   239: Ignition (0/1)
//   240: Movement (0/1)
//   80:  Data Mode
//   199: Total odometer (m, uint32)
//   89:  Fuel level (% × 10) — SaleOEM, varies; FuelMonitoring is 9 (analog)
//   9:   Analog input 1 — often wired to a fuel sender
//
// Adjust the map below if your fleet's firmware exposes fuel on a
// different ID.
const (
	ioIDIgnition  uint16 = 239
	ioIDOdoMeters uint16 = 199
	ioIDFuelPct   uint16 = 89  // % × 10
	ioIDFuelAnalog uint16 = 9  // mV; needs per-vehicle calibration to convert
)

func recordToPing(rec iot.AVLRecord, device *iot.Device) iot.Ping {
	devID := device.ID
	alt := float64(rec.Altitude)
	heading := float64(rec.Angle)
	sats := int(rec.Satellites)

	p := iot.Ping{
		VehicleID:  device.VehicleID,
		DeviceID:   &devID,
		TS:         rec.Timestamp,
		Lat:        rec.Latitude,
		Lng:        rec.Longitude,
		Altitude:   &alt,
		Heading:    &heading,
		Satellites: &sats,
	}
	// Speed: 0xFFFF means "unknown" per Codec 8 spec. 0 km/h is a valid
	// stopped reading and is preserved.
	if rec.Speed != 0xFFFF {
		sp := float64(rec.Speed)
		p.SpeedKmh = &sp
	}
	if v, ok := rec.IOs[ioIDOdoMeters]; ok {
		odoKm := float64(v) / 1000.0
		p.Odo = &odoKm
	}
	if v, ok := rec.IOs[ioIDFuelPct]; ok {
		pct := float64(v) / 10.0
		p.FuelLevel = &pct
	}
	if v, ok := rec.IOs[ioIDIgnition]; ok {
		on := v != 0
		p.Ignition = &on
	}
	if rec.EventIOID != 0 {
		ev := int(rec.EventIOID)
		p.EventID = &ev
	}
	p.Raw = encodeIOMap(rec.IOs)
	return p
}

// encodeIOMap turns the device's IO map into JSONB for the `raw` column.
// Keys are stringified IO IDs (so consumers don't have to know which IDs
// fall in which Codec 8 size class).
func encodeIOMap(m map[uint16]int64) []byte {
	if len(m) == 0 {
		return []byte(`{}`)
	}
	out := make(map[string]int64, len(m))
	for k, v := range m {
		out[strconv.Itoa(int(k))] = v
	}
	b, err := json.Marshal(out)
	if err != nil {
		return []byte(`{}`)
	}
	return b
}

func ipOnly(remote string) string {
	host, _, err := net.SplitHostPort(remote)
	if err != nil {
		return remote
	}
	return host
}
