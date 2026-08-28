package http

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/firemanx07/slay-push/internal/store/postgres"
)

func newNotificationRequest(ctx context.Context, method, path string, projectID uuid.UUID, body string) *http.Request {
	return httptest.NewRequestWithContext(contextWithProjectID(ctx, projectID), method, path, strings.NewReader(body))
}

// withChiParam attaches the chi "id" URL param the way the router would,
// for tests that call a handler directly instead of going through the
// router — every route in this package's router uses "id" as its only
// path param.
func withChiParam(r *http.Request, value string) *http.Request {
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", value)
	return r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rctx))
}

func TestHandleCreateNotification_Validation(t *testing.T) {
	h := newTestHarness(t)
	ctx := context.Background()
	projectID := postgres.UUIDTo(h.project.ID)
	deviceID := uuid.NewString()

	tests := []struct {
		name string
		body string
	}{
		{"neither target field set", `{}`},
		{"both target fields set", `{"include_player_ids":["` + deviceID + `"],"include_external_user_ids":["u1"]}`},
		{"included_segments rejected", `{"include_player_ids":["` + deviceID + `"],"included_segments":["seg"]}`},
		{"filters rejected", `{"include_player_ids":["` + deviceID + `"],"filters":[{"a":"b"}]}`},
		{"invalid uuid in include_player_ids", `{"include_player_ids":["not-a-uuid"]}`},
		{"title too long", `{"include_player_ids":["` + deviceID + `"],"title":"` + strings.Repeat("a", maxTitleLength+1) + `"}`},
		{"body too long", `{"include_player_ids":["` + deviceID + `"],"body":"` + strings.Repeat("a", maxBodyLength+1) + `"}`},
		{"idempotency_key too long", `{"include_player_ids":["` + deviceID + `"],"idempotency_key":"` + strings.Repeat("a", maxIdempotencyKeyLength+1) + `"}`},
		{"too many include_player_ids", `{"include_player_ids":[` + strings.TrimSuffix(strings.Repeat(`"`+deviceID+`",`, maxDeviceIDsPerRequest+1), ",") + `]}`},
		{"too many include_external_user_ids", `{"include_external_user_ids":[` + strings.TrimSuffix(strings.Repeat(`"x",`, maxDeviceIDsPerRequest+1), ",") + `]}`},
		{"data too large", `{"include_player_ids":["` + deviceID + `"],"data":{"blob":"` + strings.Repeat("a", maxDataBytes) + `"}}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := newNotificationRequest(ctx, http.MethodPost, "/api/v1/notifications", projectID, tt.body)
			w := httptest.NewRecorder()
			h.server.handleCreateNotification(w, req)
			if w.Code != http.StatusUnprocessableEntity {
				t.Errorf("status = %d, want %d; body: %s", w.Code, http.StatusUnprocessableEntity, w.Body.String())
			}
		})
	}
}

func TestHandleCreateNotification_Success(t *testing.T) {
	h := newTestHarness(t)
	ctx := context.Background()
	projectID := postgres.UUIDTo(h.project.ID)

	device := mustCreateDeviceForNotification(ctx, t, h, projectID)

	body := `{"include_player_ids":["` + device.String() + `"],"title":"hi","body":"there"}`
	req := newNotificationRequest(ctx, http.MethodPost, "/api/v1/notifications", projectID, body)
	w := httptest.NewRecorder()
	h.server.handleCreateNotification(w, req)

	if w.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want %d; body: %s", w.Code, http.StatusAccepted, w.Body.String())
	}
	var resp createNotificationResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Status != "pending" {
		t.Errorf("status in response = %q, want %q", resp.Status, "pending")
	}
	if _, err := uuid.Parse(resp.ID); err != nil {
		t.Errorf("response id %q is not a valid UUID: %v", resp.ID, err)
	}
}

func TestHandleCreateNotification_SuccessViaExternalUserIDs(t *testing.T) {
	h := newTestHarness(t)
	ctx := context.Background()
	projectID := postgres.UUIDTo(h.project.ID)
	externalUserID := "user-" + uuid.NewString()

	if _, err := h.queries.UpsertSubscriber(ctx, postgres.UpsertSubscriberParams{
		ProjectID:  postgres.UUIDFrom(projectID),
		ExternalID: externalUserID,
	}); err != nil {
		t.Fatalf("create test subscriber: %v", err)
	}

	body := `{"include_external_user_ids":["` + externalUserID + `"],"title":"hi","body":"there"}`
	req := newNotificationRequest(ctx, http.MethodPost, "/api/v1/notifications", projectID, body)
	w := httptest.NewRecorder()
	h.server.handleCreateNotification(w, req)

	if w.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want %d; body: %s", w.Code, http.StatusAccepted, w.Body.String())
	}
}

func TestHandleGetNotification(t *testing.T) {
	h := newTestHarness(t)
	ctx := context.Background()
	projectID := postgres.UUIDTo(h.project.ID)

	t.Run("invalid id format", func(t *testing.T) {
		req := withChiParam(newNotificationRequest(ctx, http.MethodGet, "/api/v1/notifications/not-a-uuid", projectID, ""), "not-a-uuid")
		w := httptest.NewRecorder()
		h.server.handleGetNotification(w, req)
		if w.Code != http.StatusBadRequest {
			t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
		}
	})

	t.Run("not found", func(t *testing.T) {
		missing := uuid.NewString()
		req := withChiParam(newNotificationRequest(ctx, http.MethodGet, "/api/v1/notifications/"+missing, projectID, ""), missing)
		w := httptest.NewRecorder()
		h.server.handleGetNotification(w, req)
		if w.Code != http.StatusNotFound {
			t.Errorf("status = %d, want %d", w.Code, http.StatusNotFound)
		}
	})

	t.Run("found", func(t *testing.T) {
		n, err := h.queries.CreateNotification(ctx, postgres.CreateNotificationParams{
			ProjectID:  postgres.UUIDFrom(projectID),
			Data:       []byte(`{}`),
			TargetSpec: []byte(`{}`),
		})
		if err != nil {
			t.Fatalf("create test notification: %v", err)
		}
		id := postgres.UUIDTo(n.ID).String()
		req := withChiParam(newNotificationRequest(ctx, http.MethodGet, "/api/v1/notifications/"+id, projectID, ""), id)
		w := httptest.NewRecorder()
		h.server.handleGetNotification(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d; body: %s", w.Code, http.StatusOK, w.Body.String())
		}
		var resp notificationStatusResponse
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatalf("decode response: %v", err)
		}
		if resp.ID != id {
			t.Errorf("id = %q, want %q", resp.ID, id)
		}
		if resp.Status != "pending" {
			t.Errorf("status = %q, want %q", resp.Status, "pending")
		}
	})
}

func TestHandleListRecipients(t *testing.T) {
	h := newTestHarness(t)
	ctx := context.Background()
	projectID := postgres.UUIDTo(h.project.ID)

	n, err := h.queries.CreateNotification(ctx, postgres.CreateNotificationParams{
		ProjectID:  postgres.UUIDFrom(projectID),
		Data:       []byte(`{}`),
		TargetSpec: []byte(`{}`),
	})
	if err != nil {
		t.Fatalf("create test notification: %v", err)
	}

	t.Run("empty", func(t *testing.T) {
		id := postgres.UUIDTo(n.ID).String()
		req := withChiParam(newNotificationRequest(ctx, http.MethodGet, "/api/v1/notifications/"+id+"/recipients", projectID, ""), id)
		w := httptest.NewRecorder()
		h.server.handleListRecipients(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d; body: %s", w.Code, http.StatusOK, w.Body.String())
		}
		var recipients []recipientResponse
		if err := json.Unmarshal(w.Body.Bytes(), &recipients); err != nil {
			t.Fatalf("decode response: %v", err)
		}
		if len(recipients) != 0 {
			t.Errorf("recipients = %d, want 0", len(recipients))
		}
	})

	t.Run("non-empty", func(t *testing.T) {
		device := mustCreateDeviceForNotification(ctx, t, h, projectID)
		if _, err := h.queries.InsertNotificationRecipient(ctx, postgres.InsertNotificationRecipientParams{
			NotificationID: n.ID,
			DeviceID:       postgres.UUIDFrom(device),
			ProviderType:   "expo",
		}); err != nil {
			t.Fatalf("insert recipient: %v", err)
		}

		id := postgres.UUIDTo(n.ID).String()
		req := withChiParam(newNotificationRequest(ctx, http.MethodGet, "/api/v1/notifications/"+id+"/recipients", projectID, ""), id)
		w := httptest.NewRecorder()
		h.server.handleListRecipients(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d; body: %s", w.Code, http.StatusOK, w.Body.String())
		}
		var recipients []recipientResponse
		if err := json.Unmarshal(w.Body.Bytes(), &recipients); err != nil {
			t.Fatalf("decode response: %v", err)
		}
		if len(recipients) != 1 {
			t.Fatalf("recipients = %d, want 1", len(recipients))
		}
		if recipients[0].DeviceID != device.String() {
			t.Errorf("device_id = %q, want %q", recipients[0].DeviceID, device.String())
		}
	})
}

// mustCreateDeviceForNotification registers a device directly (no
// external_user_id/device_uuid — the simple upsertDevice path) for tests
// that need a real device id to target.
func mustCreateDeviceForNotification(ctx context.Context, t *testing.T, h *testHarness, projectID uuid.UUID) uuid.UUID {
	t.Helper()
	device, err := h.queries.UpsertDevice(ctx, postgres.UpsertDeviceParams{
		ProjectID:    postgres.UUIDFrom(projectID),
		Token:        "tok-" + uuid.NewString(),
		Platform:     "android",
		ProviderType: "expo",
		Metadata:     []byte(`{}`),
	})
	if err != nil {
		t.Fatalf("create test device: %v", err)
	}
	return postgres.UUIDTo(device.ID)
}
