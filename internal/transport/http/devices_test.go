package http

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/firemanx07/slay-push/internal/store/postgres"
)

// newDeviceRequest builds a POST /api/v1/devices request with projectID
// already injected into the context — these tests call the handler
// directly, bypassing the router and requireScope entirely.
func newDeviceRequest(ctx context.Context, projectID uuid.UUID, body string) *http.Request {
	return httptest.NewRequestWithContext(contextWithProjectID(ctx, projectID), http.MethodPost, "/api/v1/devices", strings.NewReader(body))
}

func TestHandleRegisterDevice_Validation(t *testing.T) {
	h := newTestHarness(t)
	projectID := postgres.UUIDTo(h.project.ID)

	tests := []struct {
		name string
		body string
	}{
		{"missing token", `{"provider":"expo","platform":"android"}`},
		{"unknown provider", `{"token":"tok","provider":"not-a-real-provider","platform":"android"}`},
		{"unknown platform", `{"token":"tok","provider":"expo","platform":"toaster"}`},
		{"token too long", `{"token":"` + strings.Repeat("a", maxTokenLength+1) + `","provider":"expo","platform":"android"}`},
		{"external_user_id too long", `{"token":"tok","provider":"expo","platform":"android","external_user_id":"` + strings.Repeat("a", maxExternalUserIDLength+1) + `"}`},
		{"device.brand too long", `{"token":"tok","provider":"expo","platform":"android","device":{"brand":"` + strings.Repeat("a", maxDeviceInfoFieldLength+1) + `"}}`},
		{"device.language too long", `{"token":"tok","provider":"expo","platform":"android","device":{"language":"` + strings.Repeat("a", maxLanguageLength+1) + `"}}`},
		{"coords.lat out of range", `{"token":"tok","provider":"expo","platform":"android","device":{"coords":{"lat":91,"lon":0}}}`},
		{"coords.lon out of range", `{"token":"tok","provider":"expo","platform":"android","device":{"coords":{"lat":0,"lon":181}}}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := newDeviceRequest(context.Background(), projectID, tt.body)
			w := httptest.NewRecorder()
			h.server.handleRegisterDevice(w, req)
			if w.Code != http.StatusUnprocessableEntity {
				t.Errorf("status = %d, want %d; body: %s", w.Code, http.StatusUnprocessableEntity, w.Body.String())
			}
		})
	}
}

func TestHandleRegisterDevice_Success(t *testing.T) {
	h := newTestHarness(t)
	projectID := postgres.UUIDTo(h.project.ID)

	body := `{"token":"tok-` + uuid.NewString() + `","provider":"expo","platform":"android"}`
	req := newDeviceRequest(context.Background(), projectID, body)
	w := httptest.NewRecorder()
	h.server.handleRegisterDevice(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body: %s", w.Code, http.StatusOK, w.Body.String())
	}
	var resp registerDeviceResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	deviceID, err := uuid.Parse(resp.ID)
	if err != nil {
		t.Fatalf("response id %q is not a valid UUID: %v", resp.ID, err)
	}

	devices, err := h.queries.GetDevicesByIDs(context.Background(), postgres.GetDevicesByIDsParams{
		ProjectID: postgres.UUIDFrom(projectID),
		Ids:       postgres.UUIDsFrom([]uuid.UUID{deviceID}),
	})
	if err != nil {
		t.Fatalf("GetDevicesByIDs: %v", err)
	}
	if len(devices) != 1 {
		t.Fatalf("GetDevicesByIDs returned %d rows, want 1", len(devices))
	}
	if devices[0].Status != "active" {
		t.Errorf("device status = %q, want %q", devices[0].Status, "active")
	}
}

func TestHandleRegisterDevice_RejectsUnknownField(t *testing.T) {
	h := newTestHarness(t)
	projectID := postgres.UUIDTo(h.project.ID)

	req := newDeviceRequest(context.Background(), projectID, `{"token":"tok","provider":"expo","platform":"android","totally_unexpected":true}`)
	w := httptest.NewRecorder()
	h.server.handleRegisterDevice(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

// TestHandleRegisterDevice_Rotation exercises registerDeviceSerialized (the
// external_user_id + device.device_uuid path): registering the same
// physical device twice under a new token must supersede the first device
// row rather than leaving two active rows. Uses newLiveHarness — this path
// commits its own transaction directly against s.Pool, independent of
// newTestHarness's rollback-tx (see harness_test.go).
func TestHandleRegisterDevice_Rotation(t *testing.T) {
	h := newLiveHarness(t)
	projectID := postgres.UUIDTo(h.project.ID)
	externalUserID := "user-" + uuid.NewString()
	deviceUUID := uuid.NewString()

	register := func(token string) uuid.UUID {
		t.Helper()
		body := `{"token":"` + token + `","provider":"expo","platform":"android","external_user_id":"` + externalUserID + `","device":{"device_uuid":"` + deviceUUID + `"}}`
		req := newDeviceRequest(context.Background(), projectID, body)
		w := httptest.NewRecorder()
		h.server.handleRegisterDevice(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d; body: %s", w.Code, http.StatusOK, w.Body.String())
		}
		var resp registerDeviceResponse
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatalf("decode response: %v", err)
		}
		id, err := uuid.Parse(resp.ID)
		if err != nil {
			t.Fatalf("response id %q is not a valid UUID: %v", resp.ID, err)
		}
		return id
	}

	first := register("tok-" + uuid.NewString())
	second := register("tok-" + uuid.NewString())

	devices, err := h.queries.GetDevicesByIDs(context.Background(), postgres.GetDevicesByIDsParams{
		ProjectID: postgres.UUIDFrom(projectID),
		Ids:       postgres.UUIDsFrom([]uuid.UUID{first, second}),
	})
	if err != nil {
		t.Fatalf("GetDevicesByIDs: %v", err)
	}
	statuses := map[uuid.UUID]string{}
	for _, d := range devices {
		statuses[postgres.UUIDTo(d.ID)] = d.Status
	}
	if statuses[first] != "stale" {
		t.Errorf("first device status = %q, want %q (superseded by rotation)", statuses[first], "stale")
	}
	if statuses[second] != "active" {
		t.Errorf("second device status = %q, want %q", statuses[second], "active")
	}
}

func TestHandleRegisterDevice_MissingProject(t *testing.T) {
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/v1/devices", bytes.NewReader([]byte(`{"token":"tok","provider":"expo","platform":"android"}`)))
	w := httptest.NewRecorder()
	(&Server{}).handleRegisterDevice(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want %d (no project in context)", w.Code, http.StatusInternalServerError)
	}
}

func TestClientIP(t *testing.T) {
	t.Run("prefers X-Forwarded-For's first entry", func(t *testing.T) {
		req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/", nil)
		req.Header.Set("X-Forwarded-For", " 203.0.113.5 , 10.0.0.1")
		req.RemoteAddr = "127.0.0.1:9999"
		if got := clientIP(req); got != "203.0.113.5" {
			t.Errorf("clientIP = %q, want %q", got, "203.0.113.5")
		}
	})

	t.Run("falls back to RemoteAddr host when no XFF", func(t *testing.T) {
		req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/", nil)
		req.RemoteAddr = "192.0.2.1:12345"
		if got := clientIP(req); got != "192.0.2.1" {
			t.Errorf("clientIP = %q, want %q", got, "192.0.2.1")
		}
	})

	t.Run("returns RemoteAddr as-is when it has no port", func(t *testing.T) {
		req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/", nil)
		req.RemoteAddr = "not-a-host-port"
		if got := clientIP(req); got != "not-a-host-port" {
			t.Errorf("clientIP = %q, want %q", got, "not-a-host-port")
		}
	})
}

func TestBuildDeviceMetadata(t *testing.T) {
	t.Run("nil info with an ip", func(t *testing.T) {
		m := buildDeviceMetadata(nil, "1.2.3.4")
		if m["last_seen_ip"] != "1.2.3.4" {
			t.Errorf("last_seen_ip = %v, want %q", m["last_seen_ip"], "1.2.3.4")
		}
		if len(m) != 1 {
			t.Errorf("metadata = %v, want only last_seen_ip", m)
		}
	})

	t.Run("full info with no ip", func(t *testing.T) {
		info := &deviceInfoRequest{
			Brand:      "Pixel",
			OSVersion:  "14",
			DeviceUUID: "abc-123",
			Language:   "en-US",
			Coords:     &coordsRequest{Lat: 12.5, Lon: -45.5},
		}
		m := buildDeviceMetadata(info, "")
		if m["brand"] != "Pixel" || m["os_version"] != "14" || m["device_uuid"] != "abc-123" || m["language"] != "en-US" {
			t.Errorf("metadata missing expected fields: %v", m)
		}
		if _, ok := m["last_seen_ip"]; ok {
			t.Errorf("last_seen_ip should be absent when ip is empty, got %v", m["last_seen_ip"])
		}
		coords, ok := m["coords"].(map[string]float64)
		if !ok || coords["lat"] != 12.5 || coords["lon"] != -45.5 {
			t.Errorf("coords = %v, want {lat:12.5 lon:-45.5}", m["coords"])
		}
	})
}
