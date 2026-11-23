// clipsync keeps the clipboards of two computers (Windows and Mac) in sync
// over the local network: copy on one, paste on the other.
package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"runtime"
	"slices"
	"strings"
	"syscall"
	"time"

	"clipsync/internal/clip"
	"clipsync/internal/config"
	"clipsync/internal/node"
)

const version = "0.3.0"

var commands = []string{"tray", "run", "pair", "status", "unpair", "help", "version", "pair-window"}

// errRunning means the menu-bar / tray copy holds the settings.
var errRunning = errors.New("clipsync is running in the menu bar / next to the clock — quit it first (click its icon, then Quit clipsync)")

func main() { os.Exit(run()) }

func run() int {
	fs := flag.NewFlagSet("clipsync", flag.ContinueOnError)
	fs.Usage = usage
	peerAddr := fs.String("peer", "", "")
	verbose := fs.Bool("v", false, "")
	firstRun := fs.Bool("first-run", false, "") // set by the Windows installer step
	flags, pos := splitArgs(os.Args[1:])
	cmd := "tray" // double-clicked, or started at login
	if len(pos) > 0 {
		cmd, pos = pos[0], pos[1:]
	}
	if cmd != "tray" {
		// Windows: a console for text; no-op elsewhere.
		useConsole(cmd == "run" || cmd == "pair" || cmd == "pair-window")
	}
	if err := fs.Parse(flags); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := dispatch(ctx, cmd, pos, *peerAddr, *verbose, *firstRun); err != nil {
		if ctx.Err() != nil || errors.Is(err, errCanceled) {
			return 0 // stopped with Ctrl+C, or the person cancelled
		}
		if cmd == "tray" {
			alert("clipsync could not start", err.Error())
		} else {
			fmt.Fprintln(os.Stderr, "\nError:", err)
			pauseOnWindows()
		}
		return 1
	}
	return 0
}

func usage() {
	fmt.Print(`clipsync ` + version + ` — copy on one computer, paste on the other.

Usage:
  clipsync                 start in the menu bar / system tray (pairs first if needed)
  clipsync run             sync in this window instead, showing a live log
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

func dispatch(ctx context.Context, cmd string, pos []string, peerAddr string, verbose, firstRun bool) error {
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
	if cmd == "tray" {
		return runTray(ctx, cfg, dir, peerAddr, verbose, firstRun)
	}
	// Commands that change settings or use the ports must not run beside
	// the tray copy (pair-window is started by the tray itself).
	if cmd == "run" || cmd == "pair" || cmd == "unpair" {
		release, err := acquireInstanceLock(dir)
		if err != nil {
			return errRunning
		}
		defer release()
	}
	if peerAddr != "" && cfg.Peer != nil && cmd == "run" {
		cfg.Peer.Addr = withDefaultPort(peerAddr)
		if err := cfg.Save(dir); err != nil {
			return err
		}
	}

	switch cmd {
	case "pair-window":
		fmt.Println("clipsync — pairing")
		fmt.Println()
		res, err := pairInteractive(ctx, cfg, dir, peerAddr, consoleUI{})
		if err != nil {
			return err
		}
		fmt.Printf("\nSyncing with %s starts again in the background. This window closes in a moment.\n", res.PeerName)
		time.Sleep(3 * time.Second)
		return nil
	case "run":
		if cfg.Peer == nil {
			if _, err := pairInteractive(ctx, cfg, dir, peerAddr, consoleUI{}); err != nil {
				return err
			}
			fmt.Println()
		}
		return runSync(ctx, cfg, dir, verbose)
	case "pair":
		ui := consoleUI{}
		if len(pos) > 0 {
			_, err = joinPairing(ctx, cfg, dir, strings.Join(pos, ""), peerAddr, ui)
		} else {
			_, err = hostPairing(ctx, cfg, dir, ui)
		}
		if err != nil {
			return err
		}
		fmt.Println("Now start clipsync on both computers.")
		pauseOnWindows()
		return nil
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

// runSync is the console mode: sync in this window with a live log.
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
	in := stdin
	go func() {
		s, err := in.ReadString('\n')
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

// pauseOnWindows keeps a console window that clipsync opened long enough to
// read the result.
func pauseOnWindows() {
	if runtime.GOOS != "windows" || !ownConsole {
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
