package store_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

const otherTestUDID = "C4764B41-8F74-49C6-8766-A20EA46125BF"

func assignment(owner domain.SessionID, udid string, at time.Time) domain.SimDeviceAssignment {
	return domain.SimDeviceAssignment{SessionID: owner, UDID: udid, AssignedAt: at}
}

func TestAssignSimDevice_GivesASessionADeviceOfItsOwn(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "mer")
	owner := seedSession(t, s, "mer")
	now := time.Now().UTC().Truncate(time.Second)

	held, taken, err := s.AssignSimDevice(ctx, assignment(owner, testUDID, now))
	if err != nil {
		t.Fatalf("assign: %v", err)
	}
	if !taken || held.UDID != testUDID || held.SessionID != owner {
		t.Fatalf("assign = %+v, taken=%v; want the device given to this session", held, taken)
	}
	got, ok, err := s.GetSimDeviceAssignment(ctx, owner)
	if err != nil || !ok || got.UDID != testUDID {
		t.Fatalf("read back = %+v, %v, %v", got, ok, err)
	}
}

// The exclusion is the schema, not a check in Go: udid is UNIQUE, so a second
// session cannot be given a device that already belongs to somebody.
func TestAssignSimDevice_RefusesADeviceAnotherSessionHolds(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "mer")
	owner, other := seedSession(t, s, "mer"), seedSession(t, s, "mer")
	now := time.Now().UTC().Truncate(time.Second)

	if _, _, err := s.AssignSimDevice(ctx, assignment(owner, testUDID, now)); err != nil {
		t.Fatal(err)
	}
	held, taken, err := s.AssignSimDevice(ctx, assignment(other, testUDID, now))
	if err != nil {
		t.Fatalf("a refused assignment is an outcome, not an error: %v", err)
	}
	if taken {
		t.Fatal("two sessions were given the same device")
	}
	if held.UDID != "" {
		t.Fatalf("the loser was told it holds %s", held.UDID)
	}
}

// A session holds at most one device: asking again returns what it already has
// rather than accumulating reservations it will never give back.
func TestAssignSimDevice_ASessionKeepsTheDeviceItAlreadyHas(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "mer")
	owner := seedSession(t, s, "mer")
	now := time.Now().UTC().Truncate(time.Second)

	if _, _, err := s.AssignSimDevice(ctx, assignment(owner, testUDID, now)); err != nil {
		t.Fatal(err)
	}
	held, taken, err := s.AssignSimDevice(ctx, assignment(owner, otherTestUDID, now))
	if err != nil {
		t.Fatal(err)
	}
	if taken {
		t.Fatal("a session was moved to a second device behind its own back")
	}
	if held.UDID != testUDID {
		t.Fatalf("held = %s, want the device it already had", held.UDID)
	}
}

// An assignment that outlived its owner would remove a device from the pool for
// good. The release lives in the schema next to the fact it reacts to, so every
// path that ends a session gives the device back without remembering to.
func TestAssignSimDevice_ReleasedWhenTheSessionIsTerminated(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "mer")
	owner := seedSession(t, s, "mer")
	now := time.Now().UTC().Truncate(time.Second)
	if _, _, err := s.AssignSimDevice(ctx, assignment(owner, testUDID, now)); err != nil {
		t.Fatal(err)
	}

	record, _, err := s.GetSession(ctx, owner)
	if err != nil {
		t.Fatal(err)
	}
	record.IsTerminated = true
	if err := s.UpdateSession(ctx, record); err != nil {
		t.Fatal(err)
	}

	if _, ok, err := s.GetSimDeviceAssignment(ctx, owner); err != nil || ok {
		t.Fatalf("a terminated session still holds a device: ok=%v err=%v", ok, err)
	}
	newcomer := seedSession(t, s, "mer")
	_, taken, err := s.AssignSimDevice(ctx, assignment(newcomer, testUDID, now))
	if err != nil || !taken {
		t.Fatalf("the freed device was not assignable again: taken=%v err=%v", taken, err)
	}
}

// Two spawns racing for the last free device resolve to exactly one winner, and
// the loser is told so rather than silently sharing it.
func TestAssignSimDevice_ConcurrentSpawnsResolveToOneWinner(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "mer")
	now := time.Now().UTC().Truncate(time.Second)
	sessions := []domain.SessionID{
		seedSession(t, s, "mer"), seedSession(t, s, "mer"),
		seedSession(t, s, "mer"), seedSession(t, s, "mer"),
	}

	var wg sync.WaitGroup
	results := make([]bool, len(sessions))
	for i, owner := range sessions {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, taken, err := s.AssignSimDevice(ctx, assignment(owner, testUDID, now))
			if err != nil {
				t.Errorf("assign: %v", err)
				return
			}
			results[i] = taken
		}()
	}
	wg.Wait()

	winners := 0
	for _, taken := range results {
		if taken {
			winners++
		}
	}
	if winners != 1 {
		t.Fatalf("%d sessions were given one device, want exactly 1", winners)
	}
}

func TestReleaseSimDeviceAssignment_HandsTheDeviceBack(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "mer")
	owner := seedSession(t, s, "mer")
	now := time.Now().UTC().Truncate(time.Second)
	if _, _, err := s.AssignSimDevice(ctx, assignment(owner, testUDID, now)); err != nil {
		t.Fatal(err)
	}

	released, err := s.ReleaseSimDeviceAssignment(ctx, owner)
	if err != nil || !released {
		t.Fatalf("release = %v, %v", released, err)
	}
	if again, err := s.ReleaseSimDeviceAssignment(ctx, owner); err != nil || again {
		t.Fatalf("releasing a device nobody holds must report so: %v, %v", again, err)
	}
	assignments, err := s.ListSimDeviceAssignments(ctx)
	if err != nil || len(assignments) != 0 {
		t.Fatalf("assignments = %+v, %v", assignments, err)
	}
}
