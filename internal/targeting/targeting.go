// Package targeting resolves a notification's target_spec into a concrete
// list of device ids.
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

// DeviceLookup is the slice of *postgres.Queries ByExplicitTargets needs.
type DeviceLookup interface {
	GetDevicesByIDs(ctx context.Context, arg postgres.GetDevicesByIDsParams) ([]postgres.Device, error)
}

// GroupLookup is the slice of *postgres.Queries ByGroup needs.
type GroupLookup interface {
	GetActiveDevicesBySubscriberExternalIDs(ctx context.Context, arg postgres.GetActiveDevicesBySubscriberExternalIDsParams) ([]postgres.Device, error)
}

// ByExplicitTargets resolves an explicit list of device ids. Ids that don't
// belong to the project are dropped rather than erroring.
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

// ByGroup resolves every active device belonging to a subscribed subscriber
// matching any of the given external ids. Unknown external ids resolve to
// no devices.
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

// Registry dispatches to a strategy based on which field on Spec is
// populated.
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
