package targeting

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/firemanx07/slay-push/internal/store/postgres"
)

type fakeDeviceLookup struct {
	devices []postgres.Device
}

func (f *fakeDeviceLookup) GetDevicesByIDs(_ context.Context, arg postgres.GetDevicesByIDsParams) ([]postgres.Device, error) {
	requested := make(map[uuid.UUID]bool, len(arg.Ids))
	for _, id := range arg.Ids {
		requested[postgres.UUIDTo(id)] = true
	}

	var out []postgres.Device
	for _, d := range f.devices {
		if requested[postgres.UUIDTo(d.ID)] {
			out = append(out, d)
		}
	}
	return out, nil
}

func TestByExplicitTargets_ResolvesKnownDevices(t *testing.T) {
	known := uuid.New()
	unknown := uuid.New()
	projectID := uuid.New()

	lookup := &fakeDeviceLookup{devices: []postgres.Device{
		{ID: postgres.UUIDFrom(known), ProjectID: postgres.UUIDFrom(projectID)},
	}}
	r := &ByExplicitTargets{DB: lookup}

	got, err := r.Resolve(context.Background(), projectID, Spec{DeviceIDs: []uuid.UUID{known, unknown}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 || got[0] != known {
		t.Errorf("got %v, want [%v] (unknown device id silently dropped)", got, known)
	}
}

func TestByExplicitTargets_NoTargets(t *testing.T) {
	r := &ByExplicitTargets{DB: &fakeDeviceLookup{}}
	_, err := r.Resolve(context.Background(), uuid.New(), Spec{})
	if err != ErrNoTargetsSpecified {
		t.Errorf("err = %v, want ErrNoTargetsSpecified", err)
	}
}

func TestRegistry_SelectsByExplicitTargets(t *testing.T) {
	known := uuid.New()
	projectID := uuid.New()
	lookup := &fakeDeviceLookup{devices: []postgres.Device{
		{ID: postgres.UUIDFrom(known), ProjectID: postgres.UUIDFrom(projectID)},
	}}
	reg := NewRegistry(lookup)

	got, err := reg.Resolve(context.Background(), projectID, Spec{DeviceIDs: []uuid.UUID{known}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 || got[0] != known {
		t.Errorf("got %v, want [%v]", got, known)
	}
}

func TestRegistry_NoSpecPopulated(t *testing.T) {
	reg := NewRegistry(&fakeDeviceLookup{})
	_, err := reg.Resolve(context.Background(), uuid.New(), Spec{})
	if err != ErrNoTargetsSpecified {
		t.Errorf("err = %v, want ErrNoTargetsSpecified", err)
	}
}
