package endingslog

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

// HOW THE PROCESS ENDED.
//
// An ending line says who reported the ending and what they called it, and for
// the 2026-09-22 incidents that was `agent`/`other` - Claude Code's catch-all.
// The SessionEnd hook that reports it runs INSIDE the dying process, so it
// cannot know whether that process was asked to stop or decided to. What can
// know is the shell that launched it: the pane keeps a shell alive after the
// agent exits, and that shell sees the exit status, which is 128+N when signal
// N killed it. Measured against Claude Code 2.1.280 in a tmux pane:
//
//	kill -TERM   reason other               exit 143 (SIGTERM)
//	kill -HUP    reason other               exit 129 (SIGHUP)
//	kill -INT    reason other               exit 0
//	kill -KILL   no SessionEnd at all       exit 137 (SIGKILL)
//	/exit        reason prompt_input_exit   exit 0
//
// So the exit status is the fact that separates "something signalled every idle
// agent" from "the agents decided to exit".
//
// TWO WRITERS, ONE RECORD. The daemon writes the ending line when the hook
// reports; the pane shell appends an exit line when the process is gone (see
// ports.RuntimeConfig.ExitStatusFile). Neither waits for the other - the hook
// fires before the process exits, a SIGKILL fires no hook at all, and a daemon
// taken down by the same thing that took the agents must not have been holding
// the only copy in memory. Both lines are durable the instant they are written,
// and Read joins them: each exit line attaches to the nearest ending line of the
// same session, and an exit nothing reported stands as an entry of its own.

// AgentExit is how an agent process ended, as the shell that launched it saw
// it.
type AgentExit struct {
	// At is when the launching shell saw the process go.
	At time.Time `json:"at"`
	// Code is the exit status that shell saw.
	Code int `json:"code"`
	// Signal names the signal that killed the process, when Code is 128+N - the
	// convention every POSIX shell uses to report a death by signal. A process
	// that CATCHES a signal and then exits normally reports its own code
	// instead (Claude Code exits 0 on SIGINT), so an absent Signal means "not
	// killed by a signal it failed to handle", not "nobody sent one".
	Signal string `json:"signal,omitempty"`
}

// rawExit is the pane's exit line as it lands on disk. Epoch is a string
// because the shell writes whatever its clock offers - "1790100478.452558994"
// from zsh or bash, a bare "1790100478" from date(1), and a locale's comma for
// the decimal point from some bash builds.
type rawExit struct {
	Code  *int   `json:"code"`
	Epoch string `json:"epoch"`
}

// joinLead and joinLag bound how far an exit line may sit from the ending it
// belongs to. The hook reports BEFORE the process exits (about half a second
// before, measured), so an exit normally trails its ending; the lead covers a
// shell whose clock only has whole seconds, and the lag covers a slow SessionEnd
// hook, which Claude Code waits on before it exits.
const (
	joinLead = 5 * time.Second
	joinLag  = 60 * time.Second
)

// posixSignals are the signals whose numbers POSIX fixes, so a name derived
// from an exit status means the same on every platform AO runs on. Anything
// else is reported by number rather than risk naming the wrong signal.
var posixSignals = map[int]string{
	1: "SIGHUP", 2: "SIGINT", 3: "SIGQUIT", 4: "SIGILL", 5: "SIGTRAP", 6: "SIGABRT",
	8: "SIGFPE", 9: "SIGKILL", 11: "SIGSEGV", 13: "SIGPIPE", 14: "SIGALRM", 15: "SIGTERM",
}

// signalFor names the signal an exit status reports, or "" when it reports an
// ordinary exit.
func signalFor(code int) string {
	n := code - 128
	if n < 1 || n > 64 {
		return ""
	}
	if name, ok := posixSignals[n]; ok {
		return name
	}
	return fmt.Sprintf("signal %d", n)
}

// toAgentExit decodes a pane's exit line, rejecting one too damaged to trust.
func (r rawExit) toAgentExit() (AgentExit, bool) {
	if r.Code == nil {
		return AgentExit{}, false
	}
	at, ok := parseEpoch(r.Epoch)
	if !ok {
		return AgentExit{}, false
	}
	return AgentExit{At: at, Code: *r.Code, Signal: signalFor(*r.Code)}, true
}

// parseEpoch reads "<seconds>[.<fraction>]" exactly, without a float's rounding.
func parseEpoch(s string) (time.Time, bool) {
	s = strings.TrimSpace(strings.Replace(s, ",", ".", 1))
	secs, frac, _ := strings.Cut(s, ".")
	sec, err := strconv.ParseInt(secs, 10, 64)
	if err != nil || sec <= 0 {
		return time.Time{}, false
	}
	var nsec int64
	if frac != "" {
		if len(frac) > 9 {
			frac = frac[:9]
		}
		frac += strings.Repeat("0", 9-len(frac))
		if nsec, err = strconv.ParseInt(frac, 10, 64); err != nil {
			return time.Time{}, false
		}
	}
	return time.Unix(sec, nsec).UTC(), true
}

// sessionExit is one exit line, read and not yet joined.
type sessionExit struct {
	sessionID string
	exit      AgentExit
}

// joinExits attaches each exit to the nearest unclaimed ending of the same
// session inside [ending - joinLead, ending + joinLag], and returns the exits no
// ending claimed as entries of their own. endings is modified in place.
//
// Nearest wins, and each ending takes at most one exit, so a session that
// stopped, was resumed and stopped again a minute later gets each exit on its
// own ending rather than both on the first.
func joinExits(endings []Entry, exits []sessionExit) []Entry {
	sort.SliceStable(exits, func(i, j int) bool { return exits[i].exit.At.Before(exits[j].exit.At) })
	var orphans []Entry
	for _, x := range exits {
		best, bestGap := -1, time.Duration(0)
		for i := range endings {
			e := &endings[i]
			if e.SessionID != x.sessionID || e.Exit != nil {
				continue
			}
			gap := x.exit.At.Sub(e.At)
			if gap < -joinLead || gap > joinLag {
				continue
			}
			if gap < 0 {
				gap = -gap
			}
			if best < 0 || gap < bestGap {
				best, bestGap = i, gap
			}
		}
		if best >= 0 {
			exit := x.exit
			endings[best].Exit = &exit
			continue
		}
		// An agent that died and told nobody - a SIGKILL fires no hook, and a
		// hook that could not reach the daemon records nothing. The pane saw it,
		// so it is an ending all the same; Source stays empty because nobody
		// reported it.
		exit := x.exit
		orphans = append(orphans, Entry{At: exit.At, SessionID: x.sessionID, Exit: &exit})
	}
	return orphans
}
