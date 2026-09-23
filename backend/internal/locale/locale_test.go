package locale

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

// unsetLocale clears every locale variable for the test and restores them after.
func unsetLocale(t *testing.T) {
	t.Helper()
	for _, k := range vars {
		t.Setenv(k, "") // registers the restore
		if err := os.Unsetenv(k); err != nil {
			t.Fatal(err)
		}
	}
}

func TestIsSet(t *testing.T) {
	for _, tc := range []struct {
		name string
		env  map[string]string
		want bool
	}{
		{"no locale at all", nil, false},
		{"empty values count as unset", map[string]string{"LC_ALL": "", "LC_CTYPE": "", "LANG": ""}, false},
		{"LANG", map[string]string{"LANG": "th_TH.UTF-8"}, true},
		{"LC_CTYPE", map[string]string{"LC_CTYPE": "UTF-8"}, true},
		{"LC_ALL", map[string]string{"LC_ALL": "en_US.UTF-8"}, true},
		{"a non-UTF-8 locale is still the user's", map[string]string{"LANG": "C"}, true},
		{"other LC_ vars do not set the character class", map[string]string{"LC_MESSAGES": "en_US.UTF-8"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsSet(func(k string) string { return tc.env[k] }); got != tc.want {
				t.Errorf("IsSet = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestEnsureProcess(t *testing.T) {
	t.Run("sets LC_CTYPE when there is no locale", func(t *testing.T) {
		unsetLocale(t)
		changed, err := EnsureProcess()
		if err != nil {
			t.Fatal(err)
		}
		if !changed || os.Getenv("LC_CTYPE") != CType {
			t.Fatalf("changed=%v LC_CTYPE=%q, want true %q", changed, os.Getenv("LC_CTYPE"), CType)
		}
		if _, ok := os.LookupEnv("LANG"); ok {
			t.Fatal("EnsureProcess set LANG; it must only set the character class")
		}
	})

	t.Run("leaves a user's locale alone", func(t *testing.T) {
		unsetLocale(t)
		t.Setenv("LANG", "th_TH.UTF-8")
		changed, err := EnsureProcess()
		if err != nil {
			t.Fatal(err)
		}
		if changed {
			t.Fatal("changed = true, want false")
		}
		if _, ok := os.LookupEnv("LC_CTYPE"); ok {
			t.Fatalf("LC_CTYPE = %q, want unset", os.Getenv("LC_CTYPE"))
		}
	})
}

// TestShellGuard runs the guard in a real POSIX shell with a scrubbed
// environment, the way a pane's launch script runs it.
func TestShellGuard(t *testing.T) {
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("no sh")
	}
	for _, tc := range []struct {
		name string
		env  []string
		want string // LC_ALL|LC_CTYPE|LANG after the guard
	}{
		{"no locale gets C.UTF-8", nil, "||" + CType + "|"},
		{"empty values count as unset", []string{"LANG=", "LC_ALL="}, "||" + CType + "|"},
		{"LANG is left alone", []string{"LANG=th_TH.UTF-8"}, "|||th_TH.UTF-8"},
		{"LC_CTYPE is left alone", []string{"LC_CTYPE=UTF-8"}, "||UTF-8|"},
		{"LC_ALL is left alone", []string{"LC_ALL=C"}, "|C||"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			script := probeScript()
			cmd := exec.Command(sh, "-c", script)
			cmd.Env = append([]string{"PATH=/usr/bin:/bin"}, tc.env...)
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("sh: %v: %s", err, out)
			}
			if got := strings.TrimSpace(string(out)); got != tc.want {
				t.Errorf("after guard = %q, want %q", got, tc.want)
			}
		})
	}
}

// probeScript is the guard followed by a probe that prints the three
// variables, each preceded by a separator so an unset one shows as empty.
func probeScript() string {
	return ShellGuard() + `; printf '|%s|%s|%s\n' "$LC_ALL" "$LC_CTYPE" "$LANG"`
}
