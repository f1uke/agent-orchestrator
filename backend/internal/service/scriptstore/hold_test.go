package scriptstore_test

import (
	"reflect"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/scriptstore"
)

// A worktree kept over uncommitted files AND a refused publish names both,
// so the board's tooltip lists every file a person has to deal with.
func TestKeepNamesEveryFileTheHoldIsAbout(t *testing.T) {
	f := newFixture(t)
	rec := f.session()
	w := f.ensure(rec.ID)
	f.commit(w.Path, "projects/nter/login.yaml", "session\n")
	f.commit(f.scripts, "projects/nter/login.yaml", "person\n")
	f.write(w.Path, "projects/nter/draft.yaml", "draft\n")
	if _, err := f.svc.Settle(f.ctx, rec.ID, scriptstore.SettleKeep); err != nil {
		t.Fatal(err)
	}
	row := f.row(rec.ID)
	want := []string{"projects/nter/draft.yaml", "projects/nter/login.yaml"}
	if row.HeldReason != domain.HoldUncommitted || !reflect.DeepEqual(row.HeldFiles, want) {
		t.Fatalf("row = %+v, want held over the draft, naming the draft and the conflict %v", row, want)
	}
}
