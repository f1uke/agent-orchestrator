package integration

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	"github.com/aoagents/agent-orchestrator/backend/internal/msgqueue"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	sessionmanager "github.com/aoagents/agent-orchestrator/backend/internal/session_manager"
)

// ATTACHING A QA to a task that is already running, against a real git worktree,
// a real SQLite store and the real lifecycle reducer.
//
// The whole feature is one claim - "you can add a second agent to a task in
// flight without disturbing the one that is working" - and it is a claim about
// what happens to a REAL checkout that a REAL agent is holding. A fake workspace
// cannot fail the way this must not fail, so the tree here is real.

// TestCrewAttach_AMechanicalTaskGainsAWorkingQA is the manual half of lazy
// creation: a `mechanical` task is never given a qa by the trigger, so a human
// asking for one is a CREATE - and it arrives WORKING, in dev's tree, beside a
// dev that is not disturbed.
func TestCrewAttach_AMechanicalTaskGainsAWorkingQA(t *testing.T) {
	ctx := context.Background()
	s := newCrewStack(t)
	s.rt.perSessionHandles = true

	dev, err := s.mgr.Spawn(ctx, ports.SpawnConfig{
		ProjectID: "mer", Kind: domain.KindWorker, Branch: "feature/task", Prompt: "rename the flag",
		TaskSize: domain.TaskSizeMechanical,
	})
	if err != nil {
		t.Fatalf("spawn dev: %v", err)
	}
	createdBefore, destroyedBefore := s.rt.created, s.rt.destroyed

	qa, err := s.mgr.AttachCrewMember(ctx, dev.ID, domain.CrewRoleQA, "")
	if err != nil {
		t.Fatalf("AttachCrewMember: %v", err)
	}

	// The member exists and is RUNNING: a human who asks for a qa gets one that is
	// working, because there is no turn left for it to wait for.
	if qa.IsSuspended || !qa.Awake() {
		t.Fatalf("the attached member did not start: suspended=%v awake=%v", qa.IsSuspended, qa.Awake())
	}
	if qa.Metadata.RuntimeHandleID == "" {
		t.Fatalf("the attached member is awake with no runtime handle")
	}
	if s.rt.created != createdBefore+1 {
		t.Fatalf("runtimes created %d->%d, want exactly one more (the new member's)", createdBefore, s.rt.created)
	}
	// ...and dev's terminal was not touched on the way.
	if s.rt.destroyed != destroyedBefore {
		t.Fatalf("attaching destroyed a runtime: %d->%d", destroyedBefore, s.rt.destroyed)
	}

	// dev is untouched: it keeps working straight through the attach, which is the
	// whole point of being able to do this to a task in flight.
	devRow := s.record(t, dev.ID)
	if devRow.IsSuspended || !devRow.Awake() {
		t.Fatalf("attaching stood dev down: suspended=%v awake=%v", devRow.IsSuspended, devRow.Awake())
	}
	s.assertBothAwake(t, dev.ID, qa.ID)

	// One REAL worktree, still on disk, with dev's work in it.
	if qa.Metadata.WorkspacePath != devRow.Metadata.WorkspacePath || qa.Metadata.Branch != devRow.Metadata.Branch {
		t.Fatalf("the attached member is not in dev's tree: %q@%q vs %q@%q",
			qa.Metadata.WorkspacePath, qa.Metadata.Branch, devRow.Metadata.WorkspacePath, devRow.Metadata.Branch)
	}
	if !dirExists(t, devRow.Metadata.WorkspacePath) {
		t.Fatal("attaching disturbed the shared worktree")
	}
	// The crew is recorded on BOTH rows, keyed on dev's id.
	if devRow.CrewID != devRow.ID || !devRow.CrewRole.IsDev() {
		t.Fatalf("dev row = crew %q role %q, want %s/dev", devRow.CrewID, devRow.CrewRole, devRow.ID)
	}
	if qa.CrewID != devRow.ID || qa.CrewRole != domain.CrewRoleQA {
		t.Fatalf("qa row = crew %q role %q, want %s/qa", qa.CrewID, qa.CrewRole, devRow.ID)
	}
}

// TestCrewAttach_TheNewMemberIsReachableBeforeItWakes. A member normally arrives
// working, but its start is BEST EFFORT - and a member whose launch failed must
// still be addressable: `ao send` to it is HELD by #217's queue, not dropped and
// not a wake.
func TestCrewAttach_TheNewMemberIsReachableBeforeItWakes(t *testing.T) {
	ctx := context.Background()
	s := newCrewStack(t)
	s.rt.perSessionHandles = true
	dev, qa := s.attachedCrewNeverStarted(t)

	pane := &paneSpy{}
	now := s.now
	queue := msgqueue.New(s.store, pane, pane, slog.New(slog.NewTextHandler(io.Discard, nil)),
		msgqueue.WithClock(func() time.Time { return now }))
	messenger := queueingMessenger{store: s.store, pane: pane, queue: queue}

	out, err := messenger.Send(ctx, qa.ID, "have a look at the flag rename before we merge")
	if err != nil {
		t.Fatalf("send to the newly attached member: %v", err)
	}
	if !out.Queued {
		t.Fatal("a message for a member that has never woken was typed at a pane that does not exist")
	}
	for range 3 {
		if err := queue.Drain(ctx); err != nil {
			t.Fatalf("drain: %v", err)
		}
		now = now.Add(2 * time.Second)
	}
	if typed := pane.typed(); len(typed) != 0 {
		t.Fatalf("draining delivered %v to a member with no terminal", typed)
	}
	if got := s.awakeMembers(t, dev.ID, qa.ID); len(got) != 1 || got[0] != dev.ID {
		t.Fatalf("awake after a held message = %v, want only dev: a message must not start an agent", got)
	}

	// And it is not lost: it lands when the member is started.
	if _, err := s.mgr.WakeCrewMember(ctx, qa.ID); err != nil {
		t.Fatalf("wake the attached member: %v", err)
	}
	for range 3 {
		if err := queue.Drain(ctx); err != nil {
			t.Fatalf("drain: %v", err)
		}
		now = now.Add(2 * time.Second)
	}
	if len(pane.typed()) == 0 {
		t.Fatal("the held message never reached the member after it woke")
	}
}

// TestCrewAttach_StartingItLeavesDevRunning. An attached member is started by
// exactly the route a spawn-time one is - and starting it takes nothing from the
// member already working. Both agents then have the shared checkout, which is
// the point of this chunk and the state its predecessor existed to prevent.
func TestCrewAttach_StartingItLeavesDevRunning(t *testing.T) {
	ctx := context.Background()
	s := newCrewStack(t)
	s.rt.perSessionHandles = true
	s.rt.trackLiveness = true
	dev, qa := s.attachedCrewNeverStarted(t)
	devHandle := s.record(t, dev.ID).Metadata.RuntimeHandleID

	if _, err := s.mgr.WakeCrewMember(ctx, qa.ID); err != nil {
		t.Fatalf("WakeCrewMember: %v", err)
	}
	s.assertBothAwake(t, dev.ID, qa.ID)
	if s.rt.wasDestroyed(devHandle) {
		t.Fatalf("dev's terminal %q was reaped when qa started", devHandle)
	}
	devRow := s.record(t, dev.ID)
	if devRow.IsSuspended || devRow.IsTerminated {
		t.Fatalf("dev = suspended %v terminated %v; starting qa must leave it working",
			devRow.IsSuspended, devRow.IsTerminated)
	}
	if !dirExists(t, devRow.Metadata.WorkspacePath) {
		t.Fatal("starting the second member removed the shared worktree")
	}

	// AO_CREW_ID is dev's id for the member that just came up, so the checklist it
	// writes lands on the card the human opens.
	if got := s.rt.lastCfg.Env[sessionmanager.EnvCrewID]; got != string(dev.ID) {
		t.Fatalf("the attached member launched with AO_CREW_ID=%q, want dev's id %q", got, dev.ID)
	}
	// And dev's own AO_CREW_ID was already right without a restart: a solo spawn
	// exports its OWN id, and after the attach the crew id IS its own id.
	if devRow.CrewID != devRow.ID {
		t.Fatalf("dev's crew id %q is not its own id %q, so its AO_CREW_ID is now stale", devRow.CrewID, devRow.ID)
	}

	// Asking for the member that is already running is a no-op, not an error and
	// not a stand-down of anybody.
	if _, err := s.mgr.WakeCrewMember(ctx, dev.ID); err != nil {
		t.Fatalf("start the member that is already running: %v", err)
	}
	s.assertBothAwake(t, dev.ID, qa.ID)
}

// TestCrewAttach_RefusesAFinishedTask goes through the SERVICE, because that is
// where a task's status is derived - a merged PR is a fact about PR rows, not
// about the session row, and the manager cannot see it.
func TestCrewAttach_RefusesAFinishedTask(t *testing.T) {
	ctx := context.Background()
	s := newCrewStack(t)
	dev, err := s.mgr.Spawn(ctx, ports.SpawnConfig{
		ProjectID: "mer", Kind: domain.KindWorker, Branch: "feature/task", Prompt: "rename the flag",
		TaskSize: domain.TaskSizeMechanical,
	})
	if err != nil {
		t.Fatalf("spawn dev: %v", err)
	}
	pr := domain.PullRequest{URL: "https://example.test/pr/1", SessionID: dev.ID, Number: 1, Merged: true, UpdatedAt: s.now}
	if err := s.store.WritePR(ctx, pr, nil, nil); err != nil {
		t.Fatalf("record the merge: %v", err)
	}

	if _, err := s.svc.AttachCrewMember(ctx, dev.ID, domain.CrewRoleQA, ""); err == nil {
		t.Fatal("a task whose PR has merged accepted a new crew member")
	}
	all, err := s.store.ListAllSessions(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 1 {
		t.Fatalf("a refused attach left %d rows, want 1", len(all))
	}
}

// TestCrewAttach_RefusesASecondQAEvenAgainstTheRealDatabase. The Go check runs
// under the crew lock; the partial unique index (0047) is what makes the rule a
// property of the DATA rather than of one process. Both are asserted here,
// because a test against a map store can only see the first.
func TestCrewAttach_RefusesASecondQAEvenAgainstTheRealDatabase(t *testing.T) {
	ctx := context.Background()
	s := newCrewStack(t)
	s.rt.perSessionHandles = true
	dev, qa := s.attachedCrew(t)

	if _, err := s.mgr.AttachCrewMember(ctx, dev.ID, domain.CrewRoleQA, ""); !errors.Is(err, sessionmanager.ErrCrewRoleTaken) {
		t.Fatalf("a second attach returned %v, want ErrCrewRoleTaken", err)
	}
	// The seat stays qa's even after it is stood down: `ao session restore` is how
	// it comes back, so the id that smoke_check and review_run rows name survives.
	if _, err := s.mgr.Teardown(ctx, qa.ID, domain.TerminationCauseKill); err != nil {
		t.Fatalf("stand qa down: %v", err)
	}
	if _, err := s.mgr.AttachCrewMember(ctx, dev.ID, domain.CrewRoleQA, ""); !errors.Is(err, sessionmanager.ErrCrewRoleTaken) {
		t.Fatalf("a replacement qa was accepted: %v", err)
	}
	// Writing the membership directly is refused by SQLite itself, which is the
	// invariant a racing caller cannot step around.
	stray, err := s.store.CreateSession(ctx, domain.SessionRecord{ProjectID: "mer", Kind: domain.KindWorker})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.store.SetSessionCrew(ctx, stray.ID, dev.ID, domain.CrewRoleQA, s.now); err == nil {
		t.Fatal("the database accepted a second qa on one crew; the partial unique index must refuse it")
	}
	// dev's tree is still standing through all of that.
	if !dirExists(t, s.record(t, dev.ID).Metadata.WorkspacePath) {
		t.Fatal("the refused attaches removed the shared worktree")
	}
}

// TestSolo_AttachingToNobodyChangesNothing is the hard requirement: a task nobody
// touches must behave exactly as it does today. Nothing here calls the attach at
// all - it asserts that the ROUTE existing costs a solo task nothing.
func TestSolo_AttachingToNobodyChangesNothing(t *testing.T) {
	ctx := context.Background()
	s := newCrewStack(t)
	rec, err := s.mgr.Spawn(ctx, ports.SpawnConfig{
		ProjectID: "mer", Kind: domain.KindWorker, Branch: "feature/untouched", Prompt: "small tweak",
		TaskSize: domain.TaskSizeMechanical,
	})
	if err != nil {
		t.Fatalf("spawn: %v", err)
	}
	all, err := s.store.ListAllSessions(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 1 {
		t.Fatalf("a mechanical spawn produced %d rows, want exactly 1", len(all))
	}
	if rec.InCrew() {
		t.Fatalf("a mechanical spawn produced a crew: crew=%q role=%q", rec.CrewID, rec.CrewRole)
	}
	// The whole lifetime, unchanged: the tree is its own, and its teardown frees it.
	res, err := s.mgr.Teardown(ctx, rec.ID, domain.TerminationCauseKill)
	if err != nil {
		t.Fatalf("teardown: %v", err)
	}
	if !res.Freed {
		t.Fatalf("a solo teardown did not free its worktree: %+v", res)
	}
	if dirExists(t, rec.Metadata.WorkspacePath) {
		t.Fatal("a solo worktree survived its own teardown")
	}
}

// attachedCrew is the state this feature creates: a mechanical task that was
// spawned solo, is still running, and has since been given a qa.
func (s *crewStack) attachedCrew(t *testing.T) (dev, qa domain.SessionRecord) {
	t.Helper()
	return s.attachedCrewWithStartFailure(t, nil)
}

// attachedCrewNeverStarted attaches a member whose LAUNCH fails. That is the one
// way a member can now exist without ever having run, and it is the state the
// message queue and the glance rule are about.
func (s *crewStack) attachedCrewNeverStarted(t *testing.T) (dev, qa domain.SessionRecord) {
	t.Helper()
	return s.attachedCrewWithStartFailure(t, errors.New("stub: no tmux for the new member"))
}

func (s *crewStack) attachedCrewWithStartFailure(t *testing.T, startErr error) (dev, qa domain.SessionRecord) {
	t.Helper()
	ctx := context.Background()
	devRec, err := s.mgr.Spawn(ctx, ports.SpawnConfig{
		ProjectID: "mer", Kind: domain.KindWorker, Branch: "feature/task", Prompt: "rename the flag",
		TaskSize: domain.TaskSizeMechanical,
	})
	if err != nil {
		t.Fatalf("spawn dev: %v", err)
	}
	// createErr fails exactly the next Create, which is the new member's launch.
	s.rt.createErr = startErr
	qaRec, err := s.mgr.AttachCrewMember(ctx, devRec.ID, domain.CrewRoleQA, "")
	if err != nil {
		t.Fatalf("attach qa: %v", err)
	}
	return s.record(t, devRec.ID), qaRec
}

// BOTH DIRECTIONS OF THE POLICY GATE, against the real store and the real
// service, because the two halves are the whole feature: an AO session is
// refused on a project that has turned automatic crew formation off, and a
// person on the same project, the same task and the same second still gets a qa.
//
// This is the failure it exists for. The flag was honoured by the code and
// defeated in practice for two days: workers whose brief hands the smoke
// checklist to qa found none, ran `ao crew add` themselves, and `ao crew add`
// deliberately skipped the eligibility test the flag is read at.
func TestCrewAttach_CrewOffProjectRefusesAnAgentAndServesAHuman(t *testing.T) {
	ctx := context.Background()
	s := newCrewStack(t)
	s.rt.perSessionHandles = true
	crewOffProject(t, s)

	dev, err := s.mgr.Spawn(ctx, ports.SpawnConfig{
		ProjectID: "mer", Kind: domain.KindWorker, Branch: "feature/task", Prompt: "rename the flag",
		TaskSize: domain.TaskSizeMechanical,
	})
	if err != nil {
		t.Fatalf("spawn dev: %v", err)
	}

	// THE AGENT. `ao crew add` sends $AO_SESSION_ID, so the call names a session
	// and is refused - and leaves nothing behind.
	_, err = s.svc.AttachCrewMember(ctx, dev.ID, domain.CrewRoleQA, dev.ID)
	if err == nil {
		t.Fatal("an AO session attached a qa on a project that forms no crews")
	}
	var apiErr *apierr.Error
	if !errors.As(err, &apiErr) || apiErr.Code != "CREW_AUTO_FORMATION_OFF" {
		t.Fatalf("refusal = %v, want a CREW_AUTO_FORMATION_OFF api error", err)
	}
	all, err := s.store.ListAllSessions(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 1 {
		t.Fatalf("a refused attach left %d rows, want 1", len(all))
	}

	// THE HUMAN. Same project, same task, no session id: the escape hatch the flag
	// was designed around is untouched, and the qa arrives working.
	qa, err := s.svc.AttachCrewMember(ctx, dev.ID, domain.CrewRoleQA, "")
	if err != nil {
		t.Fatalf("a human could not add a qa by hand on a crew-off project: %v", err)
	}
	if qa.CrewRole != domain.CrewRoleQA || qa.CrewID != dev.ID {
		t.Fatalf("manual qa role=%q crew=%q, want qa/%s", qa.CrewRole, qa.CrewID, dev.ID)
	}
	if qa.CrewJoinReason != domain.CrewJoinManual {
		t.Fatalf("manual qa join reason = %q, want %q", qa.CrewJoinReason, domain.CrewJoinManual)
	}
}

// crewOffProject turns AUTOMATIC crew formation off for the stack's project,
// leaving everything else about it as newCrewStack wrote it.
func crewOffProject(t *testing.T, s *crewStack) {
	t.Helper()
	ctx := context.Background()
	rec, ok, err := s.store.GetProject(ctx, "mer")
	if err != nil || !ok {
		t.Fatalf("GetProject: %v (found=%v)", err, ok)
	}
	rec.Config.DisableAutoCrew = true
	if err := s.store.UpsertProject(ctx, rec); err != nil {
		t.Fatalf("UpsertProject: %v", err)
	}
}

// THE SAME TWO CALLS OVER THE WIRE, through the real router, so the seam the
// incident actually ran through is the one under test: `ao crew add` reaches the
// daemon as this POST and nothing else, and the desktop app's `+ qa` reaches it
// as the same POST without a `from`. If the field were dropped anywhere between
// the route and the manager, every agent would look like a human again and the
// unit tests below would still pass.
func TestCrewAttachHTTP_CrewOffProjectRefusesAnAgentAndServesAHuman(t *testing.T) {
	ctx := context.Background()
	s := newCrewStack(t)
	s.rt.perSessionHandles = true
	crewOffProject(t, s)
	srv := writeRouterFor(t, s)

	dev, err := s.mgr.Spawn(ctx, ports.SpawnConfig{
		ProjectID: "mer", Kind: domain.KindWorker, Branch: "feature/task", Prompt: "rename the flag",
		TaskSize: domain.TaskSizeMechanical,
	})
	if err != nil {
		t.Fatalf("spawn dev: %v", err)
	}
	url := srv.URL + "/api/v1/sessions/" + string(dev.ID) + "/crew/members"

	// What `ao crew add` sends from inside a session.
	body, status := postJSONString(t, srv, url, `{"role":"qa","from":"`+string(dev.ID)+`"}`)
	if status != http.StatusConflict {
		t.Fatalf("an agent's attach = %d, want 409; body=%s", status, body)
	}
	if !strings.Contains(body, "CREW_AUTO_FORMATION_OFF") {
		t.Fatalf("refusal body = %s, want CREW_AUTO_FORMATION_OFF", body)
	}
	// Every sentence a caller needs to stop looking for a way round it.
	for _, want := range []string{"Never form a crew automatically", "person", "+ qa"} {
		if !strings.Contains(body, want) {
			t.Fatalf("refusal body does not mention %q: %s", want, body)
		}
	}

	// What the desktop app's `+ qa` sends, and what a person's shell sends.
	body, status = postJSONString(t, srv, url, `{"role":"qa"}`)
	if status != http.StatusCreated {
		t.Fatalf("a human's attach = %d, want 201; body=%s", status, body)
	}
	if !strings.Contains(body, `"role":"qa"`) {
		t.Fatalf("the human's attach did not return a qa: %s", body)
	}
}

// postJSONString posts a raw body and returns the response body and status.
func postJSONString(t *testing.T, srv *httptest.Server, url, body string) (string, int) {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, url, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw), resp.StatusCode
}
