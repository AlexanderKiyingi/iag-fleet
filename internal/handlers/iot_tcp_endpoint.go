package handlers

// Where a tracker should dial in, and the SMS that puts it there.
//
// The HTTP ingest already has TELEMETRY_INGEST_URL: a configured public address
// the ingestion guide hands an operator, with a deliberate "unset says so" path
// rather than a fallback, because a hostname that resolves nowhere outside the
// private network cannot be reached by a device on a GPRS SIM and produces no
// error anyone sees.
//
// The TCP listeners had no equivalent. The guide could only answer with a
// protocol name and a default port — "SinoTrack / HQ protocol, default :5013" —
// which is not an address, and the comment beside it conceded the point: the
// address "lives on the device, not here". So the one screen that exists to
// tell an operator where to point a tracker could not, and the host survived
// only in whoever configured the TCP proxy.
//
// These variables close that. The shape mirrors the HTTP block exactly: report
// a real address, or report that there is none. Never invent one.
//
//	SINOTRACK_PUBLIC_ADDR=45.112.204.245:5013
//	TELTONIKA_PUBLIC_ADDR=45.112.204.245:5027

import (
	"fmt"
	"net"
	"os"
	"strings"

	"github.com/gin-gonic/gin"
)

const (
	defaultSinotrackPort = "5013"
	defaultTeltonikaPort = "5027"

	// The factory password on every ST-90x. Operators who changed it know they
	// did; operators who did not would otherwise have to look it up.
	sinotrackDefaultPassword = "0000"
)

// splitPublicAddr separates a configured address into host and port, supplying
// the listener's default port when the operator gave only a host.
//
// Bare IPv6 is accepted with brackets ("[2001:db8::1]:5013") because that is
// what net.SplitHostPort understands; an unbracketed IPv6 literal is
// indistinguishable from host:port and is left to fail visibly rather than be
// guessed at.
func splitPublicAddr(addr, defaultPort string) (host, port string) {
	addr = strings.TrimSpace(addr)
	if addr == "" {
		return "", defaultPort
	}
	if h, p, err := net.SplitHostPort(addr); err == nil && p != "" {
		return h, p
	}
	return strings.Trim(addr, "[]"), defaultPort
}

// sinotrackSetServerSMS builds the literal `804` command an operator texts to
// point an ST-90x at this deployment.
//
// This is the string that actually gets typed, and it is where onboarding goes
// wrong: a transposed port digit produces a device that never connects and no
// error anywhere. Generating it removes the transcription.
//
// The warning is not decoration. Most ST-901 firmwares accept only a literal IP
// in `804`, and a platform TCP proxy hands out a *hostname* — so a perfectly
// well-formed command built from a hostname silently does nothing on the
// majority of units. An operator who is told that before texting can test one
// unit; one who is not discovers it as a fleet that never reports.
func sinotrackSetServerSMS(host, port string) (command, warning string) {
	if host == "" {
		return "", ""
	}
	command = fmt.Sprintf("%s%s %s %s", "804", sinotrackDefaultPassword, host, port)
	if net.ParseIP(host) == nil {
		warning = "This address is a hostname. Most ST-90x firmwares accept only a " +
			"literal IP in the 804 command and will ignore a hostname without " +
			"reporting an error — confirm with one unit before programming a fleet, " +
			"or terminate the gateway on a static IP."
	}
	return command, warning
}

// describeTCPListener reports one raw-TCP listener to an operator.
//
// `configured` is the field a caller should branch on; when it is false there is
// an `error` saying which variable to set and no address to copy, for the same
// reason the HTTP block withholds one: an address that cannot be reached is
// worse than no address, because it looks actionable.
func describeTCPListener(envVar, publicAddr, protocol, defaultPort string) gin.H {
	out := gin.H{
		"protocol":    protocol,
		"defaultPort": defaultPort,
		"identifier":  "The id the device transmits must match iot_devices.serial; no bearer token on the wire.",
	}
	host, port := splitPublicAddr(publicAddr, defaultPort)
	if host == "" {
		out["configured"] = false
		out["error"] = envVar + " is not set on this deployment — no public address to " +
			"give out. Set it to the host:port a tracker should dial, which must be a " +
			"raw TCP endpoint (an HTTP route will not work), then restart the service."
		return out
	}
	out["configured"] = true
	out["host"] = host
	out["port"] = port
	out["address"] = net.JoinHostPort(host, port)
	return out
}

// tcpIngestionBlock is the `tcp` half of the ingestion guide.
func tcpIngestionBlock() gin.H {
	sinotrack := describeTCPListener(
		"SINOTRACK_PUBLIC_ADDR",
		os.Getenv("SINOTRACK_PUBLIC_ADDR"),
		"SinoTrack / HQ protocol (ST-901, ST-906, ST-915 and HQ-speaking clones)",
		defaultSinotrackPort,
	)
	if host, _ := sinotrack["host"].(string); host != "" {
		port, _ := sinotrack["port"].(string)
		command, warning := sinotrackSetServerSMS(host, port)
		sms := gin.H{
			"setServer": command,
			"password":  sinotrackDefaultPassword,
			"note": "Text this to the device's SIM. Default password " +
				sinotrackDefaultPassword + "; one real space where shown, no leading +. " +
				"The unit replies SET OK. Set the carrier APN first with 803" +
				sinotrackDefaultPassword + " <apn>.",
		}
		if warning != "" {
			sms["warning"] = warning
		}
		sinotrack["sms"] = sms
	}

	return gin.H{
		"sinotrack": sinotrack,
		"teltonika": describeTCPListener(
			"TELTONIKA_PUBLIC_ADDR",
			os.Getenv("TELTONIKA_PUBLIC_ADDR"),
			"Teltonika Codec 8 / 8E",
			defaultTeltonikaPort,
		),
		"note": "Both listeners speak raw TCP and cannot be reached through the API " +
			"gateway or the web app. Each needs a publicly reachable host:port of its own.",
	}
}
