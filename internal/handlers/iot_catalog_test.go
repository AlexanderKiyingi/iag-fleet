package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestValidateDeviceHardware_acceptsCatalogueHardware(t *testing.T) {
	for _, tc := range []struct{ dtype, brand, model string }{
		{DeviceTypeTracker, "SinoTrack", "ST-901"},
		{DeviceTypeTracker, "Teltonika", "FMB920"},
		{DeviceTypeFuelSensor, "Technoton", "DUT-E"},
	} {
		if err := validateDeviceHardware(tc.dtype, tc.brand, tc.model); err != nil {
			t.Fatalf("%s %s %s: %v", tc.dtype, tc.brand, tc.model, err)
		}
	}
}

func TestValidateDeviceHardware_capitalisationIsNotAFailure(t *testing.T) {
	// An operator typing "sinotrack" has not made a mistake worth an error.
	if err := validateDeviceHardware(DeviceTypeTracker, "sinotrack", "st-901"); err != nil {
		t.Fatalf("case should not matter: %v", err)
	}
}

func TestValidateDeviceHardware_rejectsAModelTheBrandDoesNotHave(t *testing.T) {
	// The typo case, and the whole reason this is a catalogue and not a text box.
	err := validateDeviceHardware(DeviceTypeTracker, "SinoTrack", "FMB920")
	if err == nil {
		t.Fatal("a Teltonika model under SinoTrack must be rejected")
	}
	// The error has to be actionable — an operator needs the alternatives.
	if !strings.Contains(err.Error(), "ST-901") {
		t.Fatalf("error should list the brand's models: %v", err)
	}
}

func TestValidateDeviceHardware_rejectsAnUnknownBrand(t *testing.T) {
	err := validateDeviceHardware(DeviceTypeTracker, "Definitely Not A Brand", "X")
	if err == nil {
		t.Fatal("an unknown brand must be rejected")
	}
	// …and must point at the escape hatch, or the operator is simply stuck.
	if !strings.Contains(err.Error(), BrandOther) {
		t.Fatalf("error should offer %q: %v", BrandOther, err)
	}
}

func TestValidateDeviceHardware_otherAcceptsAnything(t *testing.T) {
	// Hardware with no decoder still needs an inventory row.
	if err := validateDeviceHardware(DeviceTypeTracker, BrandOther, "Some GT06 clone"); err != nil {
		t.Fatalf("Other must accept unknown hardware: %v", err)
	}
}

func TestValidateDeviceHardware_rejectsAFuelProbeCalledATracker(t *testing.T) {
	// A probe registered as a tracker gets provisioned with a serial it can
	// never dial in with, and then reads as a device that is simply offline.
	err := validateDeviceHardware(DeviceTypeTracker, "Technoton", "DUT-E")
	if err == nil {
		t.Fatal("a fuel sensor must not register as a gps_tracker")
	}
	if !strings.Contains(err.Error(), DeviceTypeFuelSensor) {
		t.Fatalf("error should say what it actually is: %v", err)
	}
}

func TestValidateDeviceHardware_rejectsAnUnknownType(t *testing.T) {
	if err := validateDeviceHardware("drone", "SinoTrack", "ST-901"); err == nil {
		t.Fatal("an unknown device type must be rejected")
	}
}

func TestValidateDeviceHardware_emptyStaysAllowed(t *testing.T) {
	// Rows predating migration 0051 genuinely have unknown type and brand, and
	// a PATCH that touches only the label must not fail on their account.
	if err := validateDeviceHardware("", "", ""); err != nil {
		t.Fatalf("unknown hardware must remain recordable: %v", err)
	}
}

func TestCatalogProtocol_isTheHardwaresProtocolNotTheObservedOne(t *testing.T) {
	if got := catalogProtocol("SinoTrack", "ST-901"); got != "hq" {
		t.Fatalf("protocol %q, want hq", got)
	}
	if got := catalogProtocol("Teltonika", "FMB920"); got != "teltonika" {
		t.Fatalf("protocol %q, want teltonika", got)
	}
	// Unknown hardware claims nothing rather than guessing.
	if got := catalogProtocol(BrandOther, "mystery"); got != "" {
		t.Fatalf("protocol %q, want empty", got)
	}
}

func TestDeviceCatalogEndpoint_servesTypesBrandsAndModels(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/iot/catalog", nil)

	// No store: the catalogue is a property of the build, so a deployment
	// without telemetry configured must still be able to render the form.
	(&IoT{}).deviceCatalog(c)

	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	var body struct {
		Types []struct {
			Value string `json:"value"`
			Label string `json:"label"`
		} `json:"types"`
		Brands []struct {
			Brand  string `json:"brand"`
			Models []struct {
				Model             string `json:"model"`
				Type              string `json:"type"`
				Protocol          string `json:"protocol"`
				Listener          string `json:"listener"`
				AttachesToTracker bool   `json:"attachesToTracker"`
			} `json:"models"`
		} `json:"brands"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v — %s", err, w.Body.String())
	}

	if len(body.Types) != 2 {
		t.Fatalf("want gps_tracker and fuel_sensor, got %+v", body.Types)
	}

	brands := map[string]bool{}
	var sawSinoTrack, sawFuelProbe bool
	for _, b := range body.Brands {
		brands[b.Brand] = true
		for _, m := range b.Models {
			if b.Brand == "SinoTrack" && m.Model == "ST-901" {
				sawSinoTrack = true
				if m.Protocol != "hq" || m.Listener != "sinotrack" {
					t.Fatalf("ST-901 should route to the sinotrack listener over hq: %+v", m)
				}
			}
			if m.Type == DeviceTypeFuelSensor && m.AttachesToTracker {
				sawFuelProbe = true
			}
		}
	}
	for _, want := range []string{"SinoTrack", "Teltonika", BrandOther} {
		if !brands[want] {
			t.Fatalf("catalogue is missing brand %q", want)
		}
	}
	if !sawSinoTrack {
		t.Fatal("SinoTrack ST-901 missing from the catalogue")
	}
	if !sawFuelProbe {
		t.Fatal("no fuel probe reported as attaching to a tracker")
	}
}

func TestDeviceCatalogEndpoint_carriesTheListenerEndpoints(t *testing.T) {
	gin.SetMode(gin.TestMode)
	t.Setenv("SINOTRACK_PUBLIC_ADDR", "45.112.204.245:5013")

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/iot/catalog", nil)
	(&IoT{}).deviceCatalog(c)

	// Choosing a model has to be enough to show where to point it; a second
	// round trip to the ingestion guide would be a screen that can render the
	// picker before it can render the answer.
	if !strings.Contains(w.Body.String(), "8040000 45.112.204.245 5013") {
		t.Fatalf("catalogue should carry the listener SMS: %s", w.Body.String())
	}
}
