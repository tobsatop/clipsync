package wire

import (
	"bytes"
	"context"
	"crypto/hmac"
	"errors"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"

	"filippo.io/cpace"
)

var (
	host   = Identity{ID: "host-id", Name: "Mac", Port: 1111}
	joiner = Identity{ID: "joiner-id", Name: "Windows", Port: 2222}
)

func listen(t *testing.T) net.Listener {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	return ln
}

type pairOut struct {
	res *PairResult
	err error
}

func pair(t *testing.T, hostCode, joinCode string) (hostRes, joinRes pairOut) {
	t.Helper()
	ln := listen(t)
	ch := make(chan pairOut, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			ch <- pairOut{nil, err}
			return
		}
		defer conn.Close()
		h, err := ReadHello(conn)
		if err != nil {
			ch <- pairOut{nil, err}
			return
		}
		res, err := HostPair(conn, h, host, hostCode)
		ch <- pairOut{res, err}
	}()
	res, err := JoinPair(context.Background(), ln.Addr().String(), joiner, joinCode)
	return <-ch, pairOut{res, err}
}

func TestPairSameCode(t *testing.T) {
	h, j := pair(t, "482913", "482913")
	if h.err != nil || j.err != nil {
		t.Fatalf("pair failed: host=%v joiner=%v", h.err, j.err)
	}
	if !bytes.Equal(h.res.Key, j.res.Key) || len(h.res.Key) != 32 {
		t.Fatal("keys differ")
	}
	if h.res.PeerID != joiner.ID || j.res.PeerID != host.ID {
		t.Fatalf("wrong peer IDs: %q %q", h.res.PeerID, j.res.PeerID)
	}
	if h.res.PeerAddr != "127.0.0.1:2222" || j.res.PeerAddr != "127.0.0.1:1111" {
		t.Fatalf("wrong peer addrs: %q %q", h.res.PeerAddr, j.res.PeerAddr)
	}
}

func TestPairWrongCode(t *testing.T) {
	h, j := pair(t, "482913", "482914")
	if !errors.Is(h.err, ErrWrongCode) || !errors.Is(j.err, ErrWrongCode) {
		t.Fatalf("want ErrWrongCode on both sides, got host=%v joiner=%v", h.err, j.err)
	}
}

func TestPairingKeysAreFresh(t *testing.T) {
	a, _ := pair(t, "111111", "111111")
	b, _ := pair(t, "111111", "111111")
	if a.err != nil || b.err != nil {
		t.Fatal(a.err, b.err)
	}
	if bytes.Equal(a.res.Key, b.res.Key) {
		t.Fatal("two pairings with the same code produced the same key")
	}
}

type acceptOut struct {
	s   *Session
	err error
}

func connect(t *testing.T, hostKey, joinKey []byte) (*Session, *Session, error, error) {
	t.Helper()
	ln := listen(t)
	ch := make(chan acceptOut, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			ch <- acceptOut{nil, err}
			return
		}
		h, err := ReadHello(conn)
		if err != nil {
			conn.Close()
			ch <- acceptOut{nil, err}
			return
		}
		s, err := AcceptSync(conn, h, host, hostKey)
		if err != nil {
			conn.Close()
		}
		ch <- acceptOut{s, err}
	}()
	c, cerr := Dial(context.Background(), ln.Addr().String(), joiner, host.ID, joinKey)
	a := <-ch
	if c != nil {
		t.Cleanup(func() { c.Close() })
	}
	if a.s != nil {
		t.Cleanup(func() { a.s.Close() })
	}
	return a.s, c, a.err, cerr
}

func TestSessionBothWays(t *testing.T) {
	key := bytes.Repeat([]byte{7}, 32)
	srv, cli, serr, cerr := connect(t, key, key)
	if serr != nil || cerr != nil {
		t.Fatal(serr, cerr)
	}
	if !cli.Dialed || srv.Dialed {
		t.Fatal("Dialed flags wrong")
	}
	thai := "สวัสดี clipboard\nบรรทัดสอง"
	for i := 0; i < 3; i++ {
		if err := cli.Send(MsgItem, []byte(thai)); err != nil {
			t.Fatal(err)
		}
		typ, got, err := srv.Recv(time.Second)
		if err != nil || typ != MsgItem || string(got) != thai {
			t.Fatalf("server got %d %q %v", typ, got, err)
		}
		if err := srv.Send(MsgItem, []byte("back")); err != nil {
			t.Fatal(err)
		}
		typ, got, err = cli.Recv(time.Second)
		if err != nil || typ != MsgItem || string(got) != "back" {
			t.Fatalf("client got %d %q %v", typ, got, err)
		}
	}
	big := bytes.Repeat([]byte("x"), 5<<20)
	go cli.Send(MsgItem, big)
	if _, got, err := srv.Recv(5 * time.Second); err != nil || !bytes.Equal(got, big) {
		t.Fatalf("large message: %v", err)
	}
}

func TestSessionWrongKey(t *testing.T) {
	_, _, serr, cerr := connect(t, bytes.Repeat([]byte{1}, 32), bytes.Repeat([]byte{2}, 32))
	if !errors.Is(serr, ErrBadKey) || !errors.Is(cerr, ErrBadKey) {
		t.Fatalf("want ErrBadKey on both sides, got server=%v client=%v", serr, cerr)
	}
}

func TestDialWrongPeer(t *testing.T) {
	key := bytes.Repeat([]byte{7}, 32)
	ln := listen(t)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		if h, err := ReadHello(conn); err == nil {
			AcceptSync(conn, h, Identity{ID: "someone-else", Name: "Other"}, key)
		}
	}()
	_, err := Dial(context.Background(), ln.Addr().String(), joiner, host.ID, key)
	if err == nil || !strings.Contains(err.Error(), "not the paired computer") {
		t.Fatalf("want wrong-peer error, got %v", err)
	}
}

func TestFrameLimit(t *testing.T) {
	var buf bytes.Buffer
	writeFrame(&buf, make([]byte, 100))
	if _, err := readFrame(&buf, 99); err == nil {
		t.Fatal("oversized frame accepted")
	}
}

// attackerTry plays a malicious joiner (from the security review): one guess,
// check the host's confirm tag offline, then hang up instead of confirming.
func attackerTry(t *testing.T, ln net.Listener, hostID, guess string) (right bool) {
	t.Helper()
	conn, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	msgA, st, _ := cpace.Start(guess, cpace.NewContextInfo("evil", hostID, []byte(pairContext)))
	evil := newHello(ModePair, Identity{ID: "evil", Name: "evil", Port: 1}, nil)
	sendHello(conn, &evil)
	writeJSON(conn, pairMsg{Pake: msgA})
	writeFrame(conn, []byte("{")) // garbage instead of Fail/Confirm
	if _, err := ReadHello(conn); err != nil {
		t.Fatal(err)
	}
	var m pairMsg
	if err := readJSON(conn, &m); err != nil {
		t.Fatal(err)
	}
	shared, err := st.Finish(m.Pake)
	if err != nil {
		t.Fatal(err)
	}
	_, hostConfirm, _, _ := pairKeys(shared)
	return hmac.Equal(hostConfirm, m.Confirm)
}

func TestServePairingCountsAbortedGuesses(t *testing.T) {
	ln := listen(t)
	done := make(chan error, 1)
	go func() {
		_, err := ServePairing(ln, host, "123456", 3, nil)
		done <- err
	}()
	for i := 0; i < 3; i++ {
		if attackerTry(t, ln, host.ID, fmt.Sprintf("%06d", i)) {
			t.Fatal("wrong guess judged right")
		}
	}
	select {
	case err := <-done:
		if !errors.Is(err, ErrTooManyAttempts) {
			t.Fatalf("want ErrTooManyAttempts, got %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("host kept accepting guesses after 3 aborted attempts")
	}
}

func TestServePairingSucceedsAfterWrongTriesAndNoise(t *testing.T) {
	ln := listen(t)
	type out struct {
		res *PairResult
		err error
	}
	done := make(chan out, 1)
	left := []int{}
	go func() {
		res, err := ServePairing(ln, host, "123456", 3, func(n int) { left = append(left, n) })
		done <- out{res, err}
	}()
	for i := 0; i < 2; i++ {
		if _, err := JoinPair(context.Background(), ln.Addr().String(), joiner, "654321"); !errors.Is(err, ErrWrongCode) {
			t.Fatalf("want ErrWrongCode, got %v", err)
		}
	}
	// Connections that never reach the key exchange cost nothing.
	for i := 0; i < 5; i++ {
		c, err := net.Dial("tcp", ln.Addr().String())
		if err != nil {
			t.Fatal(err)
		}
		c.Write([]byte("hello?"))
		c.Close()
	}
	res, err := JoinPair(context.Background(), ln.Addr().String(), joiner, "123456")
	if err != nil {
		t.Fatalf("right code after two wrong ones: %v", err)
	}
	o := <-done
	if o.err != nil || !bytes.Equal(o.res.Key, res.Key) {
		t.Fatalf("host: %v", o.err)
	}
	if fmt.Sprint(left) != "[2 1]" {
		t.Fatalf("tries-left callbacks = %v, want [2 1]", left)
	}
}

func TestTamperedHelloFailsLogin(t *testing.T) {
	// A man in the middle that rewrites the listener's announced port must
	// break the login, because both Hellos are bound into the keys.
	key := bytes.Repeat([]byte{7}, 32)
	ln := listen(t)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		h, err := ReadHello(conn)
		if err != nil {
			return
		}
		h.raw = bytes.Replace(h.raw, []byte(`"port":2222`), []byte(`"port":9999`), 1)
		AcceptSync(conn, h, host, key)
	}()
	_, err := Dial(context.Background(), ln.Addr().String(), joiner, host.ID, key)
	if !errors.Is(err, ErrBadKey) {
		t.Fatalf("want ErrBadKey, got %v", err)
	}
}

func TestVersionMismatchIsReported(t *testing.T) {
	key := bytes.Repeat([]byte{7}, 32)
	ln := listen(t)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		ReadHello(conn)
		// An older clipsync answers with protocol 1.
		writeJSON(conn, Hello{App: appName, V: 1, Mode: ModeSync, ID: host.ID, Name: "Old PC", Port: 1})
	}()
	_, err := Dial(context.Background(), ln.Addr().String(), joiner, host.ID, key)
	if !errors.Is(err, ErrVersion) || !strings.Contains(err.Error(), "Old PC") {
		t.Fatalf("want ErrVersion naming the peer, got %v", err)
	}
}

func TestTooLargeIsNotANetworkError(t *testing.T) {
	key := bytes.Repeat([]byte{7}, 32)
	srv, cli, serr, cerr := connect(t, key, key)
	if serr != nil || cerr != nil {
		t.Fatal(serr, cerr)
	}
	if err := cli.Send(MsgItem, make([]byte, MaxPayload+1)); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("want ErrTooLarge, got %v", err)
	}
	// The session still works afterwards.
	if err := cli.Send(MsgItem, []byte("ok")); err != nil {
		t.Fatal(err)
	}
	if _, got, err := srv.Recv(time.Second); err != nil || string(got) != "ok" {
		t.Fatalf("after too-large: %q %v", got, err)
	}
}
