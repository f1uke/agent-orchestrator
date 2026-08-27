package integration_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	smokesvc "github.com/aoagents/agent-orchestrator/backend/internal/service/smoke"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
)

// smokeCrew stands up the REAL data path - sqlite store, real smoke service -
// with a dev and a qa on one task. Nothing here is a double: the concurrency
// claim is about what two writers do to one database, so a fake would prove
// nothing about it.
func smokeCrew(t *testing.T) (*smokesvc.Service, *sqlite.Store, domain.SessionID, domain.SessionID) {
	t.Helper()
	ctx := context.Background()
	store, err := sqlite.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.UpsertProject(ctx, domain.ProjectRecord{ID: "mer", Path: "/repo/mer", RegisteredAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	dev, err := store.CreateSession(ctx, domain.SessionRecord{
		ID: "mer-1", ProjectID: "mer", Kind: domain.KindWorker,
		CrewID: "mer-1", CrewRole: domain.CrewRoleDev, CreatedAt: time.Now(), UpdatedAt: time.Now(),
	})
	if err != nil {
		t.Fatal(err)
	}
	qa, err := store.CreateSession(ctx, domain.SessionRecord{
		ID: "mer-2", ProjectID: "mer", Kind: domain.KindWorker,
		CrewID: "mer-1", CrewRole: domain.CrewRoleQA, CreatedAt: time.Now(), UpdatedAt: time.Now(),
	})
	if err != nil {
		t.Fatal(err)
	}
	return smokesvc.New(store, t.TempDir(), nil), store, dev.ID, qa.ID
}

// THE CONCURRENCY CLAIM, driven rather than asserted.
//
// The whole design rests on "an author reaches only the cases it names", and the
// reason that had to become true is that `ao smoke set` sets the WHOLE list: two
// members using it in the same seconds erase each other. So this runs both
// members' writes AT ONCE, through the real service and the real store, and
// requires every case to survive.
func TestSharedAuthorship_ConcurrentWritersBothSurvive(t *testing.T) {
	svc, _, dev, qa := smokeCrew(t)
	ctx := context.Background()

	const perMember = 12
	var wg sync.WaitGroup
	errs := make(chan error, 2*perMember)
	start := make(chan struct{})

	write := func(from domain.SessionID, prefix string, i int) {
		defer wg.Done()
		<-start // release them together, so the writes genuinely interleave
		_, err := svc.AddCases(ctx, from, dev, []domain.SmokeAuthoredCase{{
			ID:   fmt.Sprintf("%s-%02d", prefix, i),
			Name: fmt.Sprintf("%s case %d", prefix, i),
			Why:  "written while the other member was writing",
		}})
		if err != nil {
			errs <- fmt.Errorf("%s-%02d: %w", prefix, i, err)
		}
	}
	for i := 0; i < perMember; i++ {
		wg.Add(2)
		go write(dev, "dev", i)
		go write(qa, "qa", i)
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("a concurrent write failed: %v", err)
	}

	res, err := svc.List(ctx, dev)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(res.Checks) != 2*perMember {
		t.Fatalf("checks = %d, want %d - a writer erased the other's cases", len(res.Checks), 2*perMember)
	}

	// And every case is attributed to the member that actually wrote it, which is
	// the whole reason a shared list is readable at all.
	byRole := map[domain.CrewRole]int{}
	seq := map[int]string{}
	for _, c := range res.Checks {
		byRole[c.AuthoredByRole]++
		if prior, clash := seq[c.Seq]; clash {
			t.Fatalf("two cases share seq %d (%s and %s): a user would see two CHECK %d", c.Seq, prior, c.ID, c.Seq)
		}
		seq[c.Seq] = c.ID
	}
	if byRole[domain.CrewRoleDev] != perMember || byRole[domain.CrewRoleQA] != perMember {
		t.Fatalf("attribution = %v, want %d each", byRole, perMember)
	}
}

// THE OTHER HALF OF THE CLAIM: neither member can destroy a case the human has
// already played, however they come at it - and they come at it while the other
// is writing, because that is the state the guard has to hold in.
func TestSharedAuthorship_NeitherMemberCanDestroyAPlayedCase(t *testing.T) {
	svc, store, dev, qa := smokeCrew(t)
	ctx := context.Background()

	if _, err := svc.Author(ctx, dev, dev, []domain.SmokeAuthoredCase{
		{ID: "played", Name: "The user judged this one"},
		{ID: "spare", Name: "Something else on the list"},
	}); err != nil {
		t.Fatalf("author: %v", err)
	}
	if _, err := svc.SetVerdict(ctx, dev, "played", domain.SmokeFail, "the header flickers"); err != nil {
		t.Fatalf("set verdict: %v", err)
	}
	if err := store.InsertSmokeEvidence(ctx, domain.SmokeEvidence{
		ID: "ev1", CheckID: "played", SessionID: dev, Kind: "image", Filename: "shot.png",
		Mime: "image/png", SizeBytes: 12, CreatedAt: time.Now().UTC(), Source: domain.SmokeEvidenceUser,
	}); err != nil {
		t.Fatalf("insert evidence: %v", err)
	}

	// Every way a member could take that case off the list, from both members, all
	// at once, while other writes land around them.
	var wg sync.WaitGroup
	refusals := make(chan error, 8)
	attack := func(from domain.SessionID, fn func() error) {
		defer wg.Done()
		refusals <- fn()
	}
	for _, from := range []domain.SessionID{dev, qa} {
		wg.Add(3)
		// An explicit removal.
		go attack(from, func() error { _, err := svc.RemoveCase(ctx, from, dev, "played"); return err })
		// A whole-list re-author that simply leaves it out.
		go attack(from, func() error {
			_, err := svc.Author(ctx, from, dev, []domain.SmokeAuthoredCase{{ID: "spare", Name: "Something else on the list"}})
			return err
		})
		// The accident the id derivation makes easy: the same case, reworded, so
		// its derived id no longer matches and the old one falls out.
		go attack(from, func() error {
			_, err := svc.Author(ctx, from, dev, []domain.SmokeAuthoredCase{{Name: "The user judged this one, reworded"}})
			return err
		})
	}
	wg.Wait()
	close(refusals)
	for err := range refusals {
		if !errors.Is(err, smokesvc.ErrResultsAtRisk) {
			t.Fatalf("a write that would destroy the user's result was not refused: %v", err)
		}
	}

	// The verdict, the note and the evidence bytes are all still there.
	res, err := svc.List(ctx, dev)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	var played *domain.SmokeCheck
	for i := range res.Checks {
		if res.Checks[i].ID == "played" {
			played = &res.Checks[i]
		}
	}
	if played == nil {
		t.Fatalf("the played case is gone: %+v", res.Checks)
	}
	if played.Verdict != domain.SmokeFail || played.Note != "the header flickers" || len(played.Evidence) != 1 {
		t.Fatalf("the played case lost what the user recorded: %+v", played)
	}
}

// A concurrent EDIT of the same case is last-write-wins, which is honest - but
// it must never lose the user's result, and it must never leave the row in a
// half-written state (the patch reads and writes inside one transaction).
func TestSharedAuthorship_ConcurrentEditsOfOneCaseKeepItCoherent(t *testing.T) {
	svc, _, dev, qa := smokeCrew(t)
	ctx := context.Background()

	if _, err := svc.Author(ctx, dev, dev, []domain.SmokeAuthoredCase{{
		ID: "c1", Name: "Drag scrolls the list", Why: "it has broken twice",
		Steps: []string{"Open the tab.", "Drag up."}, Expected: "the list follows the finger",
	}}); err != nil {
		t.Fatalf("author: %v", err)
	}
	if _, err := svc.SetVerdict(ctx, dev, "c1", domain.SmokePass, "looked right"); err != nil {
		t.Fatalf("set verdict: %v", err)
	}

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(2)
		pr := i
		go func() {
			defer wg.Done()
			if _, err := svc.EditCase(ctx, dev, dev, "c1", domain.SmokeCasePatch{PRNum: &pr}); err != nil {
				t.Errorf("dev edit: %v", err)
			}
		}()
		go func() {
			defer wg.Done()
			ref := fmt.Sprintf("f.go:%d", pr)
			if _, err := svc.EditCase(ctx, qa, dev, "c1", domain.SmokeCasePatch{FileRef: &ref}); err != nil {
				t.Errorf("qa edit: %v", err)
			}
		}()
	}
	wg.Wait()

	res, err := svc.List(ctx, dev)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	got := res.Checks[0]
	// Fields NEITHER writer named survive every interleaving: a patch that read
	// outside its transaction could write a stale copy of them back.
	if got.Name != "Drag scrolls the list" || got.Why != "it has broken twice" ||
		got.Expected != "the list follows the finger" || len(got.Steps) != 2 {
		t.Fatalf("concurrent one-field edits damaged the rest of the case: %+v", got)
	}
	// And the human's lane is untouched by either of them.
	if got.Verdict != domain.SmokePass || got.Note != "looked right" {
		t.Fatalf("concurrent edits reached the user's result: %q/%q", got.Verdict, got.Note)
	}
}

// The stand-down and the checklist are ONE state on screen, so they must be one
// state in the store: authoring a case retracts a stand-down even when the two
// happen together.
func TestSharedAuthorship_StandDownAndCasesStayConsistent(t *testing.T) {
	svc, _, dev, qa := smokeCrew(t)
	ctx := context.Background()

	if _, err := svc.StandDown(ctx, qa, dev, "no runtime surface in this change"); err != nil {
		t.Fatalf("stand down: %v", err)
	}
	res, err := svc.List(ctx, dev)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if res.StandDown == nil || res.StandDown.ByRole != domain.CrewRoleQA {
		t.Fatalf("the stand-down did not survive a round trip: %+v", res.StandDown)
	}

	if _, err := svc.AddCases(ctx, dev, dev, []domain.SmokeAuthoredCase{{ID: "c1", Name: "Actually, look at the header"}}); err != nil {
		t.Fatalf("add: %v", err)
	}
	res, err = svc.List(ctx, dev)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if res.StandDown != nil {
		t.Fatalf("a case is on the list and the stand-down still claims there is nothing to check: %+v", res.StandDown)
	}
	if len(res.Checks) != 1 {
		t.Fatalf("checks = %d, want 1", len(res.Checks))
	}
}
