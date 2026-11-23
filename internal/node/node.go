// Package node keeps the two clipboards in sync: it watches this one, keeps
// a connection to the paired computer, and applies what arrives.
package node

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"log"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"clipsync/internal/clip"
	"clipsync/internal/config"
	"clipsync/internal/lan"
	"clipsync/internal/wire"
)

const (
	maxText      = 16 << 20
	maxImage     = 40 << 20
	pingEvery    = 10 * time.Second
	readTimeout  = 35 * time.Second
	beaconEvery  = 3 * time.Second
	resendWithin = 2 * time.Minute // unconfirmed copies older than this are dropped
	maxHandshake = 8               // concurrent logins from not-yet-trusted connections
	// tieBreakWindow: two sessions this close together are a simultaneous
	// dial; an older session that a new one challenges is presumed dead.
	tieBreakWindow = 5 * time.Second
	warnAfterFails = 6
	// maxReadAttempts: polls spent on one copy whose read keeps failing.
	maxReadAttempts = 3
)

type Options struct {
	PollInterval time.Duration // how often to check the clipboard; default 250ms
	RedialEvery  time.Duration // how often to retry the peer; default 5s
	Verbose      bool
	// OnStatus, if set, is called (from any goroutine) whenever the
	// connection or the last transfer changes.
	OnStatus func(Status)
}

// Status is what a tray icon shows.
type Status struct {
	Connected bool
	Peer      string
	Paused    bool
	Last      string // e.g. "Sent text, 12 characters"
	LastAt    time.Time
}

// outItem is a local copy the peer has not confirmed yet.
type outItem struct {
	item clip.Item
	hash [32]byte
	at   time.Time
}

type Node struct {
	cfg  *config.Config
	dir  string // where to save config changes; "" means don't save
	clip clip.Clipboard
	log  *log.Logger
	opt  Options

	ln     net.Listener
	udp    *net.UDPConn
	port   int
	hsSem  chan struct{}
	wg     sync.WaitGroup
	saveMu sync.Mutex
	// clipMu makes "read a local copy and record it" and "write a received
	// item and record it" happen one at a time, so neither can overwrite
	// the other's bookkeeping halfway.
	clipMu sync.Mutex

	mu        sync.Mutex
	sess      *wire.Session
	lastHash  [32]byte // what both clipboards hold, as far as we know
	hasHash   bool
	pending   *outItem
	dialing   bool
	dialFails int
	candidate string // address from a beacon; not trusted until a login works
	paused    bool
	status    Status
	// ignoreSeq is the clipboard counter right after we wrote a received
	// item, so that write is not mistaken for a new copy (Windows reads an
	// image back in a different encoding, so the hash alone is not enough).
	ignoreSeq int64
	hasIgnore bool
	throttle  map[string]time.Time
}

func New(cfg *config.Config, dir string, cb clip.Clipboard, logger *log.Logger, opt Options) *Node {
	if opt.PollInterval == 0 {
		opt.PollInterval = 250 * time.Millisecond
	}
	if opt.RedialEvery == 0 {
		opt.RedialEvery = 5 * time.Second
	}
	return &Node{
		cfg: cfg, dir: dir, clip: cb, log: logger, opt: opt,
		hsSem:    make(chan struct{}, maxHandshake),
		throttle: map[string]time.Time{},
	}
}

// Start opens the ports and begins syncing in the background.
func (n *Node) Start(ctx context.Context) error {
	if n.cfg.Peer == nil {
		return errors.New("not paired")
	}
	ln, err := net.Listen("tcp", fmt.Sprintf(":%d", n.cfg.Port))
	if err != nil {
		return fmt.Errorf("cannot open TCP port %d — is clipsync already running, or is another app using it? (%v)", n.cfg.Port, err)
	}
	n.ln = ln
	n.port = ln.Addr().(*net.TCPAddr).Port
	if n.cfg.BeaconPort != 0 {
		udp, err := lan.Listen(n.cfg.BeaconPort)
		if err != nil {
			ln.Close()
			return fmt.Errorf("cannot open UDP port %d — is clipsync already running, or is another app using it? (%v)", n.cfg.BeaconPort, err)
		}
		n.udp = udp
	}

	n.run(func() {
		<-ctx.Done()
		n.ln.Close()
		if n.udp != nil {
			n.udp.Close()
		}
		n.mu.Lock()
		if n.sess != nil {
			n.sess.Close()
			n.sess = nil
		}
		n.mu.Unlock()
	})
	n.run(func() { n.acceptLoop(ctx) })
	n.run(func() { n.redialLoop(ctx) })
	n.run(func() { n.clipLoop(ctx) })
	if n.udp != nil {
		n.run(func() { n.beaconSendLoop(ctx) })
		n.run(func() { n.beaconRecvLoop(ctx) })
	}
	return nil
}

func (n *Node) run(f func()) {
	n.wg.Add(1)
	go func() {
		defer n.wg.Done()
		f()
	}()
}

// Wait blocks until the context given to Start is done and loops have ended.
func (n *Node) Wait() { n.wg.Wait() }

// ListenAddr is the local TCP address (useful when Port was 0).
func (n *Node) ListenAddr() string { return n.ln.Addr().String() }

// Connected reports whether a session with the peer is up.
func (n *Node) Connected() bool {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.sess != nil
}

// SetPeerAddr tells the node where the peer listens.
func (n *Node) SetPeerAddr(addr string) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.cfg.Peer.Addr = addr
}

// SetPaused stops (or resumes) sending local copies and applying received
// ones. Copies made while paused are never sent.
func (n *Node) SetPaused(p bool) {
	n.mu.Lock()
	n.paused = p
	if p {
		n.pending = nil
	}
	n.mu.Unlock()
	n.report(func(st *Status) { st.Paused = p })
}

// report applies change to the status and passes a copy to OnStatus.
func (n *Node) report(change func(*Status)) {
	n.mu.Lock()
	change(&n.status)
	n.status.Peer = n.cfg.Peer.Name
	st := n.status
	n.mu.Unlock()
	if n.opt.OnStatus != nil {
		n.opt.OnStatus(st)
	}
}

func (n *Node) noteTransfer(msg string) {
	n.report(func(st *Status) { st.Last, st.LastAt = msg, time.Now() })
}

func (n *Node) self() wire.Identity {
	return wire.Identity{ID: n.cfg.DeviceID, Name: n.cfg.Name, Port: n.port}
}

func (n *Node) peer() config.Peer {
	n.mu.Lock()
	defer n.mu.Unlock()
	return *n.cfg.Peer
}

// --- connections -----------------------------------------------------------

func (n *Node) acceptLoop(ctx context.Context) {
	for {
		conn, err := n.ln.Accept()
		if err != nil {
			if ctx.Err() != nil || errors.Is(err, net.ErrClosed) {
				return
			}
			time.Sleep(100 * time.Millisecond)
			continue
		}
		select {
		case n.hsSem <- struct{}{}:
			go func() {
				defer func() { <-n.hsSem }()
				n.handleIncoming(ctx, conn)
			}()
		default:
			conn.Close() // too many logins in progress; the peer will retry
		}
	}
}

func (n *Node) handleIncoming(ctx context.Context, conn net.Conn) {
	conn.SetDeadline(time.Now().Add(5 * time.Second))
	h, err := wire.ReadHello(conn)
	if errors.Is(err, wire.ErrVersion) {
		wire.SendVersionHello(conn, n.self())
		n.logThrottled("version", 5*time.Minute, "%v", err)
		conn.Close()
		return
	}
	if err != nil {
		n.debugf("ignored connection from %s: %v", conn.RemoteAddr(), err)
		conn.Close()
		return
	}
	peer := n.peer()
	if h.Mode != wire.ModeSync {
		n.debugf("ignored a pairing request from %q; run 'clipsync pair' to pair again", h.Name)
		conn.Close()
		return
	}
	if h.ID != peer.ID {
		n.logThrottled("stranger", time.Minute, "Ignored %q at %s — it is not paired with this computer", h.Name, conn.RemoteAddr())
		conn.Close()
		return
	}
	s, err := wire.AcceptSync(conn, h, n.self(), peer.Key)
	if err != nil {
		if errors.Is(err, wire.ErrBadKey) {
			n.logThrottled("badkey", time.Minute, "A computer calling itself %q at %s could not log in (keys do not match). If this keeps happening, run 'clipsync unpair' on both and pair again.", peer.Name, conn.RemoteAddr())
		} else {
			n.debugf("login from %s failed: %v", conn.RemoteAddr(), err)
		}
		conn.Close()
		return
	}
	n.adopt(ctx, s)
}

func (n *Node) redialLoop(ctx context.Context) {
	n.tryDial(ctx)
	t := time.NewTicker(n.opt.RedialEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			n.tryDial(ctx)
		}
	}
}

// tryDial connects to the peer if no session is up: first at the address a
// beacon suggested, then at the address of the last successful login.
func (n *Node) tryDial(ctx context.Context) {
	n.mu.Lock()
	if n.sess != nil || n.dialing || ctx.Err() != nil {
		n.mu.Unlock()
		return
	}
	var targets []string
	if n.candidate != "" {
		targets = append(targets, n.candidate)
	}
	if a := n.cfg.Peer.Addr; a != "" && a != n.candidate {
		targets = append(targets, a)
	}
	n.candidate = ""
	if len(targets) == 0 {
		n.mu.Unlock()
		return
	}
	n.dialing = true
	peer := *n.cfg.Peer
	n.mu.Unlock()

	go func() {
		defer func() {
			n.mu.Lock()
			n.dialing = false
			n.mu.Unlock()
		}()
		var lastErr error
		for _, addr := range targets {
			s, err := wire.Dial(ctx, addr, n.self(), peer.ID, peer.Key)
			if err == nil {
				n.adopt(ctx, s)
				return
			}
			lastErr = err
			n.debugf("connect to %s at %s: %v", peer.Name, addr, err)
		}
		if ctx.Err() != nil {
			return
		}
		n.mu.Lock()
		n.dialFails++
		fails := n.dialFails
		n.mu.Unlock()
		switch {
		case errors.Is(lastErr, wire.ErrVersion):
			n.logThrottled("version", 5*time.Minute, "%v", lastErr)
		case errors.Is(lastErr, wire.ErrBadKey):
			n.logThrottled("badkey", time.Minute, "Reached %q but could not log in (keys do not match). Run 'clipsync unpair' on both and pair again.", peer.Name)
		case fails >= warnAfterFails:
			n.logThrottled("unreachable", 5*time.Minute,
				"Still cannot reach %s (last tried %s). Check: clipsync is running there; both are on the same Wi-Fi; "+
					"on Windows, clipsync is allowed in the firewall; on Mac, Terminal has Local Network access "+
					"(System Settings > Privacy & Security > Local Network). If its IP changed, run: clipsync -peer NEW-IP",
				peer.Name, targets[len(targets)-1])
		}
	}()
}

// adopt makes s the active session. When both computers dial each other at
// once, both keep the connection opened by the computer with the smaller ID
// and drop the other, so they always agree. Otherwise the newest session
// wins: a computer only dials when it has no working session.
func (n *Node) adopt(ctx context.Context, s *wire.Session) {
	if ctx.Err() != nil {
		s.Close()
		return
	}
	preferred := func(x *wire.Session) bool {
		dialer, listener := x.PeerID, n.cfg.DeviceID
		if x.Dialed {
			dialer, listener = n.cfg.DeviceID, x.PeerID
		}
		return dialer < listener
	}
	n.mu.Lock()
	old := n.sess
	if old != nil && preferred(old) && !preferred(s) && s.Started.Sub(old.Started) < tieBreakWindow {
		n.mu.Unlock()
		s.Close()
		return
	}
	n.sess = s
	n.dialFails = 0
	n.candidate = ""
	// We no longer know what the peer holds; resend our last copy if the
	// peer never confirmed it and it is still fresh.
	n.hasHash = false
	var resend *outItem
	if n.pending != nil && time.Since(n.pending.at) <= resendWithin {
		resend = n.pending
		n.lastHash, n.hasHash = resend.hash, true
	} else {
		n.pending = nil
	}
	name := n.cfg.Peer.Name
	n.mu.Unlock()

	if old != nil {
		old.Close()
	} else {
		n.logf("Connected to %s (%s)", name, s.RemoteHost())
		n.report(func(st *Status) { st.Connected = true })
	}
	n.notePeerAddr(s.PeerAddr())
	go n.serve(ctx, s)
	if resend != nil {
		n.send(s, resend.item, name, " (copied while disconnected)")
	}
}

func (n *Node) serve(ctx context.Context, s *wire.Session) {
	done := make(chan struct{})
	defer func() {
		close(done)
		s.Close()
		n.mu.Lock()
		current := n.sess == s
		if current {
			n.sess = nil
		}
		name := n.cfg.Peer.Name
		n.mu.Unlock()
		if current && ctx.Err() == nil {
			n.logf("Lost connection to %s — reconnecting automatically", name)
		}
		if current {
			n.report(func(st *Status) { st.Connected = false })
		}
	}()
	go func() {
		t := time.NewTicker(pingEvery)
		defer t.Stop()
		for {
			select {
			case <-done:
				return
			case <-t.C:
				if err := s.Send(wire.MsgPing, nil); err != nil {
					s.Close()
					return
				}
			}
		}
	}()
	for {
		typ, payload, err := s.Recv(readTimeout)
		if err != nil {
			n.debugf("session ended: %v", err)
			return
		}
		switch typ {
		case wire.MsgPing:
		case wire.MsgAck:
			n.mu.Lock()
			if n.pending != nil && bytes.Equal(n.pending.hash[:], payload) {
				n.pending = nil
			}
			n.mu.Unlock()
		case wire.MsgItem:
			it, err := decodeItem(payload)
			if err != nil {
				n.logf("Dropped an item from %s: %v", n.peer().Name, err)
				continue
			}
			n.receive(ctx, s, it)
		default:
			n.debugf("ignored message type %d", typ)
		}
	}
}

// --- clipboard -------------------------------------------------------------

func (n *Node) clipLoop(ctx context.Context) {
	last := n.clip.Seq()
	attempts := 0
	t := time.NewTicker(n.opt.PollInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		cur := n.clip.Seq()
		if cur == last {
			continue
		}
		n.mu.Lock()
		ours := n.hasIgnore && cur == n.ignoreSeq
		n.mu.Unlock()
		if ours {
			last = cur
			continue // our own write of a received item
		}
		n.mu.Lock()
		paused := n.paused
		n.mu.Unlock()
		if paused {
			last, attempts = cur, 0
			continue
		}
		// A read that fails for a passing reason (another app holding the
		// clipboard) is retried on the next ticks before giving up.
		attempts++
		if n.onLocalCopy(ctx, cur, attempts >= maxReadAttempts) || attempts >= maxReadAttempts {
			last, attempts = cur, 0
		}
	}
}

// onLocalCopy handles a new local copy. It returns false when reading
// failed for a reason worth retrying; lastTry makes it report that failure.
func (n *Node) onLocalCopy(ctx context.Context, seq int64, lastTry bool) bool {
	n.clipMu.Lock()
	it, ok, retry := n.takeLocalCopy(ctx, seq, lastTry)
	n.clipMu.Unlock()
	if retry {
		return false
	}
	if !ok {
		return true
	}
	n.mu.Lock()
	s := n.sess
	name := n.cfg.Peer.Name
	n.mu.Unlock()
	if s == nil {
		n.logThrottled("offline", time.Minute, "Copied, but %s is not connected — it will get it on reconnect (within 2 minutes)", name)
		return true
	}
	n.send(s, it, name, "")
	return true
}

// takeLocalCopy reads a new local copy and records it as pending. Call with
// clipMu held. retry reports a passing failure worth another try.
func (n *Node) takeLocalCopy(ctx context.Context, seq int64, lastTry bool) (_ clip.Item, ok, retry bool) {
	it, err := n.clip.Read(ctx)
	switch {
	case errors.Is(err, clip.ErrSensitive):
		n.logf("Password copied — not sent (password-manager items stay on this computer)")
		return clip.Item{}, false, false
	case errors.Is(err, clip.ErrFiles):
		n.logThrottled("files", 10*time.Minute, "Files copied — not sent (only text and images are synced)")
		return clip.Item{}, false, false
	case errors.Is(err, clip.ErrNothing):
		return clip.Item{}, false, false
	case errors.Is(err, clip.ErrBusy):
		return clip.Item{}, false, !lastTry
	case err != nil:
		if lastTry {
			n.logThrottled("readerr", time.Minute, "Could not read what was copied: %v", err)
		}
		return clip.Item{}, false, !lastTry
	}
	if n.clip.Seq() != seq {
		return clip.Item{}, false, false // changed while we read it; the next tick handles it
	}
	if len(it.Text) > maxText {
		n.logf("Copied text is too large to send (%d MB)", len(it.Text)>>20)
		return clip.Item{}, false, false
	}
	if len(it.Image) > maxImage {
		if len(it.Text) == 0 {
			n.logf("Copied image is too large to send (%s)", size(len(it.Image)))
			return clip.Item{}, false, false
		}
		it.Image = nil // still send the text
	}
	h := hashItem(it)
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.hasHash && h == n.lastHash {
		return clip.Item{}, false, false // what we just received, or already sent
	}
	n.lastHash, n.hasHash = h, true
	n.pending = &outItem{item: it, hash: h, at: time.Now()}
	return it, true, false
}

func (n *Node) send(s *wire.Session, it clip.Item, name, note string) {
	if err := s.Send(wire.MsgItem, encodeItem(it)); err != nil {
		if errors.Is(err, wire.ErrTooLarge) {
			n.logf("Not sent to %s: %v", name, err)
			n.mu.Lock()
			n.pending = nil
			n.mu.Unlock()
			return
		}
		n.logf("Could not send to %s (will retry on reconnect): %v", name, err)
		s.Close()
		return
	}
	n.logf("Sent to %s: %s%s", name, describe(it), note)
	n.noteTransfer("Sent " + describe(it))
}

func (n *Node) receive(ctx context.Context, s *wire.Session, it clip.Item) {
	h := hashItem(it)
	defer s.Send(wire.MsgAck, h[:])
	n.clipMu.Lock()
	defer n.clipMu.Unlock()
	n.mu.Lock()
	if n.paused {
		n.mu.Unlock()
		return // acknowledged but not applied
	}
	if n.hasHash && h == n.lastHash {
		n.mu.Unlock()
		return
	}
	prevHash, prevHas := n.lastHash, n.hasHash
	n.lastHash, n.hasHash = h, true
	n.pending = nil // the peer's copy is newer than any unconfirmed one of ours
	name := n.cfg.Peer.Name
	n.mu.Unlock()

	before := n.clip.Seq()
	err := n.clip.Write(ctx, it)
	after := n.clip.Seq()
	n.mu.Lock()
	if after != before {
		// Whatever we put there (even half of it, if the write failed) is
		// ours, not a new copy to send back.
		n.ignoreSeq, n.hasIgnore = after, true
	}
	if err != nil {
		n.lastHash, n.hasHash = prevHash, prevHas
	}
	n.mu.Unlock()
	if err != nil {
		n.logf("Received %s from %s but could not put it on the clipboard: %v", describe(it), name, err)
		return
	}
	n.logf("Received from %s: %s — ready to paste", name, describe(it))
	n.noteTransfer("Received " + describe(it))
}

// --- discovery -------------------------------------------------------------

func (n *Node) beaconSendLoop(ctx context.Context) {
	b := lan.NewBeacon(lan.KindHere, n.cfg.DeviceID, n.cfg.Name, n.port)
	t := time.NewTicker(beaconEvery)
	defer t.Stop()
	for {
		if err := lan.Broadcast(n.udp, n.cfg.BeaconPort, b); err != nil {
			n.debugf("broadcast: %v", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func (n *Node) beaconRecvLoop(ctx context.Context) {
	for {
		b, ip, err := lan.Read(n.udp)
		if err != nil {
			if ctx.Err() != nil || errors.Is(err, net.ErrClosed) {
				return
			}
			time.Sleep(100 * time.Millisecond)
			continue
		}
		if b.Kind != lan.KindHere || b.ID != n.peer().ID {
			continue
		}
		// Beacons are unauthenticated: only a hint for the next dial. The
		// address is saved after a login there succeeds.
		addr := net.JoinHostPort(ip.String(), strconv.Itoa(b.Port))
		n.mu.Lock()
		if n.sess == nil && addr != n.cfg.Peer.Addr {
			n.candidate = addr
		}
		n.mu.Unlock()
		n.tryDial(ctx)
	}
}

// notePeerAddr saves where the peer is after a successful login, so a
// restart reconnects at once.
func (n *Node) notePeerAddr(addr string) {
	if addr == "" {
		return
	}
	n.saveMu.Lock()
	defer n.saveMu.Unlock()
	n.mu.Lock()
	if n.cfg.Peer.Addr == addr {
		n.mu.Unlock()
		return
	}
	n.cfg.Peer.Addr = addr
	snapshot := *n.cfg
	peer := *n.cfg.Peer
	snapshot.Peer = &peer
	n.mu.Unlock()

	n.debugf("peer is now at %s", addr)
	if n.dir != "" {
		if err := snapshot.Save(n.dir); err != nil {
			n.logf("Could not save settings: %v", err)
		}
	}
}

// --- helpers ---------------------------------------------------------------

func hashItem(it clip.Item) [32]byte {
	h := sha256.New()
	var n [8]byte
	binary.BigEndian.PutUint64(n[:], uint64(len(it.Text)))
	h.Write(n[:])
	h.Write(it.Text)
	h.Write(it.Image)
	var out [32]byte
	copy(out[:], h.Sum(nil))
	return out
}

// encodeItem lays out an item as: 4-byte text length, text, PNG bytes.
func encodeItem(it clip.Item) []byte {
	b := make([]byte, 4+len(it.Text)+len(it.Image))
	binary.BigEndian.PutUint32(b, uint32(len(it.Text)))
	copy(b[4:], it.Text)
	copy(b[4+len(it.Text):], it.Image)
	return b
}

func decodeItem(b []byte) (clip.Item, error) {
	if len(b) < 4 {
		return clip.Item{}, errors.New("malformed item")
	}
	n := binary.BigEndian.Uint32(b)
	if uint64(n) > uint64(len(b)-4) {
		return clip.Item{}, errors.New("malformed item")
	}
	it := clip.Item{Text: b[4 : 4+n], Image: b[4+n:]}
	if len(it.Text) == 0 {
		it.Text = nil
	}
	if len(it.Image) == 0 {
		it.Image = nil
	}
	if !utf8.Valid(it.Text) {
		return clip.Item{}, errors.New("text is not valid UTF-8")
	}
	it.Text = clip.NormalizeText(it.Text)
	if it.Image != nil {
		if err := clip.CheckPNG(it.Image); err != nil {
			if it.Text == nil {
				return clip.Item{}, err
			}
			it.Image = nil // keep the text
		}
	}
	if it.Empty() {
		return clip.Item{}, errors.New("empty item")
	}
	return it, nil
}

func describe(it clip.Item) string {
	var parts []string
	if len(it.Text) > 0 {
		parts = append(parts, fmt.Sprintf("text, %d characters", utf8.RuneCount(it.Text)))
	}
	if len(it.Image) > 0 {
		w, h := clip.ImageSize(it.Image)
		parts = append(parts, fmt.Sprintf("image %dx%d, %s", w, h, size(len(it.Image))))
	}
	return strings.Join(parts, " + ")
}

func size(n int) string {
	if n >= 1<<20 {
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	}
	return fmt.Sprintf("%d KB", (n+1023)/1024)
}

// Logging never happens while n.mu is held, and the writer given by main
// never blocks, so a stalled console cannot stall syncing.

func (n *Node) logf(format string, a ...any) { n.log.Printf(format, a...) }

func (n *Node) debugf(format string, a ...any) {
	if n.opt.Verbose {
		n.log.Printf("[debug] "+format, a...)
	}
}

// logThrottled logs at most once per interval for each key.
func (n *Node) logThrottled(key string, every time.Duration, format string, a ...any) {
	n.mu.Lock()
	now := time.Now()
	ok := now.Sub(n.throttle[key]) >= every
	if ok {
		n.throttle[key] = now
	}
	n.mu.Unlock()
	if ok {
		n.logf(format, a...)
	}
}
