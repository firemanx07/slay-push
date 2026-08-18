// Package apns sends push notifications directly to Apple's APNs HTTP/2
// service via sideshow/apns2.
package apns

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/sideshow/apns2"
	"github.com/sideshow/apns2/token"

	"github.com/firemanx07/slay-push/internal/provider"
)

func init() {
	provider.Register("apns", New)
}

// credentialJSON supports token-based (.p8) and cert-based auth.
type credentialJSON struct {
	AuthType    string `json:"auth_type"`   // "token" (default) or "cert"
	Environment string `json:"environment"` // "production" (default) or "sandbox"

	// Token auth
	KeyID      string `json:"key_id"`
	TeamID     string `json:"team_id"`
	PrivateKey string `json:"private_key"` // .p8 PEM contents

	// Cert auth
	CertPEM string `json:"cert_pem"`
	KeyPEM  string `json:"key_pem"`

	// Topic APNs requires, typically the app's bundle id.
	BundleID string `json:"bundle_id"`
}

type cachedClient struct {
	client *apns2.Client
	topic  string
}

type adapter struct {
	mu      sync.Mutex
	clients map[[32]byte]*cachedClient // keyed by sha256(credential bytes)
}

// New returns an APNs provider.Adapter.
func New() provider.Adapter {
	return &adapter{clients: make(map[[32]byte]*cachedClient)}
}

func (a *adapter) Name() string { return "apns" }

func (a *adapter) clientFor(credential json.RawMessage) (*cachedClient, error) {
	key := sha256.Sum256(credential)

	a.mu.Lock()
	defer a.mu.Unlock()
	if cc, ok := a.clients[key]; ok {
		return cc, nil
	}

	var cred credentialJSON
	if err := json.Unmarshal(credential, &cred); err != nil {
		return nil, fmt.Errorf("apns: invalid credential: %w", err)
	}
	if cred.BundleID == "" {
		return nil, fmt.Errorf("apns: credential missing bundle_id")
	}

	var client *apns2.Client
	switch cred.AuthType {
	case "cert":
		certPair, err := tls.X509KeyPair([]byte(cred.CertPEM), []byte(cred.KeyPEM))
		if err != nil {
			return nil, fmt.Errorf("apns: parse cert credential: %w", err)
		}
		client = apns2.NewClient(certPair)
	default: // "token" or unset
		authKey, err := token.AuthKeyFromBytes([]byte(cred.PrivateKey))
		if err != nil {
			return nil, fmt.Errorf("apns: parse .p8 private key: %w", err)
		}
		client = apns2.NewTokenClient(&token.Token{
			AuthKey: authKey,
			KeyID:   cred.KeyID,
			TeamID:  cred.TeamID,
		})
	}

	if cred.Environment == "sandbox" {
		client = client.Development()
	} else {
		client = client.Production()
	}

	cc := &cachedClient{client: client, topic: cred.BundleID}
	a.clients[key] = cc
	return cc, nil
}

func (a *adapter) Send(ctx context.Context, credential json.RawMessage, req provider.SendRequest) (provider.SendResult, error) {
	cc, err := a.clientFor(credential)
	if err != nil {
		return provider.SendResult{}, err
	}

	payload := map[string]any{
		"aps": map[string]any{
			"alert": map[string]string{"title": req.Title, "body": req.Body},
		},
	}
	// Custom data sits alongside "aps" at the payload's top level, per
	// Apple's remote-notification payload convention.
	for k, v := range req.Data {
		payload[k] = v
	}

	resp, err := cc.client.PushWithContext(ctx, &apns2.Notification{
		DeviceToken: req.Token,
		Topic:       cc.topic,
		Payload:     payload,
	})
	if err != nil {
		return provider.SendResult{Status: provider.StatusTransientError}, fmt.Errorf("apns: push: %w", err)
	}

	return classifyResponse(resp), nil
}

// classifyResponse maps an APNs response Reason to a provider.Status.
// Reasons not listed explicitly fall through to the transient default.
func classifyResponse(resp *apns2.Response) provider.SendResult {
	if resp.Sent() {
		return provider.SendResult{ProviderMessageID: resp.ApnsID, Status: provider.StatusSent}
	}

	result := provider.SendResult{Err: fmt.Errorf("apns: %d %s", resp.StatusCode, resp.Reason)}
	switch resp.Reason {
	case apns2.ReasonBadDeviceToken, apns2.ReasonUnregistered, apns2.ReasonDeviceTokenNotForTopic:
		result.Status = provider.StatusInvalidToken
	case apns2.ReasonTooManyRequests:
		result.Status = provider.StatusThrottled
	case apns2.ReasonInternalServerError, apns2.ReasonServiceUnavailable, apns2.ReasonShutdown, apns2.ReasonIdleTimeout:
		result.Status = provider.StatusTransientError
	default:
		result.Status = provider.StatusTransientError
	}
	return result
}
