package terminal

import (
	"context"
	"encoding/base64"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/inputgate"
)

// TestInjectionDefersUntilTypingStops is the end-to-end coordination test for the
// message-clobber fix. It wires the REAL terminal mux (which records each client
// keystroke into the gate) to the REAL gate that the delivery path consults, and
// proves that a would-be message injection is held while keystrokes are flowing
// through the pane and only released after the user stops typing. Before the fix,
// delivery ignored user typing entirely and injected immediately.
func TestInjectionDefersUntilTypingStops(t *testing.T) {
	pty := newFakePTY()
	sp := &fakeSpawner{ptys: []*fakePTY{pty}}
	src := &fakeSource{alive: true, spawner: sp}

	gate := inputgate.New(inputgate.WithQuietWindow(150*time.Millisecond), inputgate.WithMaxDefer(5*time.Second))
	mgr := NewManager(src, nil, testLogger(), WithHeartbeat(0), WithInputRecorder(gate))
	defer mgr.Close()

	conn := newFakeConn()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go mgr.Serve(ctx, conn)

	conn.in <- clientMsg{Ch: chTerminal, ID: "t1", Type: msgOpen}
	recv(t, conn, chTerminal, msgOpened, time.Second)

	keystroke := func() {
		conn.in <- clientMsg{Ch: chTerminal, ID: "t1", Type: msgData, Data: base64.StdEncoding.EncodeToString([]byte("x"))}
	}

	// Prime one keystroke and wait until it is processed, so the gate has recorded
	// pane t1 before the delivery waiter starts (otherwise WaitForQuiet would see a
	// never-typed pane and return at once — which is correct, just not what we test).
	keystroke()
	eventually(t, time.Second, func() bool { return len(pty.writtenBytes()) >= 1 })

	// Delivery side: block on a typing gap, then stamp when injection would happen.
	delivered := make(chan time.Time, 1)
	go func() {
		gate.WaitForQuiet(ctx, "t1")
		delivered <- time.Now()
	}()

	// Keep typing (a keystroke every 30ms) until told to stop.
	stopTyping := make(chan struct{})
	typingDone := make(chan struct{})
	go func() {
		defer close(typingDone)
		ticker := time.NewTicker(30 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-stopTyping:
				return
			case <-ticker.C:
				keystroke()
			}
		}
	}()

	// While keystrokes are still flowing, the message must NOT have been delivered.
	time.Sleep(250 * time.Millisecond)
	select {
	case <-delivered:
		t.Fatal("message delivered while the user was still typing")
	default:
	}

	// Stop typing; the quiet window should now open and release delivery.
	close(stopTyping)
	<-typingDone
	stopped := time.Now()

	select {
	case at := <-delivered:
		if gap := at.Sub(stopped); gap < 100*time.Millisecond {
			t.Fatalf("delivered only %s after typing stopped; expected to wait ~the quiet window", gap)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("message never delivered after typing stopped")
	}
}
