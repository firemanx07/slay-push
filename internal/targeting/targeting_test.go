package targeting

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/firemanx07/slay-push/internal/store/postgres"
)

type fakeLookup struct {
	devices      []postgres.Device            // for ByExplicitTargets
	groupDevices map[string][]postgres.Device // external_id -> devices, for ByGroup
}

func (f *fakeLookup) GetDevicesByIDs(_ context.Context, arg postgres.GetDevicesByIDsParams) ([]postgres.Device, error) {
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

func (f *fakeLookup) GetActiveDevicesBySubscriberExternalIDs(_ context.Context, arg postgres.GetActiveDevicesBySubscriberExternalIDsParams) ([]postgres.Device, error) {
	var out []postgres.Device
	for _, extID := range arg.ExternalIds {
		out = append(out, f.groupDevices[extID]...)
	}
	return out, nil
}

func TestByExplicitTargets_ResolvesKnownDevices(t *testing.T) {
	known := uuid.New()
	unknown := uuid.New()
	projectID := uuid.New()

	lookup := &fakeLookup{devices: []postgres.Device{
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
	r := &ByExplicitTargets{DB: &fakeLookup{}}
	_, err := r.Resolve(context.Background(), uuid.New(), Spec{})
	if err != ErrNoTargetsSpecified {
		t.Errorf("err = %v, want ErrNoTargetsSpecified", err)
	}
}

func TestByGroup_ResolvesAllDevicesUnderExternalIDs(t *testing.T) {
	projectID := uuid.New()
	dev1, dev2, dev3 := uuid.New(), uuid.New(), uuid.New()

	lookup := &fakeLookup{groupDevices: map[string][]postgres.Device{
		"user-1": {
			{ID: postgres.UUIDFrom(dev1), ProjectID: postgres.UUIDFrom(projectID)},
			{ID: postgres.UUIDFrom(dev2), ProjectID: postgres.UUIDFrom(projectID)},
		},
		"user-2": {
			{ID: postgres.UUIDFrom(dev3), ProjectID: postgres.UUIDFrom(projectID)},
		},
	}}
	r := &ByGroup{DB: lookup}

	got, err := r.Resolve(context.Background(), projectID, Spec{ExternalUserIDs: []string{"user-1", "user-2", "user-unknown"}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := map[uuid.UUID]bool{dev1: true, dev2: true, dev3: true}
	if len(got) != len(want) {
		t.Fatalf("got %v, want 3 devices spanning both subscribers", got)
	}
	for _, id := range got {
		if !want[id] {
			t.Errorf("unexpected device id in result: %v", id)
		}
	}
}

func TestByGroup_NoTargets(t *testing.T) {
	r := &ByGroup{DB: &fakeLookup{}}
	_, err := r.Resolve(context.Background(), uuid.New(), Spec{})
	if err != ErrNoTargetsSpecified {
		t.Errorf("err = %v, want ErrNoTargetsSpecified", err)
	}
}

func TestRegistry_SelectsByExplicitTargets(t *testing.T) {
	known := uuid.New()
	projectID := uuid.New()
	lookup := &fakeLookup{devices: []postgres.Device{
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

func TestRegistry_SelectsByGroup(t *testing.T) {
	dev := uuid.New()
	projectID := uuid.New()
	lookup := &fakeLookup{groupDevices: map[string][]postgres.Device{
		"user-1": {{ID: postgres.UUIDFrom(dev), ProjectID: postgres.UUIDFrom(projectID)}},
	}}
	reg := NewRegistry(lookup)

	got, err := reg.Resolve(context.Background(), projectID, Spec{ExternalUserIDs: []string{"user-1"}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 || got[0] != dev {
		t.Errorf("got %v, want [%v]", got, dev)
	}
}

func TestRegistry_NoSpecPopulated(t *testing.T) {
	reg := NewRegistry(&fakeLookup{})
	_, err := reg.Resolve(context.Background(), uuid.New(), Spec{})
	if err != ErrNoTargetsSpecified {
		t.Errorf("err = %v, want ErrNoTargetsSpecified", err)
	}
}
