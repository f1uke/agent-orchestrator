// Package locale gives every process AO launches a UTF-8 character locale
// when it would otherwise run with none.
//
// The desktop app is started from Finder, so launchd hands the daemon an
// environment with no LANG, LC_ALL or LC_CTYPE, and every child inherits that:
// git, simctl, the tmux server, and every agent, reviewer and run pane behind
// it. Most tools cope, but the ones that decode text by the process locale do
// not. pbcopy is the one that bites: Claude Code's fullscreen TUI copies a
// selection by piping it to pbcopy, which reads UTF-8 bytes as MacRoman when no
// locale is set, so Thai `บน` (E0 B8 9A E0 B8 99) lands on the clipboard as
// `‡∏ö‡∏ô`.
//
// Only the character class is set, never LANG: LC_CTYPE decides how bytes map
// to characters, which is the whole bug, while LANG also changes message
// language, collation and number formats for every tool. The value is C.UTF-8
// because it names the same locale on macOS and glibc Linux; the bare "UTF-8"
// that macOS also accepts is not a locale glibc knows.
//
// A locale the user already has always wins: if any of LC_ALL, LC_CTYPE or LANG
// is set to a non-empty value, nothing is touched, even when that locale is not
// UTF-8.
package locale

import "os"

// CType is the LC_CTYPE value AO supplies when a process has no locale.
const CType = "C.UTF-8"

// vars are the variables that can set a process's character locale, in POSIX
// precedence order. An empty value counts as unset, as it does for setlocale.
var vars = []string{"LC_ALL", "LC_CTYPE", "LANG"}

// IsSet reports whether the environment read through getenv already carries a
// character locale.
func IsSet(getenv func(string) string) bool {
	for _, k := range vars {
		if getenv(k) != "" {
			return true
		}
	}
	return false
}

// EnsureProcess gives the current process LC_CTYPE=C.UTF-8 when it has no
// locale, so every child it starts afterwards inherits one. It reports whether
// it changed anything. Call it once at startup, before any child is spawned.
func EnsureProcess() (bool, error) {
	if IsSet(os.Getenv) {
		return false, nil
	}
	if err := os.Setenv("LC_CTYPE", CType); err != nil {
		return false, err
	}
	return true, nil
}

// ShellGuard is a POSIX shell statement that exports LC_CTYPE=C.UTF-8 only when
// the shell running it has no locale. It is for processes that do not inherit
// the daemon's environment: a tmux pane takes its environment from the tmux
// server, which may have been started long before, by launchd or by the user,
// without a locale. Deciding inside the pane means a locale the server, the
// user's tmux config or the project's configured env provides is left alone.
func ShellGuard() string {
	return `[ -n "${LC_ALL}${LC_CTYPE}${LANG}" ] || export LC_CTYPE=` + CType
}
