// clipsync keeps the clipboards of two computers (Windows and Mac) in sync
// over the local network: copy on one, paste on the other.
package main

import (
	"bufio"
	"context"
	"crypto/rand"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"math/big"
	"net"
	"os"
	"os/signal"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"

	"clipsync/internal/clip"
	"clipsync/internal/config"
	"clipsync/internal/lan"
	"clipsync/internal/node"
	"clipsync/internal/wire"
)

const (
	version          = "0.2.0"
	maxPairAttempts  = 3
	pairingLifetime  = 10 * time.Minute
	discoveryTimeout = 60 * time.Second
)

var (
	commands         = []string{"run", "pair", "status", "unpair", "help", "version"}
	errBadCodeFormat = errors.New("type the 6-digit code (if you see Thai letters, switch the keyboard to English)")
)

func main() { os.Exit(run()) }

func run() int {
	fs := flag.NewFlagSet("clipsync", flag.ContinueOnError)
	fs.Usage = usage
	peerAddr := fs.String("peer", "", "")
	verbose := fs.Bool("v", false, "")
	flags, pos := splitArgs(os.Args[1:])
	if err := fs.Parse(flags); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	cmd := "run"
	if len(pos) > 0 {
		cmd, pos = pos[0], pos[1:]
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := dispatch(ctx, cmd, pos, *peerAddr, *verbose); err != nil {
		if ctx.Err() != nil {
			return 0 // stopped with Ctrl+C
		}
		fmt.Fprintln(os.Stderr, "\nError:", err)
		pauseOnWindows()
		return 1
	}
	return 0
}

func usage() {
	fmt.Print(`clipsync ` + version + ` — copy on one computer, paste on the other.

Usage:
  clipsync                 start syncing (asks to pair the first time)
  clipsync pair            show a pairing code on this computer
  clipsync pair CODE       pair using the code shown on the other computer
  clipsync status          show this computer and the paired one
  clipsync unpair          forget the paired computer

Options:
  -peer ADDRESS   the other computer's IP, if it cannot be found automatically
  -v              show a detailed log
`)
}

// splitArgs separates flags from positional arguments so flags work in any
// position, e.g. "clipsync -v pair 123456 -peer 192.168.1.5".
func splitArgs(args []string) (flags, pos []string) {
	for i := 0; i < len(args); i++ {
		a := args[i]
		if strings.HasPrefix(a, "-") && a != "-" {
			flags = append(flags, a)
			if strings.TrimLeft(a, "-") == "peer" && i+1 < len(args) {
				flags = append(flags, args[i+1])
				i++
			}
			continue
		}
		pos = append(pos, a)
	}
	return flags, pos
}

func dispatch(ctx context.Context, cmd string, pos []string, peerAddr string, verbose bool) error {
	if !slices.Contains(commands, cmd) {
		usage()
		return fmt.Errorf("unknown command %q", cmd)
	}
	switch cmd {
	case "help":
		usage()
		return nil
	case "version":
		fmt.Println("clipsync", version)
		return nil
	}

	dir, err := config.Dir()
	if err != nil {
		return err
	}
	cfg, err := config.Load(dir)
	if err != nil {
		return err
	}

	switch cmd {
	case "run":
		if cfg.Peer == nil {
			if err := interactivePair(ctx, cfg, dir, peerAddr); err != nil {
				return err
			}
		} else if peerAddr != "" {
			cfg.Peer.Addr = withDefaultPort(peerAddr)
			if err := cfg.Save(dir); err != nil {
				return err
			}
		}
		return runSync(ctx, cfg, dir, verbose)
	case "pair":
		if len(pos) > 0 {
			err = joinPairing(ctx, cfg, dir, strings.Join(pos, ""), peerAddr)
		} else {
			err = hostPairing(ctx, cfg, dir)
		}
		if err != nil {
			return err
		}
		return runSync(ctx, cfg, dir, verbose)
	case "status":
		fmt.Printf("This computer: %s\n", cfg.Name)
		fmt.Printf("Settings:      %s\n", config.Path(dir))
		fmt.Printf("Ports:         TCP %d, UDP %d\n", cfg.Port, cfg.BeaconPort)
		if cfg.Peer == nil {
			fmt.Println("Paired with:   nobody yet — run clipsync to pair")
		} else {
			fmt.Printf("Paired with:   %s (last seen at %s)\n", cfg.Peer.Name, orDash(cfg.Peer.Addr))
		}
		return nil
	default: // unpair
		if cfg.Peer == nil {
			fmt.Println("Not paired.")
			return nil
		}
		name := cfg.Peer.Name
		cfg.Peer = nil
		if err := cfg.Save(dir); err != nil {
			return err
		}
		fmt.Printf("Forgot %s. Run clipsync to pair again.\n", name)
		return nil
	}
}

func runSync(ctx context.Context, cfg *config.Config, dir string, verbose bool) error {
	cb, err := clip.NewSystem()
	if err != nil {
		return fmt.Errorf("cannot use the clipboard: %w", err)
	}
	setupConsole()
	logger := log.New(newAsyncWriter(os.Stdout), "", log.Ltime)
	n := node.New(cfg, dir, cb, logger, node.Options{Verbose: verbose})
	if err := n.Start(ctx); err != nil {
		return err
	}
	fmt.Printf("clipsync %s on %s, paired with %s.\n", version, cfg.Name, cfg.Peer.Name)
	fmt.Println("Copy on one computer, paste on the other. Keep this window open (Ctrl+C to stop).")
	n.Wait()
	return nil
}

// --- pairing ---------------------------------------------------------------

func interactivePair(ctx context.Context, cfg *config.Config, dir, peerAddr string) error {
	fmt.Printf("clipsync %s — this computer: %s\n\n", version, cfg.Name)
	fmt.Println("Not paired yet. Pair it with your other computer (only needed once):")
	fmt.Println("  1) Show a pairing code on this computer")
	fmt.Println("  2) Type the code shown on the other computer")
	for {
		choice, err := prompt(ctx, "Choose 1 or 2: ")
		if err != nil {
			return err
		}
		switch choice {
		case "1", "ๅ": // ๅ is the 1 key on a Thai keyboard
			return hostPairing(ctx, cfg, dir)
		case "2", "/": // / is the 2 key on a Thai keyboard
			return joinInteractive(ctx, cfg, dir, peerAddr)
		default:
			fmt.Println("Type 1 or 2 (switch the keyboard to English if needed).")
		}
	}
}

func joinInteractive(ctx context.Context, cfg *config.Config, dir, peerAddr string) error {
	wrong := 0
	for {
		code, err := prompt(ctx, "Code: ")
		if err != nil {
			return err
		}
		err = joinPairing(ctx, cfg, dir, code, peerAddr)
		switch {
		case errors.Is(err, errBadCodeFormat):
			fmt.Println(capitalize(err.Error()) + ".")
		case errors.Is(err, wire.ErrWrongCode):
			wrong++
			if wrong >= maxPairAttempts {
				return errors.New("wrong code 3 times — start pairing again on both computers")
			}
			fmt.Println("Wrong code. Check the code on the other computer and try again.")
		default:
			return err
		}
	}
}

func hostPairing(ctx context.Context, cfg *config.Config, dir string) error {
	code, err := newCode()
	if err != nil {
		return err
	}
	ln, err := net.Listen("tcp", fmt.Sprintf(":%d", cfg.Port))
	if err != nil {
		return fmt.Errorf("cannot open TCP port %d — is clipsync already running, or is another app using it? Close it first (%v)", cfg.Port, err)
	}
	defer ln.Close()
	snd, err := lan.Sender()
	if err != nil {
		return err
	}
	defer snd.Close()

	pctx, cancel := context.WithTimeout(ctx, pairingLifetime)
	defer cancel()
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

	fmt.Printf("\n    Pairing code:  %s %s\n\n", code[:3], code[3:])
	fmt.Println("On the other computer: run clipsync, choose 2, and type this code.")
	if ips := lan.LocalIPv4(); len(ips) > 0 {
		fmt.Printf("(This computer's address: %s)\n", strings.Join(ips, ", "))
	}
	fmt.Println("Waiting... (Ctrl+C to cancel)")

	self := wire.Identity{ID: cfg.DeviceID, Name: cfg.Name, Port: cfg.Port}
	res, err := wire.ServePairing(ln, self, code, maxPairAttempts, func(left int) {
		fmt.Printf("A wrong code was typed on the other computer (%d more tries for this code).\n", left)
	})
	switch {
	case err == nil:
		return savePeer(cfg, dir, res)
	case errors.Is(err, wire.ErrTooManyAttempts):
		return errors.New("3 failed tries — this code is no longer valid. Run clipsync again for a new code")
	case errors.Is(pctx.Err(), context.DeadlineExceeded):
		return errors.New("no one paired within 10 minutes — run clipsync again for a new code")
	case ctx.Err() != nil:
		return ctx.Err()
	}
	return err
}

func joinPairing(ctx context.Context, cfg *config.Config, dir, rawCode, peerAddr string) error {
	code, err := normalizeCode(rawCode)
	if err != nil {
		return err
	}
	addr := peerAddr
	if addr != "" {
		addr = withDefaultPort(addr)
	} else if addr, err = findPairingHost(ctx, cfg); err != nil {
		return err
	}
	fmt.Println("Pairing...")
	self := wire.Identity{ID: cfg.DeviceID, Name: cfg.Name, Port: cfg.Port}
	res, err := wire.JoinPair(ctx, addr, self, code)
	if errors.Is(err, wire.ErrWrongCode) || ctx.Err() != nil {
		return err
	}
	if err != nil {
		return fmt.Errorf("could not pair with %s: %v\nIf the other computer runs Windows, allow clipsync in its firewall and try again", addr, err)
	}
	return savePeer(cfg, dir, res)
}

const notFoundHelp = `could not find the other computer. Check that:
  - both computers are on the same Wi-Fi
  - the other computer is showing a pairing code (choose 1 there)
  - on Windows, clipsync is allowed through the firewall
  - on Mac, Terminal has Local Network access (System Settings > Privacy & Security)
Or give its address: clipsync pair CODE -peer 192.168.x.x`

func findPairingHost(ctx context.Context, cfg *config.Config) (string, error) {
	udp, err := lan.Listen(cfg.BeaconPort)
	if err != nil {
		return "", fmt.Errorf("cannot open UDP port %d — is clipsync already running, or is another app using it? Close it first (%v)", cfg.BeaconPort, err)
	}
	defer udp.Close()
	stop := context.AfterFunc(ctx, func() { udp.Close() })
	defer stop()
	udp.SetReadDeadline(time.Now().Add(discoveryTimeout))
	fmt.Println("Looking for the other computer on this network...")
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
		fmt.Printf("Found %s (%s)\n", b.Name, ip)
		return net.JoinHostPort(ip.String(), strconv.Itoa(b.Port)), nil
	}
}

func savePeer(cfg *config.Config, dir string, res *wire.PairResult) error {
	cfg.Peer = &config.Peer{ID: res.PeerID, Name: res.PeerName, Key: res.Key, Addr: res.PeerAddr}
	if err := cfg.Save(dir); err != nil {
		return err
	}
	fmt.Printf("\nPaired with %s.\n\n", res.PeerName)
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

// --- terminal helpers ------------------------------------------------------

var stdin = bufio.NewReader(os.Stdin)

// prompt reads one line, giving up if ctx ends (Ctrl+C) while waiting.
func prompt(ctx context.Context, q string) (string, error) {
	fmt.Print(q)
	type result struct {
		s   string
		err error
	}
	ch := make(chan result, 1)
	go func() {
		s, err := stdin.ReadString('\n')
		ch <- result{strings.TrimSpace(s), err}
	}()
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case r := <-ch:
		if r.err != nil && r.s == "" {
			return "", errors.New("no input")
		}
		return r.s, nil
	}
}

// asyncWriter hands log lines to a goroutine so a stalled console (for
// example text being selected in a Windows console) never stalls syncing.
// Lines are dropped if the console stays stuck long enough to fill the queue.
type asyncWriter struct{ ch chan []byte }

func newAsyncWriter(w io.Writer) *asyncWriter {
	a := &asyncWriter{ch: make(chan []byte, 256)}
	go func() {
		for b := range a.ch {
			w.Write(b)
		}
	}()
	return a
}

func (a *asyncWriter) Write(p []byte) (int, error) {
	select {
	case a.ch <- append([]byte(nil), p...):
	default:
	}
	return len(p), nil
}

// pauseOnWindows keeps a double-clicked console window open long enough to
// read the error.
func pauseOnWindows() {
	if runtime.GOOS != "windows" {
		return
	}
	fmt.Print("\nPress Enter to close this window...")
	stdin.ReadString('\n')
}

func capitalize(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
