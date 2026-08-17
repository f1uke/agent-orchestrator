package sessionmanager

import (
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// managerWithLCM builds a manager alongside the fake lifecycle recorder, so a
// test can read back WHICH cause a teardown named.
func managerWithLCM() (*Manager, *fakeStore, *fakeLCM) {
	st := newFakeStore()
	st.projects["mer"] = domain.ProjectRecord{ID: "mer", Config: testRoleAgents()}
	lcm := &fakeLCM{store: st}
	m := New(Deps{
		Runtime: &fakeRuntime{}, Agents: fakeAgents{}, Workspace: &fakeWorkspace{}, Store: st,
		Messenger: &fakeMessenger{}, Lifecycle: lcm,
		LookPath: func(string) (string, error) { return "/bin/true", nil },
	})
	return m, st, lcm
}

// Kill and the auto-reclaim loop share one teardown path, which is exactly why
// the record has to distinguish them. Without a cause travelling through, both
// leave the same anonymous "exited" and "did the reclaimer take my worker?"
// becomes unanswerable — the question this whole branch exists to answer.
func TestTeardown_RecordsTheOperationThatOrderedIt(t *testing.T) {
	cases := []struct {
		name string
		run  func(m *Manager) error
		want string
	}{
		{
			name: "an explicit kill",
			run:  func(m *Manager) error { _, err := m.Kill(ctx, "mer-1"); return err },
			want: domain.TerminationCauseKill,
		},
		{
			name: "the auto-reclaim loop",
			run: func(m *Manager) error {
				_, err := m.Teardown(ctx, "mer-1", domain.TerminationCauseAutoReclaim)
				return err
			},
			want: domain.TerminationCauseAutoReclaim,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m, st, lcm := managerWithLCM()
			st.sessions["mer-1"] = mkLive("mer-1")

			if err := tc.run(m); err != nil {
				t.Fatalf("teardown: %v", err)
			}
			got := lcm.terminationCauses["mer-1"]
			if len(got) != 1 || got[0] != tc.want {
				t.Fatalf("causes = %v, want [%s]", got, tc.want)
			}
		})
	}
}

// A teardown REFUSED because the worktree holds work never reaches the terminal
// write at all, so it records no cause — the session is still live and must not
// wear an account of an ending that did not happen.
func TestTeardown_RefusedTeardownRecordsNoCause(t *testing.T) {
	m, st, lcm := managerWithLCM()
	st.sessions["mer-1"] = mkLive("mer-1")
	m.workspace.(*fakeWorkspace).destroyErr = ports.ErrWorkspaceDirty

	if _, err := m.Teardown(ctx, "mer-1", domain.TerminationCauseAutoReclaim); err != nil {
		t.Fatalf("a refusal is not an error: %v", err)
	}
	if got := lcm.terminationCauses["mer-1"]; len(got) != 0 {
		t.Fatalf("causes = %v, want none for a preserved workspace", got)
	}
}
