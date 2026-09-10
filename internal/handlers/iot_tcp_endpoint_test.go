package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestSplitPublicAddr_suppliesTheListenerDefaultPort(t *testing.T) {
	host, port := splitPublicAddr("45.112.204.245", defaultSinotrackPort)
	if host != "45.112.204.245" || port != defaultSinotrackPort {
		t.Fatalf("got %s:%s, want 45.112.204.245:%s", host, port, defaultSinotrackPort)
	}
}

func TestSplitPublicAddr_keepsAnExplicitPort(t *testing.T) {
	host, port := splitPublicAddr("gps.example.com:5099", defaultSinotrackPort)
	if host != "gps.example.com" || port != "5099" {
		t.Fatalf("got %s:%s, want gps.example.com:5099", host, port)
	}
}

func TestSplitPublicAddr_bracketedIPv6(t *testing.T) {
	host, port := splitPublicAddr("[2001:db8::1]:5013", defaultSinotrackPort)
	if host != "2001:db8::1" || port != "5013" {
		t.Fatalf("got %s:%s, want 2001:db8::1:5013", host, port)
	}
}

func TestSinotrackSetServerSMS_buildsTheLiteralCommand(t *testing.T) {
	// This is the string an operator types into a phone. A wrong digit here is
	// a device that never connects and reports no error, so it is generated
	// rather than transcribed.
	cmd, warning := sinotrackSetServerSMS("45.112.204.245", "5013")
	if cmd != "8040000 45.112.204.245 5013" {
		t.Fatalf("command %q", cmd)
	}
	if warning != "" {
		t.Fatalf("an IP literal needs no warning, got %q", warning)
	}
}

func TestSinotrackSetServerSMS_warnsOnAHostname(t *testing.T) {
	// Most ST-90x firmwares accept only a literal IP in 804, and a platform TCP
	// proxy hands out a hostname — so a well-formed command silently does
	// nothing. Telling the operator before they text a fleet is the point.
	cmd, warning := sinotrackSetServerSMS("gps.example.com", "5013")
	if cmd != "8040000 gps.example.com 5013" {
		t.Fatalf("command %q", cmd)
	}
	if warning == "" {
		t.Fatal("a hostname must carry the literal-IP warning")
	}
	if !strings.Contains(strings.ToLower(warning), "ip") {
		t.Fatalf("warning should say why: %q", warning)
	}
}

func TestDescribeTCPListener_unsetReportsNoAddress(t *testing.T) {
	out := describeTCPListener("SINOTRACK_PUBLIC_ADDR", "", "HQ", defaultSinotrackPort)

	if out["configured"] != false {
		t.Fatal("an unset listener must not report as configured")
	}
	// The whole point of the HTTP block's honesty, applied here: never hand out
	// an address that cannot be reached, because it looks actionable.
	if _, ok := out["address"]; ok {
		t.Fatal("no address may be advertised when none is configured")
	}
	msg, _ := out["error"].(string)
	if !strings.Contains(msg, "SINOTRACK_PUBLIC_ADDR") {
		t.Fatalf("the error must name the variable to set: %q", msg)
	}
}

func TestDescribeTCPListener_setIsReported(t *testing.T) {
	out := describeTCPListener("SINOTRACK_PUBLIC_ADDR", "45.112.204.245:5013", "HQ", defaultSinotrackPort)

	if out["configured"] != true {
		t.Fatal("a configured listener must report as configured")
	}
	if out["address"] != "45.112.204.245:5013" {
		t.Fatalf("address %v", out["address"])
	}
	if _, ok := out["error"]; ok {
		t.Fatal("a configured listener carries no error")
	}
}

func TestIngestionGuide_tcpBlockCarriesTheSMSWhenConfigured(t *testing.T) {
	gin.SetMode(gin.TestMode)
	t.Setenv("SINOTRACK_PUBLIC_ADDR", "45.112.204.245:5013")

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/iot/ingestion", nil)
	(&IoT{}).ingestionGuide(c)

	var body struct {
		TCP struct {
			Sinotrack struct {
				Configured bool   `json:"configured"`
				Address    string `json:"address"`
				SMS        struct {
					SetServer string `json:"setServer"`
					Warning   string `json:"warning"`
				} `json:"sms"`
			} `json:"sinotrack"`
			Teltonika struct {
				Configured bool `json:"configured"`
			} `json:"teltonika"`
		} `json:"tcp"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v — %s", err, w.Body.String())
	}

	if !body.TCP.Sinotrack.Configured || body.TCP.Sinotrack.Address != "45.112.204.245:5013" {
		t.Fatalf("sinotrack block %+v", body.TCP.Sinotrack)
	}
	if body.TCP.Sinotrack.SMS.SetServer != "8040000 45.112.204.245 5013" {
		t.Fatalf("sms %q", body.TCP.Sinotrack.SMS.SetServer)
	}
	if body.TCP.Sinotrack.SMS.Warning != "" {
		t.Fatalf("an IP needs no warning: %q", body.TCP.Sinotrack.SMS.Warning)
	}
	// One listener being configured must not imply the other is.
	if body.TCP.Teltonika.Configured {
		t.Fatal("teltonika was not configured in this test")
	}
}

func TestIngestionGuide_tcpBlockOffersNoSMSWithoutAnAddress(t *testing.T) {
	gin.SetMode(gin.TestMode)
	t.Setenv("SINOTRACK_PUBLIC_ADDR", "")

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/iot/ingestion", nil)
	(&IoT{}).ingestionGuide(c)

	// A command built from a default port and no host would be a string an
	// operator could text, and it would point nowhere.
	if strings.Contains(w.Body.String(), "8040000") {
		t.Fatalf("no SMS may be offered without an address: %s", w.Body.String())
	}
}
