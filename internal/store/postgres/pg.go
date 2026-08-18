package postgres

import (
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

// This file bridges sqlc's pgx-native types (pgtype.UUID, pgtype.Timestamptz)
// to the plain google/uuid.UUID and time.Time types the rest of the codebase
// (HTTP handlers, dispatch orchestration) uses — those are exactly the two
// places sqlc's wire types are awkward: JSON responses and testability
// without a live Postgres connection.

func UUIDFrom(u uuid.UUID) pgtype.UUID {
	return pgtype.UUID{Bytes: u, Valid: true}
}

func UUIDTo(u pgtype.UUID) uuid.UUID {
	return uuid.UUID(u.Bytes)
}

func UUIDsFrom(ids []uuid.UUID) []pgtype.UUID {
	out := make([]pgtype.UUID, len(ids))
	for i, id := range ids {
		out[i] = UUIDFrom(id)
	}
	return out
}

func TimeTo(t pgtype.Timestamptz) time.Time {
	return t.Time
}

func TimeToPtr(t pgtype.Timestamptz) *time.Time {
	if !t.Valid {
		return nil
	}
	tt := t.Time
	return &tt
}

func TextFrom(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
