package node

import (
	"bytes"
	"context"
	"log"
	"sync"
	"testing"
	"time"

	"clipsync/internal/clip"
	"clipsync/internal/config"
)

// testLog collects node logs and prints them only if the test fails, since
// session goroutines may still log just after a test returns.
type testLog struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (l *testLog) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.Write(p)
}

func newTestLog(t *testing.T) *testLog {
	l := &testLog{}
	t.Cleanup(func() {
		if t.Failed() {
			l.mu.Lock()
			t.Log("node log:\n" + l.buf.String())
			l.mu.Unlock()
		}
	})
	return l
}

func newNode(t *testing.T, ctx context.Context, self, peer string, key []byte, cb clip.Clipboard) *Node {
	t.Helper()
	cfg := &config.Config{DeviceID: self, Name: self, Peer: &config.Peer{ID: peer, Name: peer, Key: key}}
	n := New(cfg, "", cb, log.New(newTestLog(t), self+" ", 0), Options{
		PollInterval: 10 * time.Millisecond,
		RedialEvery:  50 * time.Millisecond,
		Verbose:      true,
	})
	if err := n.Start(ctx); err != nil {
		t.Fatal(err)
	}
	return n
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func text(cb *clip.Memory) string {
	it, ok := cb.Current()
	if !ok {
		return ""
	}
	return string(it.Data)
}

// pairUp starts two connected nodes. Both know each other's address, so both
// dial at once, which also exercises the duplicate-connection rule.
func pairUp(t *testing.T) (context.CancelFunc, *Node, *Node, *clip.Memory, *clip.Memory) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	key := bytes.Repeat([]byte{9}, 32)
	memA, memB := clip.NewMemory(), clip.NewMemory()
	a := newNode(t, ctx, "aaaa", "bbbb", key, memA)
	b := newNode(t, ctx, "bbbb", "aaaa", key, memB)
	a.SetPeerAddr(b.ListenAddr())
	b.SetPeerAddr(a.ListenAddr())
	t.Cleanup(func() {
		cancel()
		a.Wait()
		b.Wait()
	})
	waitFor(t, "connection", func() bool { return a.Connected() && b.Connected() })
	time.Sleep(200 * time.Millisecond) // let any duplicate connection settle
	return cancel, a, b, memA, memB
}

func TestSyncBothWaysWithoutEcho(t *testing.T) {
	_, a, b, memA, memB := pairUp(t)
	if !a.Connected() || !b.Connected() {
		t.Fatal("connection did not survive the duplicate-dial race")
	}

	memA.Copy(clip.Item{Kind: clip.Text, Data: []byte("from Mac ไทย")}, false)
	waitFor(t, "text on B", func() bool { return text(memB) == "from Mac ไทย" })

	memB.Copy(clip.Item{Kind: clip.Text, Data: []byte("from Windows")}, false)
	waitFor(t, "text on A", func() bool { return text(memA) == "from Windows" })

	// Copying the first text again must still reach B, since B changed since.
	memA.Copy(clip.Item{Kind: clip.Text, Data: []byte("from Mac ไทย")}, false)
	waitFor(t, "text on B again", func() bool { return text(memB) == "from Mac ไทย" })

	time.Sleep(300 * time.Millisecond)
	if memA.Writes() != 1 || memB.Writes() != 2 {
		t.Fatalf("echo loop: A written %d times (want 1), B %d times (want 2)", memA.Writes(), memB.Writes())
	}
}

func TestSensitiveIsNotSent(t *testing.T) {
	_, _, _, memA, memB := pairUp(t)
	memA.Copy(clip.Item{Kind: clip.Text, Data: []byte("hunter2")}, true)
	time.Sleep(300 * time.Millisecond)
	if _, ok := memB.Current(); ok {
		t.Fatal("sensitive content was sent")
	}
}

func TestReconnectAfterPeerRestart(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	key := bytes.Repeat([]byte{9}, 32)
	memA, memB := clip.NewMemory(), clip.NewMemory()
	a := newNode(t, ctx, "aaaa", "bbbb", key, memA)

	ctxB, cancelB := context.WithCancel(ctx)
	b := newNode(t, ctxB, "bbbb", "aaaa", key, memB)
	a.SetPeerAddr(b.ListenAddr())
	waitFor(t, "first connection", func() bool { return a.Connected() && b.Connected() })

	cancelB()
	b.Wait()
	waitFor(t, "A to notice", func() bool { return !a.Connected() })

	// B comes back on a new port; only B knows where A is.
	b2 := newNode(t, ctx, "bbbb", "aaaa", key, memB)
	b2.SetPeerAddr(a.ListenAddr())
	waitFor(t, "reconnection", func() bool { return a.Connected() && b2.Connected() })

	memA.Copy(clip.Item{Kind: clip.Text, Data: []byte("after restart")}, false)
	waitFor(t, "text after restart", func() bool { return text(memB) == "after restart" })
	cancel()
	a.Wait()
	b2.Wait()
}

func TestWrongKeyNeverConnects(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	a := newNode(t, ctx, "aaaa", "bbbb", bytes.Repeat([]byte{1}, 32), clip.NewMemory())
	b := newNode(t, ctx, "bbbb", "aaaa", bytes.Repeat([]byte{2}, 32), clip.NewMemory())
	a.SetPeerAddr(b.ListenAddr())
	b.SetPeerAddr(a.ListenAddr())
	time.Sleep(400 * time.Millisecond)
	if a.Connected() || b.Connected() {
		t.Fatal("connected with mismatched keys")
	}
	cancel()
	a.Wait()
	b.Wait()
}

func TestCopyWhileDisconnectedArrivesOnReconnect(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	key := bytes.Repeat([]byte{9}, 32)
	memA, memB := clip.NewMemory(), clip.NewMemory()
	a := newNode(t, ctx, "aaaa", "bbbb", key, memA)
	time.Sleep(50 * time.Millisecond) // what was on the clipboard at start is not sent
	memA.Copy(clip.Item{Kind: clip.Text, Data: []byte("copied while offline")}, false)
	time.Sleep(100 * time.Millisecond) // let A notice the copy

	b := newNode(t, ctx, "bbbb", "aaaa", key, memB)
	b.SetPeerAddr(a.ListenAddr())
	waitFor(t, "offline copy to arrive", func() bool { return text(memB) == "copied while offline" })

	// Once confirmed, it is not resent on the next reconnect.
	waitFor(t, "ack", func() bool { a.mu.Lock(); defer a.mu.Unlock(); return a.pending == nil })
	cancel()
	a.Wait()
	b.Wait()
}
