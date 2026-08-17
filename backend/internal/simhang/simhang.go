// Package simhang answers one question about an app running on a simulator:
// is its MAIN THREAD blocked?
//
// It exists because the answer changes what an agent does next. An app whose
// main thread is parked in a syscall cannot answer an accessibility query and
// cannot process a touch, so `ao sim ax` comes back with nothing and `ao sim
// tap` appears to do nothing - and the message AO used to print for that,
// "reported no accessibility elements", points at the one subsystem that is
// working fine. An agent that believes it re-reads the app's view code for
// several rounds while the real cause is a full pipe on the app's stdout.
//
// The mechanism is `sample`, the same tool behind Activity Monitor's "Sample
// Process": it reads thread stacks and never writes to the target. Two rules
// keep the probe from becoming a hazard of its own:
//
//   - It runs only where the caller has ALREADY failed. Nothing on a working
//     command's path pays for it.
//   - It never guesses. A probe that cannot produce a stack, or produces one it
//     does not understand, reports no verdict and the caller says exactly what
//     it said before.
//
// The discrimination that matters is that a HEALTHY idle app looks, from a
// distance, exactly like a blocked one: 100% of its samples sit in a single
// identical stack. The difference is where that stack ends. An app waiting for
// its next event is inside the run loop's own wait (__CFRunLoopServiceMachPort
// -> mach_msg2_trap); anything else that never moves for a whole second of
// sampling is a thread that has stopped serving its run loop.
package simhang

import (
	"context"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Binary is the sampler. It ships with macOS, so a machine that can run a
// simulator can run this.
const Binary = "sample"

// Probe timings. The first call takes ONE sample and is there to rule the run
// loop out cheaply (~0.3s on a warm symbol cache); only a process that is not
// obviously idle pays for the second, which samples for a full second so a
// thread that is merely busy has room to move.
const (
	quickBudget   = 20 * time.Second
	confirmWindow = "1"
	confirmEvery  = "100"
	// minConfirmSamples guards against calling a thread blocked on the strength
	// of one or two samples.
	minConfirmSamples = 5
)

// LookPath resolves a binary, matching os/exec.LookPath.
type LookPath func(file string) (string, error)

// Runner executes a command and returns its combined output. It is the same
// seam the rest of `ao sim` runs simctl through, so this package is testable
// with no macOS, no Xcode and no simulator.
type Runner func(ctx context.Context, name string, args ...string) ([]byte, error)

// Diagnosis is what the probe saw. It is only ever returned alongside ok=true;
// there is no "probably" state, because a caller cannot phrase one honestly.
type Diagnosis struct {
	// Blocked: the main thread never moved and is not in the run loop's wait.
	Blocked bool
	// TopFrame is the leaf of that stack - the call that is not returning.
	TopFrame string
	// Frames is the deepest part of the stack, leaf first. The leaf alone
	// ("write") does not say which subsystem is stuck; its callers do.
	Frames []string
	// Samples is how many samples agreed.
	Samples int
	// StdoutWrite: the stack goes through a write to the app's stdout, which is
	// the one cause of this AO can name and tell the caller how to fix.
	StdoutWrite bool
}

// Diagnose reports whether pid's main thread is blocked. The second return is
// false whenever the probe could not tell, and then the first is meaningless:
// the caller must fall back to whatever it would have said anyway.
func Diagnose(ctx context.Context, lookPath LookPath, run Runner, pid int) (Diagnosis, bool) {
	if pid <= 0 || lookPath == nil || run == nil {
		return Diagnosis{}, false
	}
	if _, err := lookPath(Binary); err != nil {
		return Diagnosis{}, false
	}

	// One sample: enough to see the run loop, which is what an app that is not
	// hung is almost always doing.
	quick, ok := sampleMainThread(ctx, run, pid, "0", "")
	if !ok {
		return Diagnosis{}, false
	}
	if quick.runLoopWait() {
		return Diagnosis{}, true
	}

	// It was somewhere else at that instant, which is ordinary for an app that
	// is working. Sample for a second: a thread that is working moves, and a
	// thread that is blocked does not.
	confirm, ok := sampleMainThread(ctx, run, pid, confirmWindow, confirmEvery)
	if !ok {
		return Diagnosis{}, false
	}
	if confirm.runLoopWait() || confirm.total < minConfirmSamples || confirm.leafCount < confirm.total {
		return Diagnosis{}, true
	}
	return Diagnosis{
		Blocked:     true,
		TopFrame:    confirm.frames[0],
		Frames:      confirm.frames,
		Samples:     confirm.total,
		StdoutWrite: confirm.stdoutWrite(),
	}, true
}

// sampleMainThread runs one `sample` and reduces it to the main thread's
// dominant stack.
func sampleMainThread(ctx context.Context, run Runner, pid int, duration, interval string) (stack, bool) {
	args := []string{strconv.Itoa(pid), duration}
	if interval != "" {
		args = append(args, interval)
	}
	// -mayDie: the app may exit while it is being sampled, and a report with
	// symbols is worth more than a clean exit code. -file keeps the report off
	// /tmp, where sample otherwise leaves one behind per run.
	args = append(args, "-mayDie", "-file", "/dev/stdout")

	probeCtx, cancel := context.WithTimeout(ctx, quickBudget)
	defer cancel()
	out, err := run(probeCtx, Binary, args...)
	if err != nil {
		return stack{}, false
	}
	return parseMainThread(string(out))
}

// stack is the main thread's dominant call chain in one sample report.
type stack struct {
	// frames is leaf first, deepest frames only.
	frames []string
	// total is how many samples the thread contributed, leafCount how many
	// ended in this exact chain. They are equal for a thread that never moved.
	total     int
	leafCount int
}

// maxFrames is how much of the chain is kept. The leaf alone is usually a libc
// primitive - `write` says nothing on its own - and it takes several callers to
// reach the frame that names the subsystem: write <- _swrite <- fwrite <-
// _Stdout.write <- _debugPrint_unlocked <- debugPrint is the whole story, and
// only the last two frames tell it.
const maxFrames = 6

// runLoopWaitFrames are the frames a healthy iOS main thread is parked in
// between events. Nothing here means "blocked": it means "waiting for you".
var runLoopWaitFrames = []string{
	"mach_msg2_trap",
	"mach_msg_trap",
	"mach_msg_overwrite_trap",
	"kevent_id",
}

func (s stack) runLoopWait() bool {
	if len(s.frames) == 0 {
		return false
	}
	leafIsWait := false
	for _, frame := range runLoopWaitFrames {
		if s.frames[0] == frame {
			leafIsWait = true
		}
	}
	if !leafIsWait {
		return false
	}
	// A main thread can also sit in mach_msg for a synchronous XPC call it will
	// never get an answer to, which IS blocked. The run loop's own wait is the
	// one with the run loop in it.
	for _, frame := range s.frames {
		if strings.Contains(frame, "CFRunLoop") {
			return true
		}
	}
	return false
}

// stdoutWriteFrames are the calls a `print` goes through on its way to a pipe
// nobody is draining. Recognising them is what turns "your app is stuck" into
// "here is why, and here is what to run instead".
var stdoutWriteFrames = []string{
	"_swrite", "fwrite", "fputs", "putchar", "_Stdout", "debugPrint", "writeToStdout",
}

func (s stack) stdoutWrite() bool {
	for _, frame := range s.frames {
		for _, marker := range stdoutWriteFrames {
			if strings.Contains(frame, marker) {
				return true
			}
		}
	}
	return false
}

// mainThreadMarker is how `sample` labels the main thread, on every OS version
// that has shipped with the simulator this drives.
const mainThreadMarker = "com.apple.main-thread"

// callGraphLine splits one line of the call graph into its indentation, its
// sample count and the frame. The leading run of drawing characters (+ | ! :)
// is sample's own way of connecting siblings and carries no meaning.
var callGraphLine = regexp.MustCompile(`^([ +|!:]*)(\d+) (.*)$`)

// parseMainThread reduces a sample report to the main thread's dominant chain:
// at every level the child that most samples went through.
func parseMainThread(report string) (stack, bool) {
	lines := strings.Split(report, "\n")
	start := -1
	for i, line := range lines {
		if strings.HasPrefix(line, "Call graph:") {
			start = i + 1
			break
		}
	}
	if start < 0 {
		return stack{}, false
	}

	type node struct {
		indent, count int
		symbol        string
	}
	var thread []node
	threadIndent := -1
	for _, line := range lines[start:] {
		match := callGraphLine.FindStringSubmatch(line)
		if match == nil {
			// Blank lines and the sections that follow the graph end it; a line
			// that is not a frame inside a thread is not part of one.
			if threadIndent >= 0 && strings.TrimSpace(line) != "" {
				break
			}
			continue
		}
		indent := len(match[1])
		count, err := strconv.Atoi(match[2])
		if err != nil {
			continue
		}
		symbol := match[3]
		if threadIndent < 0 {
			if strings.Contains(symbol, mainThreadMarker) {
				threadIndent = indent
				thread = append(thread, node{indent: indent, count: count, symbol: symbol})
			}
			continue
		}
		if indent <= threadIndent {
			break // the next thread
		}
		thread = append(thread, node{indent: indent, count: count, symbol: symbol})
	}
	if len(thread) < 2 {
		return stack{}, false
	}

	// Walk down, taking the busiest child at each level. Children of the
	// current frame are the nodes after it, before the next node at or above
	// its own indentation.
	result := stack{total: thread[0].count}
	var chain []string
	current := 0
	for {
		best, bestCount := -1, -1
		for i := current + 1; i < len(thread); i++ {
			if thread[i].indent <= thread[current].indent {
				break
			}
			if thread[i].indent != thread[current+1].indent {
				continue // a grandchild, not a child
			}
			if thread[i].count > bestCount {
				best, bestCount = i, thread[i].count
			}
		}
		if best < 0 {
			break
		}
		chain = append(chain, frameName(thread[best].symbol))
		result.leafCount = thread[best].count
		current = best
	}
	if len(chain) == 0 {
		return stack{}, false
	}
	// Leaf first: the call that is not returning is the headline, its callers
	// are the context.
	for i := len(chain) - 1; i >= 0 && len(result.frames) < maxFrames; i-- {
		result.frames = append(result.frames, chain[i])
	}
	return result, true
}

// frameName is the symbol without sample's own annotations: the image it came
// from, the offset into it, and the address.
func frameName(symbol string) string {
	name := symbol
	if i := strings.Index(name, "  (in "); i >= 0 {
		image := name[i+len("  (in "):]
		name = strings.TrimSpace(name[:i])
		// An unsymbolicated frame is still worth naming by its image: "??? (in
		// Nimbus)" says the app's own code, which is the useful half.
		if name == "???" {
			if end := strings.Index(image, ")"); end >= 0 {
				name = "??? (in " + image[:end] + ")"
			}
		}
		return name
	}
	if i := strings.Index(name, "  ["); i >= 0 {
		name = name[:i]
	}
	return strings.TrimSpace(name)
}
