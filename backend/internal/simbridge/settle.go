package simbridge

import (
	"context"
	"hash/fnv"
	"math"
	"strconv"
	"time"
)

// Settling exists because `ao sim ax` reads whatever is on screen at the
// instant it is asked, and a screen that is still arriving is not the screen
// anyone meant to act on.
//
// This is not theoretical. During the real-app measurement a login screen read
// as six elements while Maestro, reading the same screen a few seconds later,
// saw sixty-seven - and the conclusion drawn from it ("our tree cannot see web
// content") was wrong. A read taken once the screen had settled agreed with
// Maestro. If an un-settled read can mislead a careful human running a
// controlled measurement, it will quietly write a recorded flow against a
// half-loaded screen.
//
// ⚠ Deliberately NOT how Maestro does it. Maestro's waitForAppToSettle takes
// two screenshots per iteration, PNG-encodes them and SHA256s both, in a loop
// with no delay - measured in the earlier study and judged too expensive to
// take on a machine shared with a human's real work. Here the sample is the
// accessibility tree that the caller was going to read anyway, and the
// comparison is one FNV-64a pass over it: no screenshot, no image encode, no
// cryptographic hash. The cost of settling is therefore exactly "one more AX
// read per attempt" and nothing else.
const (
	// DefaultSettleReads is how many reads ReadSettled will take before giving
	// up. Two is the floor - stability cannot be observed from one sample -
	// and three allows one retry, which is what a screen that is mid-animation
	// on the first comparison usually needs.
	DefaultSettleReads = 3
	// DefaultSettleDelay is how long to wait between reads. Short enough that
	// settling adds well under a second of waiting on top of the reads
	// themselves, long enough that two reads do not both land inside the same
	// animation frame.
	DefaultSettleDelay = 250 * time.Millisecond
)

// SettleOptions tunes ReadSettled. The zero value means the defaults above,
// so a caller that does not care can pass SettleOptions{}.
type SettleOptions struct {
	// MaxReads bounds the whole operation. A screen that never settles - a
	// spinner, a video, a marquee - must not hang the caller, so this is the
	// promise that it cannot.
	MaxReads int
	// Delay is how long to wait between reads.
	Delay time.Duration
}

func (o SettleOptions) maxReads() int {
	if o.MaxReads < 2 {
		return DefaultSettleReads
	}
	return o.MaxReads
}

func (o SettleOptions) delay() time.Duration {
	if o.Delay <= 0 {
		return DefaultSettleDelay
	}
	return o.Delay
}

// SettleResult says what settling actually cost and whether it worked, so a
// caller can tell a human "this screen never stopped moving" instead of
// silently handing over a snapshot of something mid-flight.
type SettleResult struct {
	// Reads is how many AX reads were taken, including the first.
	Reads int
	// Settled is true when two consecutive reads described the same screen.
	// False means the budget ran out first and Snapshot is the last read, not
	// a stable one.
	Settled bool
}

// ReadSettled reads the screen until two consecutive reads describe the same
// thing, or the budget runs out.
//
// read is passed in rather than a Driver so this is testable without a
// simulator and so callers that already wrap their read (with a lease check, a
// retry, a device resolution) do not have to unwrap it.
//
// A read that fails is returned immediately: settling is about which snapshot
// to trust, and there is nothing to settle when the device will not answer.
// The returned SettleResult is still populated, so a caller reporting the
// failure can say how far it got.
func ReadSettled(ctx context.Context, read func(context.Context) (Snapshot, error), opts SettleOptions) (Snapshot, SettleResult, error) {
	var result SettleResult

	// The first read is taken outside the loop so that "there is nothing to
	// compare against yet" is expressed by the structure rather than by a
	// guard inside it. A guard could be removed and the code would still work
	// for every input anyone happened to test, which is the shape of a bug
	// that ships.
	snap, err := read(ctx)
	if err != nil {
		return Snapshot{}, result, err
	}
	result.Reads = 1
	prev := Fingerprint(snap)

	for result.Reads < opts.maxReads() {
		select {
		case <-ctx.Done():
			return snap, result, ctx.Err()
		case <-time.After(opts.delay()):
		}
		next, err := read(ctx)
		if err != nil {
			return snap, result, err
		}
		snap = next
		result.Reads++

		current := Fingerprint(snap)
		if current == prev {
			result.Settled = true
			return snap, result, nil
		}
		prev = current
	}
	return snap, result, nil
}

// Fingerprint reduces a snapshot to the identity of the screen it describes.
//
// What goes in is everything a selector is built from - the tree shape, what
// each element is called, and where it sits - and nothing that changes on its
// own. Frames are rounded to whole points on purpose: a view still easing into
// place moves by fractions, and a fingerprint that changed with every
// sub-pixel would never settle at all, which is the failure mode of comparing
// screenshots.
//
// It is deliberately NOT a cryptographic hash. Two different screens colliding
// would mean returning a snapshot one read early - a wrong answer nobody could
// tell from a right one, but on a 64-bit space over a handful of samples that
// is not a risk worth paying SHA256 per read for.
func Fingerprint(snap Snapshot) uint64 {
	h := fnv.New64a()
	writeString(h, snap.Frontmost.BundleID)
	var walk func(els []Element)
	walk = func(els []Element) {
		for _, el := range els {
			writeString(h, el.Path)
			writeString(h, el.ID)
			writeString(h, el.Role)
			writeString(h, el.Type)
			writeString(h, el.Label)
			writeString(h, el.Value)
			writeString(h, strconv.FormatBool(el.Enabled))
			writeString(h, strconv.FormatBool(el.OffScreen))
			for _, v := range []float64{el.Frame.X, el.Frame.Y, el.Frame.Width, el.Frame.Height} {
				writeString(h, strconv.FormatInt(int64(math.Round(v)), 10))
			}
			walk(el.Children)
		}
	}
	walk(snap.Elements)
	return h.Sum64()
}

// writeString feeds one field into the hash with a separator, so that two
// adjacent fields cannot run together and make ("ab", "c") look like
// ("a", "bc").
func writeString(h interface{ Write([]byte) (int, error) }, s string) {
	_, _ = h.Write([]byte(s))
	_, _ = h.Write([]byte{0})
}
