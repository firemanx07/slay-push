// Package targeting resolves a notification's target_spec into a concrete
// list of device ids. It is deliberately a strategy interface rather than a
// growing if/else: ByGroup (group push, via the subscribers table) is a
// second implementation alongside ByExplicitTargets, and a future ByFilter
// (segmentation) could be added later, without touching whichever
// strategies already exist or the dispatch/fanout code that calls Resolve.
package targeting

import (
	"context"
	"errors"

	"github.com/google/uuid"

	"github.com/firemanx07/slay-push/internal/store/postgres"
)

// Spec mirrors the subset of a create-notification request that identifies
// its audience. DeviceIDs is transactional (explicit targets); ExternalUserIDs
// is group push (every active device under one or more subscribers).
type Spec struct {
	DeviceIDs       []uuid.UUID
	ExternalUserIDs []string
}

var ErrNoTargetsSpecified = errors.New("targeting: no targets specified")

// Resolver is implemented once per targeting mode.
type Resolver interface {
	Resolve(ctx context.Context, projectID uuid.UUID, spec Spec) ([]uuid.UUID, error)
}

// DeviceLookup is the slice of *postgres.Queries ByExplicitTargets needs,
// kept as an interface so strategies are unit-testable without a live
// database.
type DeviceLookup interface {
	GetDevicesByIDs(ctx context.Context, arg postgres.GetDevicesByIDsParams) ([]postgres.Device, error)
}

// GroupLookup is the slice of *postgres.Queries ByGroup needs.
type GroupLookup interface {
	GetActiveDevicesBySubscriberExternalIDs(ctx context.Context, arg postgres.GetActiveDevicesBySubscriberExternalIDsParams) ([]postgres.Device, error)
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

// ByGroup resolves a group push: every active device belonging to a
// subscribed (not opted-out) subscriber matching any of the given external
// ids. Unknown external ids simply resolve to no devices, same silent-drop
// posture as ByExplicitTargets.
type ByGroup struct {
	DB GroupLookup
}

func (r *ByGroup) Resolve(ctx context.Context, projectID uuid.UUID, spec Spec) ([]uuid.UUID, error) {
	if len(spec.ExternalUserIDs) == 0 {
		return nil, ErrNoTargetsSpecified
	}

	devices, err := r.DB.GetActiveDevicesBySubscriberExternalIDs(ctx, postgres.GetActiveDevicesBySubscriberExternalIDsParams{
		ProjectID:   postgres.UUIDFrom(projectID),
		ExternalIds: spec.ExternalUserIDs,
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
// Spec is populated. A future ByFilter (segmentation) means adding one more
// case here plus its own file — ByExplicitTargets/ByGroup and every caller
// of Resolve are unaffected.
type Registry struct {
	explicit *ByExplicitTargets
	group    *ByGroup
}

// lookup is satisfied by *postgres.Queries in production; tests pass a
// smaller fake implementing just what each strategy needs.
type lookup interface {
	DeviceLookup
	GroupLookup
}

func NewRegistry(db lookup) *Registry {
	return &Registry{
		explicit: &ByExplicitTargets{DB: db},
		group:    &ByGroup{DB: db},
	}
}

func (r *Registry) Resolve(ctx context.Context, projectID uuid.UUID, spec Spec) ([]uuid.UUID, error) {
	switch {
	case len(spec.DeviceIDs) > 0:
		return r.explicit.Resolve(ctx, projectID, spec)
	case len(spec.ExternalUserIDs) > 0:
		return r.group.Resolve(ctx, projectID, spec)
	default:
		return nil, ErrNoTargetsSpecified
	}
}
