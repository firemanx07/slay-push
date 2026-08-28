package postgres

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

func TestUUIDRoundTrip(t *testing.T) {
	want := uuid.New()
	got := UUIDTo(UUIDFrom(want))
	if got != want {
		t.Errorf("UUIDTo(UUIDFrom(%v)) = %v, want %v", want, got, want)
	}
}

func TestUUIDsFrom(t *testing.T) {
	ids := []uuid.UUID{uuid.New(), uuid.New(), uuid.New()}
	got := UUIDsFrom(ids)
	if len(got) != len(ids) {
		t.Fatalf("UUIDsFrom returned %d entries, want %d", len(got), len(ids))
	}
	for i, id := range ids {
		if UUIDTo(got[i]) != id {
			t.Errorf("entry %d: got %v, want %v", i, UUIDTo(got[i]), id)
		}
	}
}

func TestTimeTo(t *testing.T) {
	want := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	got := TimeTo(TimeFrom(want))
	if !got.Equal(want) {
		t.Errorf("TimeTo(TimeFrom(%v)) = %v, want %v", want, got, want)
	}
}

func TestTimeToPtr(t *testing.T) {
	t.Run("invalid returns nil", func(t *testing.T) {
		if got := TimeToPtr(pgtype.Timestamptz{}); got != nil {
			t.Errorf("TimeToPtr(invalid) = %v, want nil", got)
		}
	})

	t.Run("valid returns a pointer to the time", func(t *testing.T) {
		want := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
		got := TimeToPtr(TimeFrom(want))
		if got == nil {
			t.Fatal("TimeToPtr(valid) = nil, want a non-nil pointer")
		}
		if !got.Equal(want) {
			t.Errorf("*TimeToPtr(valid) = %v, want %v", *got, want)
		}
	})
}

func TestTextFrom(t *testing.T) {
	t.Run("empty string returns nil", func(t *testing.T) {
		if got := TextFrom(""); got != nil {
			t.Errorf("TextFrom(\"\") = %v, want nil", got)
		}
	})

	t.Run("non-empty string returns a pointer to it", func(t *testing.T) {
		got := TextFrom("hello")
		if got == nil || *got != "hello" {
			t.Errorf("TextFrom(\"hello\") = %v, want a pointer to \"hello\"", got)
		}
	})
}
