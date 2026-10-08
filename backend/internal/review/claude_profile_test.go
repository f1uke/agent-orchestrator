package review

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

var errBrokenProfile = errors.New("broken profile")

func newProfileEngine(cfg domain.ProjectConfig, launcher *fakeLauncher, store *fakeStore) (*Engine, *[]string) {
	var asked []string
	eng := New(Deps{
		Store: store, Sessions: fakeSessions{rec: liveWorker(), ok: true}, PRs: prAt("sha1"), Projects: fakeProjects{cfg: cfg}, Launcher: launcher,
		Clock: func() time.Time { return time.Unix(0, 0).UTC() },
		ClaudeSettings: func(profile string) (string, error) {
			asked = append(asked, profile)
			if profile == "Broken" {
				return "", errBrokenProfile
			}
			if profile == "Work" {
				return "/home/u/work.json", nil
			}
			return "", nil
		},
	})
	return eng, &asked
}

func TestTriggerLaunchesAClaudeReviewerOnTheProjectProfile(t *testing.T) {
	launcher := &fakeLauncher{handle: "review-mer-1"}
	eng, asked := newProfileEngine(domain.ProjectConfig{ClaudeProfile: "Work"}, launcher, &fakeStore{})

	if _, err := eng.Trigger(context.Background(), "mer-1"); err != nil {
		t.Fatal(err)
	}
	if launcher.gotSpec.ClaudeSettingsFile != "/home/u/work.json" {
		t.Fatalf("reviewer settings file = %q, want the project profile's", launcher.gotSpec.ClaudeSettingsFile)
	}
	if len(*asked) != 1 || (*asked)[0] != "Work" {
		t.Fatalf("profiles resolved = %v, want [Work]", *asked)
	}
}

func TestTriggerWithAnUnusableProfileFailsTheRunWithoutLaunching(t *testing.T) {
	launcher := &fakeLauncher{handle: "review-mer-1"}
	store := &fakeStore{}
	eng, _ := newProfileEngine(domain.ProjectConfig{ClaudeProfile: "Broken"}, launcher, store)

	if _, err := eng.Trigger(context.Background(), "mer-1"); !errors.Is(err, errBrokenProfile) {
		t.Fatalf("Trigger = %v, want the profile error", err)
	}
	if launcher.spawned {
		t.Fatal("a reviewer launched on a profile AO could not use")
	}
	if len(store.runs) != 1 || store.runs[0].Status != domain.ReviewRunFailed {
		t.Fatalf("runs = %+v, want one failed run", store.runs)
	}
}

func TestLauncherSpawnHandsTheReviewerItsSettingsFile(t *testing.T) {
	reviewer := &fakeReviewer{}
	l := NewLauncher(fakeReviewerResolver{reviewer: reviewer, ok: true}, &fakeRuntime{}, "")
	spec := launchSpec()
	spec.ClaudeSettingsFile = "/home/u/work.json"
	if _, err := l.Spawn(context.Background(), spec); err != nil {
		t.Fatal(err)
	}
	if reviewer.gotInv.SettingsFile != "/home/u/work.json" {
		t.Fatalf("invocation settings file = %q, want /home/u/work.json", reviewer.gotInv.SettingsFile)
	}
}

func TestTriggerDoesNotResolveAProfileForANonClaudeReviewer(t *testing.T) {
	launcher := &fakeLauncher{handle: "review-mer-1"}
	cfg := domain.ProjectConfig{ClaudeProfile: "Broken", Reviewers: []domain.ReviewerConfig{{Harness: domain.ReviewerHarness("greptile")}}}
	eng, asked := newProfileEngine(cfg, launcher, &fakeStore{})

	if _, err := eng.Trigger(context.Background(), "mer-1"); err != nil {
		t.Fatalf("a greptile reviewer failed on a Claude profile it does not use: %v", err)
	}
	if len(*asked) != 0 || launcher.gotSpec.ClaudeSettingsFile != "" {
		t.Fatalf("resolved %v, settings %q; want nothing for greptile", *asked, launcher.gotSpec.ClaudeSettingsFile)
	}
}
