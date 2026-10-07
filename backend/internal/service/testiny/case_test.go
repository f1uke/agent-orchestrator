package testiny

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	testinyadapter "github.com/aoagents/agent-orchestrator/backend/internal/adapters/testiny"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func (f *fakeTestiny) Case(_ context.Context, id int64) (domain.TestinyCaseDetail, error) {
	if err := f.note(fmt.Sprintf("case view %d", id)); err != nil {
		return domain.TestinyCaseDetail{}, err
	}
	return domain.TestinyCaseDetail{ID: id, Title: fmt.Sprintf("case %d", id), TestData: "qa@example.com / fake-password"}, nil
}

func (f *fakeTestiny) addCase(run domain.TestinyRunID, id int64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	res := f.results[run]
	res.Cases = append(append([]testinyadapter.Case(nil), res.Cases...), testinyadapter.Case{ID: id, Status: "NOTRUN"})
	f.results[run] = res
}

func TestCaseReadsACaseInALinkedRunAndKeepsItAMinute(t *testing.T) {
	r := newRig(t, on)
	link(r, 625, 632)
	ctx := context.Background()
	got, err := r.svc.Case(ctx, "app-1", 7167)
	if err != nil {
		t.Fatalf("Case: %v", err)
	}
	if got.ID != 7167 || got.TestData != "qa@example.com / fake-password" {
		t.Fatalf("Case = %+v", got)
	}
	r.clock.advance(59 * time.Second)
	if _, err := r.svc.Case(ctx, "app-1", 7167); err != nil {
		t.Fatal(err)
	}
	if n := r.tny.count("case view"); n != 1 {
		t.Fatalf("case view ran %d times within a minute, want 1", n)
	}
	r.clock.advance(2 * time.Second)
	if _, err := r.svc.Case(ctx, "app-1", 7167); err != nil {
		t.Fatal(err)
	}
	if n := r.tny.count("case view"); n != 2 {
		t.Fatalf("an expired case was served: case view ran %d times", n)
	}
}

func TestCaseRefusesACaseNotInTheTasksRuns(t *testing.T) {
	r := newRig(t, on)
	r.store.sessions["app-2"] = domain.SessionRecord{ID: "app-2", ProjectID: "app"}
	r.store.links = append(r.store.links, domain.TestinyRunLink{SessionID: "app-2", RunID: 625})
	link(r, 632)
	ctx := context.Background()
	for _, id := range []int64{3818, 424242} {
		if _, err := r.svc.Case(ctx, "app-1", id); !errors.Is(err, ErrCaseNotInTask) {
			t.Fatalf("Case(%d) err = %v, want ErrCaseNotInTask", id, err)
		}
	}
	if n := r.tny.count("case view"); n != 0 {
		t.Fatalf("a case outside the task was read from Testiny %d times", n)
	}
	if _, err := r.svc.Case(ctx, "app-2", 3818); err != nil {
		t.Fatalf("the same case on the task whose run has it: %v", err)
	}
}

func TestCaseRereadsARunOnlyWhenItsReadIsOld(t *testing.T) {
	r := newRig(t, on)
	link(r, 632)
	ctx := context.Background()
	if _, err := r.svc.Runs(ctx, "app-1", false); err != nil {
		t.Fatal(err)
	}
	r.tny.addCase(632, 7200)
	if _, err := r.svc.Case(ctx, "app-1", 7200); !errors.Is(err, ErrCaseNotInTask) {
		t.Fatalf("err = %v, want ErrCaseNotInTask from a read made just now", err)
	}
	r.clock.advance(16 * time.Second)
	if _, err := r.svc.Case(ctx, "app-1", 7200); err != nil {
		t.Fatalf("a case added since an old read: %v", err)
	}
	runs, err := r.svc.Runs(ctx, "app-1", false)
	if err != nil {
		t.Fatal(err)
	}
	if n := len(runs.Runs[0].Cases); n != 3 {
		t.Fatalf("the tab still shows %d cases; the read Case made should be remembered", n)
	}
}

func TestCaseSaysTestinyFailedRatherThanNotInTask(t *testing.T) {
	r := newRig(t, on)
	link(r, 632)
	r.tny.setFail(fmt.Errorf("%w: connection refused", testinyadapter.ErrUnavailable))
	if _, err := r.svc.Case(context.Background(), "app-1", 7166); !errors.Is(err, testinyadapter.ErrUnavailable) {
		t.Fatalf("err = %v, want ErrUnavailable", err)
	}
}

func TestCaseAnswersOffAndUnknownSession(t *testing.T) {
	r := newRig(t, domain.ProjectConfig{})
	if _, err := r.svc.Case(context.Background(), "app-1", 7166); !errors.Is(err, ErrOff) {
		t.Fatalf("err = %v, want ErrOff", err)
	}
	if _, err := r.svc.Case(context.Background(), "ghost-1", 7166); !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("err = %v, want ErrSessionNotFound", err)
	}
}
