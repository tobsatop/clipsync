package node

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"image/png"
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
	return string(it.Text)
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

	memA.Copy(clip.Item{Text: []byte("from Mac ไทย")}, false)
	waitFor(t, "text on B", func() bool { return text(memB) == "from Mac ไทย" })

	memB.Copy(clip.Item{Text: []byte("from Windows")}, false)
	waitFor(t, "text on A", func() bool { return text(memA) == "from Windows" })

	// Copying the first text again must still reach B, since B changed since.
	memA.Copy(clip.Item{Text: []byte("from Mac ไทย")}, false)
	waitFor(t, "text on B again", func() bool { return text(memB) == "from Mac ไทย" })

	time.Sleep(300 * time.Millisecond)
	if memA.Writes() != 1 || memB.Writes() != 2 {
		t.Fatalf("echo loop: A written %d times (want 1), B %d times (want 2)", memA.Writes(), memB.Writes())
	}
}

func TestSensitiveIsNotSent(t *testing.T) {
	_, _, _, memA, memB := pairUp(t)
	memA.Copy(clip.Item{Text: []byte("hunter2")}, true)
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

	memA.Copy(clip.Item{Text: []byte("after restart")}, false)
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
	memA.Copy(clip.Item{Text: []byte("copied while offline")}, false)
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

// testPNG makes a small valid PNG.
func testPNG(t *testing.T, w, h int, c color.Color) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, c)
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestImageAndTextPlusImage(t *testing.T) {
	_, _, _, memA, memB := pairUp(t)
	pic := testPNG(t, 64, 32, color.RGBA{255, 0, 0, 255})
	memA.Copy(clip.Item{Image: pic}, false)
	waitFor(t, "image on B", func() bool {
		it, _ := memB.Current()
		return bytes.Equal(it.Image, pic) && it.Text == nil
	})

	both := clip.Item{Text: []byte("A1\tB1\nA2\tB2"), Image: testPNG(t, 8, 8, color.White)}
	memB.Copy(both, false)
	waitFor(t, "text+image on A", func() bool {
		it, _ := memA.Current()
		return bytes.Equal(it.Image, both.Image) && string(it.Text) == string(both.Text)
	})
	time.Sleep(300 * time.Millisecond)
	if memA.Writes() != 1 || memB.Writes() != 1 {
		t.Fatalf("echo: A written %d times, B %d times (want 1 each)", memA.Writes(), memB.Writes())
	}
}

func TestDecodeItemRejectsBadInput(t *testing.T) {
	for name, b := range map[string][]byte{
		"short":          {0, 0},
		"length too big": {0, 0, 0, 9, 'h', 'i'},
		"empty":          {0, 0, 0, 0},
		"not png":        append([]byte{0, 0, 0, 0}, []byte("GIF89a....")...),
		"bad utf8":       {0, 0, 0, 2, 0xff, 0xfe},
	} {
		if _, err := decodeItem(b); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	it := clip.Item{Text: []byte("hi\r\nthere"), Image: testPNG(t, 2, 2, color.Black)}
	got, err := decodeItem(encodeItem(it))
	if err != nil || string(got.Text) != "hi\nthere" || !bytes.Equal(got.Image, it.Image) {
		t.Fatalf("round trip: %v %q", err, got.Text)
	}
}

func TestBusyClipboardIsRetried(t *testing.T) {
	_, _, _, memA, memB := pairUp(t)
	memA.FailReads(2, errors.New("the clipboard is busy"))
	memA.Copy(clip.Item{Text: []byte("after a busy moment")}, false)
	waitFor(t, "copy after two failed reads", func() bool { return text(memB) == "after a busy moment" })

	// A clipboard that stays unreadable is given up on, and the next copy works.
	memA.FailReads(10, clip.ErrBusy)
	memA.Copy(clip.Item{Text: []byte("never readable")}, false)
	time.Sleep(200 * time.Millisecond)
	memA.FailReads(0, nil)
	memA.Copy(clip.Item{Text: []byte("next copy")}, false)
	waitFor(t, "next copy", func() bool { return text(memB) == "next copy" })
}
