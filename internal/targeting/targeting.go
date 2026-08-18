// Package targeting resolves a notification's target_spec into a concrete
// list of device ids. It is deliberately a strategy interface rather than a
// growing if/else: ByGroup (group push, via the subscribers table) lands in
// Phase 2 as a second implementation, and a future ByFilter (segmentation)
// could be added later, without touching whichever strategies already exist
// or the dispatch/fanout code that calls Resolve.
package targeting

import (
	"context"
	"errors"

	"github.com/google/uuid"

	"github.com/firemanx07/slay-push/internal/store/postgres"
)

// Spec mirrors the subset of a create-notification request that identifies
// its audience. Only DeviceIDs (transactional — explicit targets) is
// implemented in Phase 1.
type Spec struct {
	DeviceIDs []uuid.UUID
}

var ErrNoTargetsSpecified = errors.New("targeting: no targets specified")

// Resolver is implemented once per targeting mode.
type Resolver interface {
	Resolve(ctx context.Context, projectID uuid.UUID, spec Spec) ([]uuid.UUID, error)
}

// DeviceLookup is the slice of *postgres.Queries a Resolver needs, kept as
// an interface so strategies are unit-testable without a live database.
type DeviceLookup interface {
	GetDevicesByIDs(ctx context.Context, arg postgres.GetDevicesByIDsParams) ([]postgres.Device, error)
}

// ByExplicitTargets resolves a transactional send: the caller already knows
// which device ids it wants. Ids that don't belong to the project (wrong
// tenant, typo, deleted device) are silently dropped rather than erroring —
// resolved targets are exactly "the subset of requested ids that are ours."
type ByExplicitTargets struct {
	DB DeviceLookup
}

func (r *ByExplicitTargets) Resolve(ctx context.Context, projectID uuid.UUID, spec Spec) ([]uuid.UUID, error) {
	if len(spec.DeviceIDs) == 0 {
		return nil, ErrNoTargetsSpecified
	}

	devices, err := r.DB.GetDevicesByIDs(ctx, postgres.GetDevicesByIDsParams{
		ProjectID: postgres.UUIDFrom(projectID),
		Ids:       postgres.UUIDsFrom(spec.DeviceIDs),
	})
	if err != nil {
		return nil, err
	}

	ids := make([]uuid.UUID, 0, len(devices))
	for _, d := range devices {
		ids = append(ids, postgres.UUIDTo(d.ID))
	}
	return ids, nil
}

// Registry is the small dispatch-selection switch, keyed by which field on
// Spec is populated. Adding ByGroup in Phase 2 means adding one more case
// here plus its own file — ByExplicitTargets and every caller of Resolve
// are unaffected.
type Registry struct {
	explicit *ByExplicitTargets
}

func NewRegistry(db DeviceLookup) *Registry {
	return &Registry{explicit: &ByExplicitTargets{DB: db}}
}

func (r *Registry) Resolve(ctx context.Context, projectID uuid.UUID, spec Spec) ([]uuid.UUID, error) {
	switch {
	case len(spec.DeviceIDs) > 0:
		return r.explicit.Resolve(ctx, projectID, spec)
	// case len(spec.ExternalUserIDs) > 0: return r.group.Resolve(ctx, projectID, spec) // Phase 2
	default:
		return nil, ErrNoTargetsSpecified
	}
}
