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

func TestHandleRegisterDevice_MissingProject(t *testing.T) {
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/v1/devices", bytes.NewReader([]byte(`{"token":"tok","provider":"expo","platform":"android"}`)))
	w := httptest.NewRecorder()
	(&Server{}).handleRegisterDevice(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want %d (no project in context)", w.Code, http.StatusInternalServerError)
	}
}
