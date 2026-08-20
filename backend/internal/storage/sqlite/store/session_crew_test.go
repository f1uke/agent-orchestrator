package store_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// TestSessionCrew_DefaultsToSoloAndRoundTrips is the shape guarantee for crew
// membership: a session created the ordinary way is SOLO (both crew fields
// empty), and a crew written by the dedicated setter reads back intact.
//
// Solo-as-the-zero-value is the whole safety argument for this feature: every
// lifetime path (teardown, reclaim, the idle sweep) branches on these fields, so
// an unset column has to mean "behave exactly as before" rather than "we
// remembered to special-case it".
func TestSessionCrew_DefaultsToSoloAndRoundTrips(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "proj-1")

	dev, err := s.CreateSession(ctx, domain.SessionRecord{ProjectID: "proj-1", Kind: domain.KindWorker})
	if err != nil {
		t.Fatal(err)
	}
	qa, err := s.CreateSession(ctx, domain.SessionRecord{ProjectID: "proj-1", Kind: domain.KindWorker})
	if err != nil {
		t.Fatal(err)
	}

	if dev.InCrew() || dev.CrewID != "" || dev.CrewRole != "" {
		t.Fatalf("a normally created session must be solo, got crewID=%q role=%q", dev.CrewID, dev.CrewRole)
	}

	now := time.Now().UTC().Truncate(time.Second)
	for _, m := range []struct {
		id   domain.SessionID
		role domain.CrewRole
	}{{dev.ID, domain.CrewRoleDev}, {qa.ID, domain.CrewRoleQA}} {
		ok, err := s.SetSessionCrew(ctx, m.id, dev.ID, m.role, now)
		if err != nil {
			t.Fatalf("SetSessionCrew(%s): %v", m.id, err)
		}
		if !ok {
			t.Fatalf("SetSessionCrew(%s) reported no row updated", m.id)
		}
	}

	gotDev, _, err := s.GetSession(ctx, dev.ID)
	if err != nil {
		t.Fatal(err)
	}
	gotQA, _, err := s.GetSession(ctx, qa.ID)
	if err != nil {
		t.Fatal(err)
	}
	if gotDev.CrewID != dev.ID || !gotDev.CrewRole.IsDev() || !gotDev.InCrew() {
		t.Fatalf("dev row = crewID %q role %q, want %q/dev", gotDev.CrewID, gotDev.CrewRole, dev.ID)
	}
	if gotQA.CrewID != dev.ID || gotQA.CrewRole != domain.CrewRoleQA {
		t.Fatalf("qa row = crewID %q role %q, want %q/qa", gotQA.CrewID, gotQA.CrewRole, dev.ID)
	}
	if gotQA.OwnsCrewWorkspace() {
		t.Fatal("a qa member must not own the crew's workspace; only dev may destroy it")
	}
	if !gotDev.OwnsCrewWorkspace() {
		t.Fatal("dev must own the crew's workspace")
	}
}

// TestSessionCrew_SurvivesAFullRowUpdate pins why crew_id/crew_role are absent
// from UpdateSession's SET list. The lifecycle write path rewrites the whole row
// from a record it loaded earlier; if crew rode along, any stale in-flight copy
// would silently dissolve a crew and orphan qa on a worktree nobody refcounts.
func TestSessionCrew_SurvivesAFullRowUpdate(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "proj-1")

	dev, err := s.CreateSession(ctx, domain.SessionRecord{ProjectID: "proj-1", Kind: domain.KindWorker})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetSessionCrew(ctx, dev.ID, dev.ID, domain.CrewRoleDev, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}

	// A full-row write built from the PRE-crew snapshot: the exact hazard.
	stale := dev
	stale.Metadata.Branch = "feature/moved-on"
	if err := s.UpdateSession(ctx, stale); err != nil {
		t.Fatal(err)
	}

	got, _, err := s.GetSession(ctx, dev.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Metadata.Branch != "feature/moved-on" {
		t.Fatalf("branch = %q, want the update to have landed", got.Metadata.Branch)
	}
	if got.CrewID != dev.ID || !got.CrewRole.IsDev() {
		t.Fatalf("a full-row update blanked the crew: crewID=%q role=%q", got.CrewID, got.CrewRole)
	}
}

// TestSessionCrew_OneDevPerCrewIsEnforcedByTheDatabase: "which member is dev" is
// the fact teardown fans out on, so a second dev would make the task's owner
// ambiguous. The partial unique index makes that unrepresentable rather than
// merely unwritten - and stays exempt for the ” of every solo row.
func TestSessionCrew_OneDevPerCrewIsEnforcedByTheDatabase(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "proj-1")

	dev, err := s.CreateSession(ctx, domain.SessionRecord{ProjectID: "proj-1", Kind: domain.KindWorker})
	if err != nil {
		t.Fatal(err)
	}
	impostor, err := s.CreateSession(ctx, domain.SessionRecord{ProjectID: "proj-1", Kind: domain.KindWorker})
	if err != nil {
		t.Fatal(err)
	}
	solo, err := s.CreateSession(ctx, domain.SessionRecord{ProjectID: "proj-1", Kind: domain.KindWorker})
	if err != nil {
		t.Fatal(err)
	}

	now := time.Now().UTC()
	if _, err := s.SetSessionCrew(ctx, dev.ID, dev.ID, domain.CrewRoleDev, now); err != nil {
		t.Fatal(err)
	}
	_, err = s.SetSessionCrew(ctx, impostor.ID, dev.ID, domain.CrewRoleDev, now)
	if err == nil {
		t.Fatal("a second dev in one crew was accepted; the partial unique index must refuse it")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "unique") {
		t.Fatalf("want a UNIQUE constraint failure, got %v", err)
	}

	// The solo row is untouched by that index: '' is exempt.
	got, _, err := s.GetSession(ctx, solo.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.InCrew() {
		t.Fatal("an untouched session must still read as solo")
	}
}
