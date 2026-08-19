package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
)

// queueSeed registers a project and a session to hang queued messages off.
func queueSeed(t *testing.T, s *sqlite.Store, project string) domain.SessionRecord {
	t.Helper()
	seedProject(t, s, project)
	rec, err := s.CreateSession(context.Background(), sampleRecord(project))
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	return rec
}

func enqueue(t *testing.T, s *sqlite.Store, id domain.SessionID, body string, at time.Time, limit int) domain.QueuedMessage {
	t.Helper()
	msg, _, err := s.EnqueueSessionMessage(context.Background(), domain.QueuedMessage{
		SessionID: id, Body: body, QueuedAt: at, ExpiresAt: at.Add(time.Hour),
	}, limit)
	if err != nil {
		t.Fatalf("enqueue %q: %v", body, err)
	}
	return msg
}

func bodies(msgs []domain.QueuedMessage) []string {
	out := make([]string, 0, len(msgs))
	for _, m := range msgs {
		out = append(out, m.Body)
	}
	return out
}

func TestQueuedMessagesKeepInsertionOrder(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	rec := queueSeed(t, s, "que")
	now := time.Now().UTC().Truncate(time.Second)

	// Deliberately DECREASING wall-clock stamps: ordering must come from the
	// insertion sequence, not from queued_at, or a clock step would shuffle an
	// inbox.
	enqueue(t, s, rec.ID, "first", now, 0)
	enqueue(t, s, rec.ID, "second", now.Add(-time.Minute), 0)
	enqueue(t, s, rec.ID, "third", now.Add(-2*time.Minute), 0)

	got, err := s.ListPendingQueuedMessages(ctx, rec.ID)
	if err != nil {
		t.Fatalf("list pending: %v", err)
	}
	want := []string{"first", "second", "third"}
	if diff := bodies(got); len(diff) != 3 || diff[0] != want[0] || diff[1] != want[1] || diff[2] != want[2] {
		t.Fatalf("pending order = %v, want %v", diff, want)
	}
}

func TestQueuedMessageOrderSurvivesDeliveryOfEarlierRows(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	rec := queueSeed(t, s, "que")
	now := time.Now().UTC().Truncate(time.Second)

	first := enqueue(t, s, rec.ID, "first", now, 0)
	second := enqueue(t, s, rec.ID, "second", now, 0)
	// Deliver both, then queue a new one: SQLite may reuse the freed rowid, and
	// the new message must still sort AFTER anything still waiting.
	if err := s.DeleteQueuedMessage(ctx, first.ID); err != nil {
		t.Fatalf("delete first: %v", err)
	}
	third := enqueue(t, s, rec.ID, "third", now, 0)
	if third.ID <= second.ID {
		t.Fatalf("new message id %d must sort after the still-pending %d", third.ID, second.ID)
	}
	got, err := s.ListPendingQueuedMessages(ctx, rec.ID)
	if err != nil {
		t.Fatalf("list pending: %v", err)
	}
	if b := bodies(got); len(b) != 2 || b[0] != "second" || b[1] != "third" {
		t.Fatalf("pending = %v, want [second third]", b)
	}
}

func TestClaimQueuedMessageWinsOnlyOnce(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	rec := queueSeed(t, s, "que")
	now := time.Now().UTC().Truncate(time.Second)
	msg := enqueue(t, s, rec.ID, "only once", now, 0)

	won, err := s.ClaimQueuedMessage(ctx, msg.ID, now)
	if err != nil || !won {
		t.Fatalf("first claim = (%v, %v), want (true, nil)", won, err)
	}
	// The second claimer must lose: this is the delivered-once guard, and it is
	// a plain false rather than an error.
	won, err = s.ClaimQueuedMessage(ctx, msg.ID, now)
	if err != nil {
		t.Fatalf("second claim errored: %v", err)
	}
	if won {
		t.Fatal("second claim won too: a claimed message could be delivered twice")
	}
	// A claimed row is no longer pending, so a second deliverer listing work
	// cannot even see it.
	pending, err := s.ListPendingQueuedMessages(ctx, rec.ID)
	if err != nil {
		t.Fatalf("list pending: %v", err)
	}
	if len(pending) != 0 {
		t.Fatalf("claimed message still listed as pending: %v", bodies(pending))
	}
}

func TestEnqueueCapEvictsOldestAndKeepsTheNewest(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	rec := queueSeed(t, s, "que")
	now := time.Now().UTC().Truncate(time.Second)

	for _, body := range []string{"one", "two", "three"} {
		enqueue(t, s, rec.ID, body, now, 2)
	}
	pending, err := s.ListPendingQueuedMessages(ctx, rec.ID)
	if err != nil {
		t.Fatalf("list pending: %v", err)
	}
	if b := bodies(pending); len(b) != 2 || b[0] != "two" || b[1] != "three" {
		t.Fatalf("capped inbox = %v, want [two three] (oldest evicted, newest kept)", b)
	}
	// The reported count must reflect the cap, not the pre-eviction total.
	_, reported, err := s.EnqueueSessionMessage(ctx, domain.QueuedMessage{
		SessionID: rec.ID, Body: "four", QueuedAt: now, ExpiresAt: now.Add(time.Hour),
	}, 2)
	if err != nil {
		t.Fatalf("enqueue four: %v", err)
	}
	if reported != 2 {
		t.Fatalf("reported pending = %d, want 2 (the cap)", reported)
	}
}

func TestExpirePendingQueuedMessagesDropsOnlyStaleOnes(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	rec := queueSeed(t, s, "que")
	now := time.Now().UTC().Truncate(time.Second)

	stale, _, err := s.EnqueueSessionMessage(ctx, domain.QueuedMessage{
		SessionID: rec.ID, Body: "stale", QueuedAt: now.Add(-2 * time.Hour), ExpiresAt: now.Add(-time.Hour),
	}, 0)
	if err != nil {
		t.Fatalf("enqueue stale: %v", err)
	}
	enqueue(t, s, rec.ID, "fresh", now, 0)

	dropped, err := s.ExpirePendingQueuedMessages(ctx, now)
	if err != nil {
		t.Fatalf("expire: %v", err)
	}
	if len(dropped) != 1 || dropped[0].ID != stale.ID {
		t.Fatalf("dropped = %v, want just the stale one", bodies(dropped))
	}
	pending, err := s.ListPendingQueuedMessages(ctx, rec.ID)
	if err != nil {
		t.Fatalf("list pending: %v", err)
	}
	if b := bodies(pending); len(b) != 1 || b[0] != "fresh" {
		t.Fatalf("pending after expiry = %v, want [fresh]", b)
	}
}

func TestFailDeliveringQueuedMessagesLeavesPendingAlone(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	rec := queueSeed(t, s, "que")
	now := time.Now().UTC().Truncate(time.Second)
	inflight := enqueue(t, s, rec.ID, "in flight", now, 0)
	enqueue(t, s, rec.ID, "still waiting", now, 0)
	if won, err := s.ClaimQueuedMessage(ctx, inflight.ID, now); err != nil || !won {
		t.Fatalf("claim: (%v, %v)", won, err)
	}

	n, err := s.FailDeliveringQueuedMessages(ctx, "daemon restarted", now)
	if err != nil {
		t.Fatalf("fail delivering: %v", err)
	}
	if n != 1 {
		t.Fatalf("failed %d rows, want 1 (only the in-flight one)", n)
	}
	all, err := s.ListQueuedMessages(ctx, rec.ID)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("inbox = %d rows, want 2 (a failed row is KEPT so the drop is visible)", len(all))
	}
	if all[0].State != domain.QueuedMessageFailed || all[0].LastError == "" {
		t.Fatalf("in-flight row = %+v, want failed with a reason", all[0])
	}
	if all[1].State != domain.QueuedMessagePending {
		t.Fatalf("waiting row = %s, want still pending", all[1].State)
	}
}

func TestQueuedMessagesSurviveAReopen(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()

	first, err := sqlite.Open(dir)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	rec := queueSeed(t, first, "que")
	now := time.Now().UTC().Truncate(time.Second)
	enqueue(t, first, rec.ID, "held across the restart", now, 0)
	enqueue(t, first, rec.ID, "and this one too", now, 0)
	if err := first.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	// A new daemon on the same data dir: the inbox is exactly as it was. A queue
	// that emptied here would lose precisely the messages a long sleep produced.
	second, err := sqlite.Open(dir)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	t.Cleanup(func() { _ = second.Close() })
	pending, err := second.ListPendingQueuedMessages(ctx, rec.ID)
	if err != nil {
		t.Fatalf("list pending after reopen: %v", err)
	}
	if b := bodies(pending); len(b) != 2 || b[0] != "held across the restart" || b[1] != "and this one too" {
		t.Fatalf("pending after reopen = %v, want both messages in order", b)
	}
	ids, err := second.SessionsWithPendingMessages(ctx)
	if err != nil {
		t.Fatalf("sessions with pending: %v", err)
	}
	if len(ids) != 1 || ids[0] != rec.ID {
		t.Fatalf("sessions with pending = %v, want [%s]", ids, rec.ID)
	}
}

func TestSessionQueuedMessageCountsSplitsPendingFromFailed(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	rec := queueSeed(t, s, "que")
	now := time.Now().UTC().Truncate(time.Second)
	doomed := enqueue(t, s, rec.ID, "doomed", now, 0)
	enqueue(t, s, rec.ID, "waiting", now, 0)
	if err := s.FailQueuedMessage(ctx, doomed.ID, "gave up", now); err != nil {
		t.Fatalf("fail: %v", err)
	}

	counts, err := s.SessionQueuedMessageCounts(ctx, rec.ID)
	if err != nil {
		t.Fatalf("counts: %v", err)
	}
	if counts.Pending != 1 || counts.Failed != 1 {
		t.Fatalf("counts = %+v, want {Pending:1 Failed:1}", counts)
	}
	// A session with an empty inbox reports zeroes rather than an error, so the
	// read model can call this for every session.
	empty, err := s.SessionQueuedMessageCounts(ctx, "nobody")
	if err != nil {
		t.Fatalf("counts for an unknown session: %v", err)
	}
	if empty.Pending != 0 || empty.Failed != 0 {
		t.Fatalf("empty counts = %+v, want zeroes", empty)
	}
}

func TestDeletingASessionTakesItsInboxWithIt(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	rec := queueSeed(t, s, "que")
	now := time.Now().UTC().Truncate(time.Second)
	enqueue(t, s, rec.ID, "orphan candidate", now, 0)

	n, err := s.PurgeSessionMessages(ctx, rec.ID)
	if err != nil {
		t.Fatalf("purge: %v", err)
	}
	if n != 1 {
		t.Fatalf("purged %d, want 1", n)
	}
	all, err := s.ListQueuedMessages(ctx, rec.ID)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(all) != 0 {
		t.Fatalf("inbox = %d rows after purge, want 0", len(all))
	}
}
