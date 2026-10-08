package claudeprofile

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestList_BuiltinsFirstThenUserProfiles(t *testing.T) {
	st, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetUser([]Profile{{Name: "Work", SettingsFile: "/etc/claude/work.json"}}); err != nil {
		t.Fatal(err)
	}
	got := st.List()
	want := []Profile{
		{Name: "Subscription", Builtin: true},
		{Name: "OmniRoute", SettingsFile: "~/.claude/settings-omniroute.json", Builtin: true},
		{Name: "Work", SettingsFile: "/etc/claude/work.json"},
	}
	if len(got) != len(want) {
		t.Fatalf("List() = %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("List()[%d] = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestResolve_CaseInsensitiveCanonicalAndEmptyIsSubscription(t *testing.T) {
	st, _ := NewStore(t.TempDir())
	_ = st.SetUser([]Profile{{Name: "Work", SettingsFile: "/w.json"}})
	cases := map[string]string{
		"":             "Subscription",
		"  ":           "Subscription",
		"subscription": "Subscription",
		"OMNIROUTE":    "OmniRoute",
		"work":         "Work",
	}
	for in, want := range cases {
		p, ok := st.Resolve(in)
		if !ok || p.Name != want {
			t.Errorf("Resolve(%q) = %+v, %v; want %s", in, p, ok, want)
		}
	}
	if _, ok := st.Resolve("nope"); ok {
		t.Error("Resolve(nope) resolved; want unknown")
	}
}

func TestLookup_UnknownNamesTheKnownProfiles(t *testing.T) {
	st, _ := NewStore(t.TempDir())
	_, err := st.Lookup("nope")
	if !errors.Is(err, ErrUnknownProfile) {
		t.Fatalf("Lookup(nope) err = %v, want ErrUnknownProfile", err)
	}
	var unknown *UnknownProfileError
	if !errors.As(err, &unknown) || strings.Join(unknown.Known, ",") != "Subscription,OmniRoute" {
		t.Fatalf("unknown error = %+v, want Known [Subscription OmniRoute]", unknown)
	}
	if !strings.Contains(err.Error(), "Subscription") || !strings.Contains(err.Error(), "OmniRoute") {
		t.Fatalf("message %q does not list the known profiles", err)
	}
}

func TestSetUser_PersistsAndReloads(t *testing.T) {
	dir := t.TempDir()
	st, _ := NewStore(dir)
	if err := st.SetUser([]Profile{{Name: "Work", SettingsFile: "~/work.json", Builtin: true}}); err != nil {
		t.Fatal(err)
	}
	st2, err := NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	p, ok := st2.Resolve("work")
	if !ok || p.SettingsFile != "~/work.json" || p.Builtin {
		t.Fatalf("reloaded Resolve(work) = %+v, %v; want the user profile, not builtin", p, ok)
	}
}

func TestSetUser_RejectsInvalidProfilesAndKeepsTheStoredOnes(t *testing.T) {
	cases := map[string][]Profile{
		"empty name":         {{Name: " ", SettingsFile: "/a.json"}},
		"duplicate name":     {{Name: "A", SettingsFile: "/a.json"}, {Name: "a", SettingsFile: "/b.json"}},
		"shadows builtin":    {{Name: "omniroute", SettingsFile: "/a.json"}},
		"empty file":         {{Name: "A"}},
		"relative file":      {{Name: "A", SettingsFile: "settings.json"}},
		"tilde without user": {{Name: "A", SettingsFile: "~bob/settings.json"}},
	}
	for name, profiles := range cases {
		t.Run(name, func(t *testing.T) {
			st, _ := NewStore(t.TempDir())
			_ = st.SetUser([]Profile{{Name: "Kept", SettingsFile: "/k.json"}})
			if err := st.SetUser(profiles); err == nil {
				t.Fatalf("SetUser(%+v) = nil, want a validation error", profiles)
			}
			if _, ok := st.Resolve("Kept"); !ok {
				t.Fatal("a rejected SetUser dropped the stored profiles")
			}
		})
	}
}

func TestSettingsPath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	writeFile := func(name, body string) string {
		t.Helper()
		path := filepath.Join(home, name)
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	good := writeFile("good.json", `{"env":{"ANTHROPIC_BASE_URL":"http://127.0.0.1:1"}}`)
	writeFile("broken.json", `{"env":`)
	writeFile("array.json", `[1,2]`)

	if path, err := SettingsPath(Subscription()); err != nil || path != "" {
		t.Fatalf("Subscription: %q, %v; want no file and no error", path, err)
	}
	if path, err := SettingsPath(Profile{Name: "Home", SettingsFile: "~/good.json"}); err != nil || path != good {
		t.Fatalf("~/good.json: %q, %v; want %s", path, err, good)
	}
	for _, file := range []string{"~/missing.json", "~/broken.json", "~/array.json"} {
		_, err := SettingsPath(Profile{Name: "Bad", SettingsFile: file})
		if !errors.Is(err, ErrSettingsFile) {
			t.Errorf("%s: err = %v, want ErrSettingsFile", file, err)
		}
		if err != nil && !strings.Contains(err.Error(), filepath.Join(home, strings.TrimPrefix(file, "~/"))) {
			t.Errorf("%s: message %q does not name the expanded file", file, err)
		}
	}
}

func TestNewStore_EmptyDirErrors(t *testing.T) {
	if _, err := NewStore(""); err == nil {
		t.Fatal("want error for empty dir")
	}
}
