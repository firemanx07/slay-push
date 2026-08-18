// Package postgres holds sqlc-generated queries/models plus hand-written
// helpers that bridge sqlc's pgx-native types to the domain-facing
// google/uuid.UUID and time.Time.
package postgres

import (
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

// UUIDFrom converts a uuid.UUID to its pgtype.UUID equivalent.
func UUIDFrom(u uuid.UUID) pgtype.UUID {
	return pgtype.UUID{Bytes: u, Valid: true}
}

// UUIDTo converts a pgtype.UUID to its uuid.UUID equivalent.
func UUIDTo(u pgtype.UUID) uuid.UUID {
	return uuid.UUID(u.Bytes)
}

// UUIDsFrom converts a slice of uuid.UUID to their pgtype.UUID equivalents.
func UUIDsFrom(ids []uuid.UUID) []pgtype.UUID {
	out := make([]pgtype.UUID, len(ids))
	for i, id := range ids {
		out[i] = UUIDFrom(id)
	}
	return out
}

// TimeTo converts a pgtype.Timestamptz to time.Time.
func TimeTo(t pgtype.Timestamptz) time.Time {
	return t.Time
}

// TimeToPtr converts a pgtype.Timestamptz to *time.Time, returning nil when
// the value isn't valid.
func TimeToPtr(t pgtype.Timestamptz) *time.Time {
	if !t.Valid {
		return nil
	}
	tt := t.Time
	return &tt
}

// TextFrom converts s to *string, returning nil for an empty string.
func TextFrom(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
