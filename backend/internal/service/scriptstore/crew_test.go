package scriptstore_test

import (
	"errors"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/scriptstore"
)

func TestACrewMemberReadsAndPublishesItsOwnersWorktree(t *testing.T) {
	f := newFixture(t)
	dev := f.session()
	qa := f.session()
	now := time.Now().UTC()
	for _, m := range []struct {
		id   domain.SessionID
		role domain.CrewRole
	}{{dev.ID, domain.CrewRoleDev}, {qa.ID, domain.CrewRoleQA}} {
		if _, err := f.db.SetSessionCrew(f.ctx, m.id, dev.ID, m.role, now); err != nil {
			t.Fatal(err)
		}
	}
	w := f.ensure(dev.ID)
	f.commit(w.Path, "projects/nter/a.yaml", "a\n")

	st, err := f.svc.Status(f.ctx, qa.ID)
	if err != nil || st.Worktree.SessionID != dev.ID || st.Worktree.Unpublished != 1 {
		t.Fatalf("qa status = %+v, %v; want dev's worktree", st, err)
	}
	out, err := f.svc.Publish(f.ctx, qa.ID)
	if err != nil || out.Result.Outcome != ports.PublishFastForward {
		t.Fatalf("qa publish = %+v, %v; want dev's commits published", out, err)
	}
	if _, err := f.svc.Status(f.ctx, "nter-404"); !errors.Is(err, scriptstore.ErrSessionNotFound) {
		t.Fatalf("status of an unknown session = %v, want ErrSessionNotFound", err)
	}
}
