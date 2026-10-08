package branchfollow

import (
	"context"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/session"
)

type sessionList []domain.SessionRecord

func (l sessionList) ListAllSessions(context.Context) ([]domain.SessionRecord, error) { return l, nil }

type recordingFollower struct{ seen []domain.SessionID }

func (f *recordingFollower) FollowWorktreeBranch(_ context.Context, rec domain.SessionRecord) (session.BranchFollow, error) {
	f.seen = append(f.seen, rec.ID)
	return session.BranchFollow{Outcome: session.BranchInSync}, nil
}

func TestPollComparesOnlyLiveSessions(t *testing.T) {
	f := &recordingFollower{}
	o := New(sessionList{
		{ID: "live"},
		{ID: "ended", IsTerminated: true},
		{ID: "queued", IsTodo: true},
	}, f, Config{})
	if err := o.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(f.seen) != 1 || f.seen[0] != "live" {
		t.Fatalf("compared %v, want only the live session", f.seen)
	}
}
