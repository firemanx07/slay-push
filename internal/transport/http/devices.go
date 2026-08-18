package http

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/firemanx07/slay-push/internal/provider"
	"github.com/firemanx07/slay-push/internal/store/postgres"
)

// platforms is the enum for what kind of device this is — distinct from
// `provider`, which is which push network to send through. A "web" device
// is a real platform even though there's no dedicated web-push adapter:
// FCM's HTTP v1 API already serves Web Push subscriptions, so a web device
// registers with provider "fcm" like any other FCM-backed device.
var platforms = map[string]bool{"ios": true, "android": true, "web": true}

type coordsRequest struct {
	Lat float64 `json:"lat"`
	Lon float64 `json:"lon"`
}

// deviceInfoRequest is device-describing metadata, entirely optional and
// stored as-is in devices.metadata (jsonb) — a flexible bag so adding a new
// optional attribute later never needs a migration. last_seen_ip is
// deliberately NOT part of this struct: a client-supplied IP can't be
// trusted, so the server captures the real request IP itself instead (see
// handleRegisterDevice).
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

	project, err := s.resolveProject(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to resolve project")
		return
	}

	// subscriber_id stays NULL (its zero value) when no external_user_id is
	// given — UpsertDevice's on-conflict path coalesces it, so a device
	// re-registering without external_user_id never loses an existing link.
	var subscriberID pgtype.UUID
	if req.ExternalUserID != "" {
		subscriber, err := s.DB.UpsertSubscriber(r.Context(), postgres.UpsertSubscriberParams{
			ProjectID:  project.ID,
			ExternalID: req.ExternalUserID,
		})
		if err != nil {
			s.Logger.Error().Err(err).Msg("upsert subscriber failed")
			writeError(w, http.StatusInternalServerError, "failed to register device")
			return
		}
		subscriberID = subscriber.ID
	}

	device, err := s.DB.UpsertDevice(r.Context(), postgres.UpsertDeviceParams{
		ProjectID:    project.ID,
		Token:        req.Token,
		Platform:     req.Platform,
		ProviderType: req.Provider,
		Metadata:     metadataJSON,
		SubscriberID: subscriberID,
	})
	if err != nil {
		s.Logger.Error().Err(err).Msg("upsert device failed")
		writeError(w, http.StatusInternalServerError, "failed to register device")
		return
	}

	writeJSON(w, http.StatusOK, registerDeviceResponse{ID: postgres.UUIDTo(device.ID).String()})
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

// clientIP reads the real observed request IP — never trusted from the
// request body, since a client can claim to be anywhere. X-Forwarded-For is
// honored (first entry) for deployments behind a reverse proxy, per the
// Docker Compose plan's expectation that operators front this with
// Caddy/Traefik/nginx; falls back to the raw connection's RemoteAddr.
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
