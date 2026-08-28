package http

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/firemanx07/slay-push/internal/provider"
	"github.com/firemanx07/slay-push/internal/store/postgres"
)

// platforms is the enum for device kind (ios/android/web), distinct from
// `provider` (which push network to use).
var platforms = map[string]bool{"ios": true, "android": true, "web": true}

type coordsRequest struct {
	Lat float64 `json:"lat"`
	Lon float64 `json:"lon"`
}

// deviceInfoRequest is optional device metadata, stored as-is in
// devices.metadata (jsonb). last_seen_ip is not part of this struct — the
// server captures it directly (see handleRegisterDevice).
type deviceInfoRequest struct {
	Brand      string         `json:"brand,omitempty"`
	OSVersion  string         `json:"os_version,omitempty"`
	DeviceUUID string         `json:"device_uuid,omitempty"`
	Language   string         `json:"language,omitempty"`
	Coords     *coordsRequest `json:"coords,omitempty"`
}

const (
	maxDeviceInfoFieldLength = 128
	maxLanguageLength        = 35 // generous for a BCP-47 tag (e.g. "zh-Hans-TW-x-private")
)

// validate checks field length constraints and coordinate bounds on device metadata.
func (d *deviceInfoRequest) validate() error {
	if d == nil {
		return nil
	}
	if len(d.Brand) > maxDeviceInfoFieldLength {
		return fmt.Errorf("device.brand must be at most %d characters", maxDeviceInfoFieldLength)
	}
	if len(d.OSVersion) > maxDeviceInfoFieldLength {
		return fmt.Errorf("device.os_version must be at most %d characters", maxDeviceInfoFieldLength)
	}
	if len(d.DeviceUUID) > maxDeviceInfoFieldLength {
		return fmt.Errorf("device.device_uuid must be at most %d characters", maxDeviceInfoFieldLength)
	}
	if len(d.Language) > maxLanguageLength {
		return fmt.Errorf("device.language must be at most %d characters", maxLanguageLength)
	}
	if d.Coords != nil {
		if d.Coords.Lat < -90 || d.Coords.Lat > 90 {
			return fmt.Errorf("device.coords.lat must be between -90 and 90, got %v", d.Coords.Lat)
		}
		if d.Coords.Lon < -180 || d.Coords.Lon > 180 {
			return fmt.Errorf("device.coords.lon must be between -180 and 180, got %v", d.Coords.Lon)
		}
	}
	return nil
}

const maxExternalUserIDLength = 256

type registerDeviceRequest struct {
	Token          string             `json:"token"`
	Provider       string             `json:"provider"`         // which push network — fcm/apns/hms/expo, required, never inferred
	Platform       string             `json:"platform"`         // what kind of device — ios/android/web, required
	ExternalUserID string             `json:"external_user_id"` // optional — groups this device under a subscriber for group push
	Device         *deviceInfoRequest `json:"device,omitempty"`
}

type registerDeviceResponse struct {
	ID string `json:"id"`
}

func (s *Server) handleRegisterDevice(w http.ResponseWriter, r *http.Request) {
	var req registerDeviceRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Token == "" {
		writeError(w, http.StatusUnprocessableEntity, "token is required")
		return
	}
	if len(req.Token) > maxTokenLength {
		writeError(w, http.StatusUnprocessableEntity, fmt.Sprintf("token must be at most %d characters", maxTokenLength))
		return
	}
	if req.Provider == "" || !provider.Known(req.Provider) {
		writeError(w, http.StatusUnprocessableEntity, "provider must be one of: expo, fcm, apns, hms")
		return
	}
	if req.Platform == "" || !platforms[req.Platform] {
		writeError(w, http.StatusUnprocessableEntity, "platform must be one of: ios, android, web")
		return
	}
	if len(req.ExternalUserID) > maxExternalUserIDLength {
		writeError(w, http.StatusUnprocessableEntity, fmt.Sprintf("external_user_id must be at most %d characters", maxExternalUserIDLength))
		return
	}
	if err := req.Device.validate(); err != nil {
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}

	metadataJSON, err := json.Marshal(buildDeviceMetadata(req.Device, clientIP(r)))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to encode device metadata")
		return
	}

	projectID, ok := projectIDFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusInternalServerError, "failed to resolve project")
		return
	}

	var device postgres.Device
	if req.ExternalUserID != "" && req.Device != nil && req.Device.DeviceUUID != "" {
		device, err = s.registerDeviceSerialized(r.Context(), projectID, req, metadataJSON)
	} else {
		device, err = s.upsertDevice(r.Context(), s.DB, projectID, req, metadataJSON)
	}
	if err != nil {
		s.Logger.Error().Err(err).Msg("register device failed")
		writeError(w, http.StatusInternalServerError, "failed to register device")
		return
	}

	writeJSON(w, http.StatusOK, registerDeviceResponse{ID: postgres.UUIDTo(device.ID).String()})
}

// upsertDevice upserts the subscriber (if an external_user_id was given)
// and the device row itself, against the given queries handle — either the
// server's pool-backed one, or a transaction-scoped one from
// registerDeviceSerialized.
func (s *Server) upsertDevice(ctx context.Context, q *postgres.Queries, projectID uuid.UUID, req registerDeviceRequest, metadataJSON []byte) (postgres.Device, error) {
	// subscriber_id stays NULL when no external_user_id is given.
	var subscriberID pgtype.UUID
	if req.ExternalUserID != "" {
		subscriber, err := q.UpsertSubscriber(ctx, postgres.UpsertSubscriberParams{
			ProjectID:  postgres.UUIDFrom(projectID),
			ExternalID: req.ExternalUserID,
		})
		if err != nil {
			return postgres.Device{}, fmt.Errorf("upsert subscriber: %w", err)
		}
		subscriberID = subscriber.ID
	}

	device, err := q.UpsertDevice(ctx, postgres.UpsertDeviceParams{
		ProjectID:    postgres.UUIDFrom(projectID),
		Token:        req.Token,
		Platform:     req.Platform,
		ProviderType: req.Provider,
		Metadata:     metadataJSON,
		SubscriberID: subscriberID,
	})
	if err != nil {
		return postgres.Device{}, fmt.Errorf("upsert device: %w", err)
	}
	return device, nil
}

// registerDeviceSerialized upserts the subscriber and device, then
// supersedes any other active device under the same subscriber sharing the
// same device_uuid (the same physical device rotating its push token) —
// all inside one transaction holding a transaction-scoped advisory lock
// keyed on (project, external_user_id, device_uuid). Without this lock, two
// concurrent registrations for the same physical device could each mark
// the other's row stale, leaving none active.
func (s *Server) registerDeviceSerialized(ctx context.Context, projectID uuid.UUID, req registerDeviceRequest, metadataJSON []byte) (postgres.Device, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return postgres.Device{}, fmt.Errorf("begin device registration tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	qtx := s.DB.WithTx(tx)

	lockKey := projectID.String() + ":" + req.ExternalUserID + ":" + req.Device.DeviceUUID
	if err := qtx.AdvisoryLockDeviceUUID(ctx, lockKey); err != nil {
		return postgres.Device{}, fmt.Errorf("acquire device registration lock: %w", err)
	}

	device, err := s.upsertDevice(ctx, qtx, projectID, req, metadataJSON)
	if err != nil {
		return postgres.Device{}, err
	}

	superseded, err := qtx.MarkStaleDevicesByDeviceUUID(ctx, postgres.MarkStaleDevicesByDeviceUUIDParams{
		ProjectID:    postgres.UUIDFrom(projectID),
		SubscriberID: device.SubscriberID,
		ID:           device.ID,
		DeviceUuid:   req.Device.DeviceUUID,
	})
	if err != nil {
		return postgres.Device{}, fmt.Errorf("supersede rotated devices: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return postgres.Device{}, fmt.Errorf("commit device registration tx: %w", err)
	}
	if len(superseded) > 0 {
		s.Logger.Info().Int("count", len(superseded)).Msg("superseded rotated device token(s)")
	}
	return device, nil
}

// buildDeviceMetadata merges the caller-supplied device info with the
// server-observed IP into the flat map stored in devices.metadata.
func buildDeviceMetadata(info *deviceInfoRequest, ip string) map[string]any {
	m := map[string]any{}
	if info != nil {
		if info.Brand != "" {
			m["brand"] = info.Brand
		}
		if info.OSVersion != "" {
			m["os_version"] = info.OSVersion
		}
		if info.DeviceUUID != "" {
			m["device_uuid"] = info.DeviceUUID
		}
		if info.Language != "" {
			m["language"] = info.Language
		}
		if info.Coords != nil {
			m["coords"] = map[string]float64{"lat": info.Coords.Lat, "lon": info.Coords.Lon}
		}
	}
	if ip != "" {
		m["last_seen_ip"] = ip
	}
	return m
}

// clientIP reads the real observed request IP, never from the request
// body. X-Forwarded-For is honored (first entry), falling back to
// RemoteAddr.
func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		parts := strings.Split(xff, ",")
		if ip := strings.TrimSpace(parts[0]); ip != "" {
			return ip
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
