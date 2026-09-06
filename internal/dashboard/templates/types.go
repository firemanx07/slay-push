// Package templates holds the dashboard's templ components (compiled from
// the .templ files in this directory into committed _templ.go files) plus
// the plain view-model types they render, kept separate from postgres's
// sqlc-generated types so templates never depend on the DB layer directly.
package templates

import "time"

// Message is a form-result notice — an error, a confirmation, or a neutral
// note — rendered as a color-coded callout instead of plain paragraph text.
// The zero value renders nothing.
type Message struct {
	Text string
	Kind string // "error", "success", or "" for a neutral note
}

// Project is the dashboard's view of a postgres.Project.
type Project struct {
	ID        string
	Name      string
	Slug      string
	Status    string
	CreatedAt time.Time
}

// ProviderCredential is the dashboard's view of a configured provider
// credential. It never carries the credential ciphertext or DEK.
type ProviderCredential struct {
	ProviderType string
	Environment  string
	IsActive     bool
	UpdatedAt    time.Time
}

// APIKey is the dashboard's view of a postgres.ApiKey.
type APIKey struct {
	ID         string
	Name       string
	KeyPrefix  string
	Scope      string
	CreatedAt  time.Time
	LastUsedAt *time.Time
	Revoked    bool
}

// Device is the dashboard's view of a postgres.Device.
type Device struct {
	ID           string
	ExternalID   string
	Platform     string
	ProviderType string
	Status       string
	CreatedAt    time.Time
}

// Notification is the dashboard's view of a postgres.Notification.
type Notification struct {
	ID              string
	Status          string
	TotalRecipients int32
	CreatedAt       time.Time
}

// RecipientCounts summarizes NotificationRecipient rows by status.
type RecipientCounts struct {
	Queued    int64
	Sending   int64
	Sent      int64
	Delivered int64
	Failed    int64
}

// Terminal reports whether every recipient has reached a terminal status.
func (c RecipientCounts) Terminal(total int32) bool {
	return c.Sent+c.Delivered+c.Failed >= int64(total)
}

// Recipient is the dashboard's view of a postgres.NotificationRecipient.
type Recipient struct {
	ID                string
	DeviceID          string
	ProviderType      string
	Status            string
	ProviderMessageID string
	ErrorMessage      string
	AttemptCount      int32
}

// Sidebar carries everything the app shell's sidebar needs to render the
// project switcher and, when a project is active, its section nav —
// independent of whatever the page itself renders.
type Sidebar struct {
	Projects  []Project
	ProjectID string // empty outside a project-scoped page
	ActiveNav string // one of "overview", "providers", "api-keys", "devices", "notifications"
}

// ProjectStats summarizes a project for the overview page's stat cards.
type ProjectStats struct {
	DeviceCount         int64
	ActiveProviderCount int
	ProviderCount       int
	ActiveAPIKeyCount   int
	APIKeyCount         int
	RecentNotifications []Notification
}
