package main

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"math/big"
	"net"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"clipsync/internal/config"
	"clipsync/internal/lan"
	"clipsync/internal/wire"
)

const (
	maxPairAttempts  = 3
	pairingLifetime  = 10 * time.Minute
	discoveryTimeout = 60 * time.Second
)

var (
	errBadCodeFormat = errors.New("type the 6-digit code (if you see Thai letters, switch the keyboard to English)")
	errCanceled      = errors.New("canceled")
)

// pairUI is how pairing talks to the person: a console, or dialog boxes
// when clipsync runs as a menu-bar/tray app.
type pairUI interface {
	// Choose asks whether this computer shows a code (false) or types the
	// code shown on the other computer (true).
	Choose(ctx context.Context, thisName string) (join bool, err error)
	// AskCode asks for the code; note explains why it asks again, if it does.
	AskCode(ctx context.Context, note string) (string, error)
	// ShowCode displays the code until close is called. cancelled is closed
	// if the person dismisses it.
	ShowCode(code string, addrs []string) (cancelled <-chan struct{}, close func())
	// Info reports progress.
	Info(msg string)
}

// consoleUI is the text version, used in a terminal or console window.
type consoleUI struct{}

func (consoleUI) Choose(ctx context.Context, thisName string) (bool, error) {
	fmt.Printf("clipsync %s — this computer: %s\n\n", version, thisName)
	fmt.Println("Pair it with your other computer (only needed once):")
	fmt.Println("  1) Show a pairing code on this computer")
	fmt.Println("  2) Type the code shown on the other computer")
	for {
		choice, err := prompt(ctx, "Choose 1 or 2: ")
		if err != nil {
			return false, err
		}
		switch choice {
		case "1", "ๅ": // ๅ is the 1 key on a Thai keyboard
			return false, nil
		case "2", "/": // / is the 2 key on a Thai keyboard
			return true, nil
		default:
			fmt.Println("Type 1 or 2 (switch the keyboard to English if needed).")
		}
	}
}

func (consoleUI) AskCode(ctx context.Context, note string) (string, error) {
	if note != "" {
		fmt.Println(note)
	}
	return prompt(ctx, "Code: ")
}

func (consoleUI) ShowCode(code string, addrs []string) (<-chan struct{}, func()) {
	fmt.Printf("\n    Pairing code:  %s %s\n\n", code[:3], code[3:])
	fmt.Println("On the other computer: choose to type a code, and type this one.")
	if len(addrs) > 0 {
		fmt.Printf("(This computer's address: %s)\n", strings.Join(addrs, ", "))
	}
	fmt.Println("Waiting... (Ctrl+C to cancel)")
	return make(chan struct{}), func() {}
}

func (consoleUI) Info(msg string) { fmt.Println(msg) }

// pairInteractive runs the whole pairing conversation.
func pairInteractive(ctx context.Context, cfg *config.Config, dir, peerAddr string, ui pairUI) (*wire.PairResult, error) {
	join, err := ui.Choose(ctx, cfg.Name)
	if err != nil {
		return nil, err
	}
	if !join {
		return hostPairing(ctx, cfg, dir, ui)
	}
	note := ""
	wrong := 0
	for {
		code, err := ui.AskCode(ctx, note)
		if err != nil {
			return nil, err
		}
		res, err := joinPairing(ctx, cfg, dir, code, peerAddr, ui)
		switch {
		case errors.Is(err, errBadCodeFormat):
			note = capitalize(err.Error()) + "."
		case errors.Is(err, wire.ErrWrongCode):
			wrong++
			if wrong >= maxPairAttempts {
				return nil, errors.New("wrong code 3 times — start pairing again on both computers")
			}
			note = "Wrong code. Check the code on the other computer and try again."
		default:
			return res, err
		}
	}
}

func hostPairing(ctx context.Context, cfg *config.Config, dir string, ui pairUI) (*wire.PairResult, error) {
	code, err := newCode()
	if err != nil {
		return nil, err
	}
	ln, err := net.Listen("tcp", fmt.Sprintf(":%d", cfg.Port))
	if err != nil {
		return nil, fmt.Errorf("cannot open TCP port %d — is clipsync already running, or is another app using it? Close it first (%v)", cfg.Port, err)
	}
	defer ln.Close()
	snd, err := lan.Sender()
	if err != nil {
		return nil, err
	}
	defer snd.Close()

	pctx, cancel := context.WithTimeout(ctx, pairingLifetime)
	defer cancel()
	var userCanceled atomic.Bool
	cancelled, closeShow := ui.ShowCode(code, lan.LocalIPv4())
	defer closeShow()
	go func() {
		select {
		case <-cancelled:
			userCanceled.Store(true)
			cancel()
		case <-pctx.Done():
		}
	}()
	stop := context.AfterFunc(pctx, func() { ln.Close() })
	defer stop()
	go func() {
		b := lan.NewBeacon(lan.KindPair, cfg.DeviceID, cfg.Name, cfg.Port)
		t := time.NewTicker(time.Second)
		defer t.Stop()
		for {
			lan.Broadcast(snd, cfg.BeaconPort, b)
			select {
			case <-pctx.Done():
				return
			case <-t.C:
			}
		}
	}()

	self := wire.Identity{ID: cfg.DeviceID, Name: cfg.Name, Port: cfg.Port}
	res, err := wire.ServePairing(ln, self, code, maxPairAttempts, func(left int) {
		ui.Info(fmt.Sprintf("A wrong code was typed on the other computer (%d more tries for this code).", left))
	})
	switch {
	case err == nil:
		return res, savePeer(cfg, dir, res, ui)
	case errors.Is(err, wire.ErrTooManyAttempts):
		return nil, errors.New("3 failed tries — this code is no longer valid. Start pairing again for a new code")
	case ctx.Err() != nil:
		return nil, ctx.Err()
	case userCanceled.Load():
		return nil, errCanceled
	case errors.Is(pctx.Err(), context.DeadlineExceeded):
		return nil, errors.New("no one paired within 10 minutes — start pairing again for a new code")
	}
	return nil, err
}

func joinPairing(ctx context.Context, cfg *config.Config, dir, rawCode, peerAddr string, ui pairUI) (*wire.PairResult, error) {
	code, err := normalizeCode(rawCode)
	if err != nil {
		return nil, err
	}
	addr := peerAddr
	if addr != "" {
		addr = withDefaultPort(addr)
	} else if addr, err = findPairingHost(ctx, cfg, ui); err != nil {
		return nil, err
	}
	ui.Info("Pairing...")
	self := wire.Identity{ID: cfg.DeviceID, Name: cfg.Name, Port: cfg.Port}
	res, err := wire.JoinPair(ctx, addr, self, code)
	if errors.Is(err, wire.ErrWrongCode) || ctx.Err() != nil {
		return nil, err
	}
	if err != nil {
		return nil, fmt.Errorf("could not pair with %s: %v\nIf the other computer runs Windows, allow clipsync in its firewall and try again", addr, err)
	}
	return res, savePeer(cfg, dir, res, ui)
}

const notFoundHelp = `could not find the other computer. Check that:
  - both computers are on the same Wi-Fi
  - the other computer is showing a pairing code
  - on Windows, clipsync is allowed through the firewall
  - on Mac, clipsync (or Terminal) has Local Network access (System Settings > Privacy & Security)`

func findPairingHost(ctx context.Context, cfg *config.Config, ui pairUI) (string, error) {
	udp, err := lan.Listen(cfg.BeaconPort)
	if err != nil {
		return "", fmt.Errorf("cannot open UDP port %d — is clipsync already running, or is another app using it? Close it first (%v)", cfg.BeaconPort, err)
	}
	defer udp.Close()
	stop := context.AfterFunc(ctx, func() { udp.Close() })
	defer stop()
	udp.SetReadDeadline(time.Now().Add(discoveryTimeout))
	ui.Info("Looking for the other computer on this network...")
	for {
		b, ip, err := lan.Read(udp)
		if err != nil {
			if ctx.Err() != nil {
				return "", ctx.Err()
			}
			return "", errors.New(notFoundHelp)
		}
		if b.Kind != lan.KindPair || b.ID == cfg.DeviceID {
			continue
		}
		ui.Info(fmt.Sprintf("Found %s (%s)", b.Name, ip))
		return net.JoinHostPort(ip.String(), strconv.Itoa(b.Port)), nil
	}
}

func savePeer(cfg *config.Config, dir string, res *wire.PairResult, ui pairUI) error {
	cfg.Peer = &config.Peer{ID: res.PeerID, Name: res.PeerName, Key: res.Key, Addr: res.PeerAddr}
	if err := cfg.Save(dir); err != nil {
		return err
	}
	ui.Info(fmt.Sprintf("Paired with %s.", res.PeerName))
	return nil
}

func newCode() (string, error) {
	n, err := rand.Int(rand.Reader, big.NewInt(1_000_000))
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%06d", n.Int64()), nil
}

// kedmanee maps the number-row keys of a Thai keyboard to the digits printed
// on them, so a code typed without switching layout still works.
var kedmanee = map[rune]rune{
	'ๅ': '1', '/': '2', '-': '3', 'ภ': '4', 'ถ': '5',
	'ุ': '6', 'ึ': '7', 'ค': '8', 'ต': '9', 'จ': '0',
}

func normalizeCode(s string) (string, error) {
	s = strings.TrimSpace(s)
	thaiLayout := strings.ContainsFunc(s, func(r rune) bool { return r >= 0x0E01 && r <= 0x0E4F })
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		case r >= '๐' && r <= '๙': // Thai digits
			b.WriteRune('0' + (r - '๐'))
		case thaiLayout && kedmanee[r] != 0:
			b.WriteRune(kedmanee[r])
		case r == ' ' || r == '-':
		default:
			return "", errBadCodeFormat
		}
	}
	if b.Len() != 6 {
		return "", errBadCodeFormat
	}
	return b.String(), nil
}

func withDefaultPort(addr string) string {
	if _, _, err := net.SplitHostPort(addr); err != nil {
		return net.JoinHostPort(addr, strconv.Itoa(config.DefaultPort))
	}
	return addr
}
