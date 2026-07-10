package sessionmanager

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
)

func quietReaper(list func(context.Context, string) ([]int, error), kill func(int) error, self int) *strayReaper {
	return &strayReaper{
		listCwdPIDs: list,
		kill:        kill,
		self:        self,
		log:         slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
}

func TestStrayReaper_KillsProcessesUnderWorktree(t *testing.T) {
	var killed []int
	r := quietReaper(
		func(_ context.Context, dir string) ([]int, error) { return []int{4321}, nil },
		func(pid int) error { killed = append(killed, pid); return nil },
		999,
	)
	r.reap(context.Background(), "/home/u/.ao/data/worktrees/proj/feat")
	if len(killed) != 1 || killed[0] != 4321 {
		t.Fatalf("killed = %v, want [4321]", killed)
	}
}

func TestStrayReaper_RefusesNonManagedPath(t *testing.T) {
	called := false
	r := quietReaper(
		func(_ context.Context, _ string) ([]int, error) { called = true; return nil, nil },
		func(int) error { return nil },
		999,
	)
	// Not under a "worktrees" segment: the reaper must not even list processes.
	r.reap(context.Background(), "/home/u/some/random/dir")
	if called {
		t.Fatal("must not scan a non-managed path")
	}
	// Relative paths are refused too.
	r.reap(context.Background(), "worktrees/x")
	if called {
		t.Fatal("must not scan a relative path")
	}
}

func TestStrayReaper_NeverKillsSelfOrInit(t *testing.T) {
	var killed []int
	r := quietReaper(
		func(_ context.Context, _ string) ([]int, error) { return []int{1, 999, 4321}, nil },
		func(pid int) error { killed = append(killed, pid); return nil },
		999,
	)
	r.reap(context.Background(), "/x/worktrees/y")
	if len(killed) != 1 || killed[0] != 4321 {
		t.Fatalf("killed = %v, want only [4321] (skip pid 1 and self 999)", killed)
	}
}

func TestStrayReaper_ListErrorIsSwallowed(t *testing.T) {
	r := quietReaper(
		func(_ context.Context, _ string) ([]int, error) { return nil, errors.New("lsof boom") },
		func(int) error { t.Fatal("must not kill when listing failed"); return nil },
		999,
	)
	r.reap(context.Background(), "/x/worktrees/y") // must not panic
}

func TestStrayReaper_NilIsNoop(t *testing.T) {
	var r *strayReaper
	r.reap(context.Background(), "/x/worktrees/y") // must not panic
	r2 := &strayReaper{}                           // listCwdPIDs nil
	r2.reap(context.Background(), "/x/worktrees/y")
}

func TestParseLsofCwd(t *testing.T) {
	out := "p100\nn/x/worktrees/proj/feat\np200\nn/somewhere/else\np300\nn/x/worktrees/proj/feat/node_modules/.bin\n"
	got := parseLsofCwd(out, "/x/worktrees/proj/feat")
	if len(got) != 2 || got[0] != 100 || got[1] != 300 {
		t.Fatalf("pids = %v, want [100 300]", got)
	}
}

func TestPathAtOrUnder(t *testing.T) {
	root := "/x/worktrees/p"
	if !pathAtOrUnder(root, root) {
		t.Fatal("equal path should match")
	}
	if !pathAtOrUnder("/x/worktrees/p/sub", root) {
		t.Fatal("child path should match")
	}
	if pathAtOrUnder("/x/worktrees/proj2", root) {
		t.Fatal("sibling prefix must NOT match")
	}
}
