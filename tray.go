package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"

	"fyne.io/systray"

	"clipsync/internal/clip"
	"clipsync/internal/config"
	"clipsync/internal/node"
)

// trayApp is clipsync as a menu-bar (Mac) or notification-area (Windows)
// icon: no window to keep open, a small menu, and a log file.
type trayApp struct {
	ctx      context.Context
	cancel   context.CancelFunc
	dir      string
	verbose  bool
	firstRun bool
	log      *log.Logger
	logPath  string
	clip     clip.Clipboard

	mu       sync.Mutex
	cfg      *config.Config
	node     *node.Node
	stopNode context.CancelFunc
	paused   bool
	pairing  bool

	// All menu text and the icon are updated by uiLoop alone, from ui.
	uiMu   sync.Mutex
	ui     uiState
	uiKick chan struct{}

	mStatus, mLast, mPause, mLogin, mPair, mLog, mQuit *systray.MenuItem
}

type uiState struct {
	note string // overrides the status line, e.g. "Pairing..."
	st   node.Status
}

func runTray(ctx context.Context, cfg *config.Config, dir, peerAddr string, verbose, firstRun bool) error {
	lock, err := acquireInstanceLock(dir)
	if errors.Is(err, errAlreadyRunning) {
		return errors.New("clipsync is already running — look for its icon in the menu bar or next to the clock")
	}
	if err != nil {
		return fmt.Errorf("cannot start: %w", err)
	}
	var once sync.Once
	release := func() { once.Do(lock) }
	defer release()
	if peerAddr != "" && cfg.Peer != nil {
		cfg.Peer.Addr = withDefaultPort(peerAddr)
		if err := cfg.Save(dir); err != nil {
			return err
		}
	}
	var extra []string
	if verbose {
		extra = append(extra, "-v")
	}
	handoff, first, err := prepareTray(cfg, dir, extra, release)
	if err != nil {
		return err
	}
	if handoff {
		return nil // the installed copy takes over
	}

	logger, logPath, closeLog, err := openLog(dir)
	if err != nil {
		return err
	}
	defer closeLog()
	cb, err := clip.NewSystem()
	if err != nil {
		return fmt.Errorf("cannot use the clipboard: %w", err)
	}
	actx, cancel := context.WithCancel(ctx)
	defer cancel()
	a := &trayApp{
		ctx: actx, cancel: cancel, cfg: cfg, dir: dir, verbose: verbose,
		firstRun: firstRun || first, log: logger, logPath: logPath, clip: cb,
		uiKick: make(chan struct{}, 1),
	}
	logger.Printf("clipsync %s starting on %s", version, cfg.Name)
	systray.Run(a.onReady, a.onExit)
	logger.Printf("clipsync stopped")
	return nil
}

// openLog appends to clipsync.log in the settings folder, starting over
// once it passes 1 MB.
func openLog(dir string) (*log.Logger, string, func(), error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, "", nil, err
	}
	path := filepath.Join(dir, "clipsync.log")
	if fi, err := os.Stat(path); err == nil && fi.Size() > 1<<20 {
		os.Rename(path, path+".old")
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, "", nil, err
	}
	return log.New(f, "", log.LstdFlags), path, func() { f.Close() }, nil
}

func (a *trayApp) onReady() {
	setTrayIcon(false)
	systray.SetTooltip("clipsync")
	a.mStatus = systray.AddMenuItem("Starting...", "")
	a.mStatus.Disable()
	a.mLast = systray.AddMenuItem("", "")
	a.mLast.Disable()
	a.mLast.Hide()
	systray.AddSeparator()
	a.mPause = systray.AddMenuItemCheckbox("Pause syncing", "Stop sending and receiving for now", false)
	a.mLogin = systray.AddMenuItemCheckbox("Start at login", "Open clipsync when you log in", autostartEnabled())
	a.mPair = systray.AddMenuItem("Pair with another computer...", "")
	a.mLog = systray.AddMenuItem("Open log", "")
	systray.AddSeparator()
	a.mQuit = systray.AddMenuItem("Quit clipsync", "")

	go a.uiLoop()
	go a.menuLoop()
	go func() {
		<-a.ctx.Done()
		systray.Quit()
	}()
	if a.firstRun {
		go firstRunHint()
	}
	go func() {
		a.mu.Lock()
		paired := a.cfg.Peer != nil
		a.mu.Unlock()
		if !paired {
			a.repair()
			return
		}
		a.start()
	}()
}

// onExit runs when the menu loop ends. It must not wait long: on macOS it
// runs on the main thread, which the menu needs.
func (a *trayApp) onExit() {
	a.cancel()
	done := make(chan struct{})
	go func() {
		a.stop()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
	}
}

func (a *trayApp) menuLoop() {
	for {
		select {
		case <-a.ctx.Done():
			return
		case <-a.mPause.ClickedCh:
			p := !a.mPause.Checked()
			if p {
				a.mPause.Check()
			} else {
				a.mPause.Uncheck()
			}
			a.setPaused(p)
		case <-a.mLogin.ClickedCh:
			on := !a.mLogin.Checked()
			if err := setAutostart(on); err != nil {
				go alert("clipsync", "Could not change start at login: "+err.Error())
				continue
			}
			if on {
				a.mLogin.Check()
			} else {
				a.mLogin.Uncheck()
			}
		case <-a.mPair.ClickedCh:
			go a.repair()
		case <-a.mLog.ClickedCh:
			openPath(a.logPath)
		case <-a.mQuit.ClickedCh:
			a.cancel() // also closes any pairing dialog
			return
		}
	}
}

// --- what the menu shows -------------------------------------------------

func (a *trayApp) updateUI(f func(*uiState)) {
	a.uiMu.Lock()
	f(&a.ui)
	a.uiMu.Unlock()
	select {
	case a.uiKick <- struct{}{}:
	default: // an update is already pending; it will read the latest state
	}
}

func (a *trayApp) uiLoop() {
	for {
		select {
		case <-a.ctx.Done():
			return
		case <-a.uiKick:
		}
		a.uiMu.Lock()
		s := a.ui
		a.uiMu.Unlock()

		var title string
		switch {
		case s.note != "":
			title = s.note
		case s.st.Paused:
			title = "Paused"
		case s.st.Connected:
			title = "Connected to " + s.st.Peer
		case s.st.Peer != "":
			title = "Waiting for " + s.st.Peer + "..."
		default:
			title = "Starting..."
		}
		a.mStatus.SetTitle(title)
		systray.SetTooltip("clipsync — " + title)
		setTrayIcon(s.note == "" && s.st.Connected && !s.st.Paused)
		if s.st.Last != "" {
			a.mLast.SetTitle(fmt.Sprintf("Last: %s (%s)", s.st.Last, s.st.LastAt.Format("15:04")))
			a.mLast.Show()
		}
	}
}

// --- the sync engine -------------------------------------------------------

func (a *trayApp) setPaused(p bool) {
	a.mu.Lock()
	a.paused = p
	n := a.node
	a.mu.Unlock()
	if n != nil {
		n.SetPaused(p) // reports through onStatus
	} else {
		a.updateUI(func(s *uiState) { s.st.Paused = p })
	}
	a.log.Printf("Syncing paused: %v", p)
}

// start runs the sync engine if paired and not already running.
func (a *trayApp) start() {
	a.mu.Lock()
	if a.node != nil || a.ctx.Err() != nil {
		a.mu.Unlock()
		return
	}
	if a.cfg.Peer == nil {
		a.mu.Unlock()
		a.updateUI(func(s *uiState) { s.note = "Not paired yet — choose Pair below" })
		return
	}
	paused := a.paused
	peer := a.cfg.Peer.Name
	a.updateUI(func(s *uiState) { *s = uiState{st: node.Status{Peer: peer, Paused: paused}} })
	nctx, cancel := context.WithCancel(a.ctx)
	var n *node.Node
	n = node.New(a.cfg, a.dir, a.clip, a.log, node.Options{
		Verbose:  a.verbose,
		Paused:   paused,
		OnStatus: func(st node.Status) { a.onStatus(n, st) },
	})
	if err := n.Start(nctx); err != nil {
		a.mu.Unlock()
		cancel()
		a.log.Printf("Could not start: %v", err)
		a.updateUI(func(s *uiState) { s.note = "Not running — see log" })
		go alert("clipsync could not start syncing", err.Error())
		return
	}
	a.node, a.stopNode = n, cancel
	a.mu.Unlock()
}

// stop shuts the sync engine down.
func (a *trayApp) stop() {
	a.mu.Lock()
	n, cancel := a.node, a.stopNode
	a.node, a.stopNode = nil, nil
	a.mu.Unlock()
	if n != nil {
		cancel()
		n.Wait()
	}
}

func (a *trayApp) onStatus(n *node.Node, st node.Status) {
	a.mu.Lock()
	current := a.node == n // start() holds a.mu until a.node is set
	a.mu.Unlock()
	if current {
		a.updateUI(func(s *uiState) { s.st = st })
	}
}

// repair stops syncing, pairs (dialogs on the Mac, a separate console window
// on Windows), and starts syncing again.
func (a *trayApp) repair() {
	a.mu.Lock()
	if a.pairing {
		a.mu.Unlock()
		return
	}
	a.pairing = true
	a.mu.Unlock()
	defer func() {
		a.mu.Lock()
		a.pairing = false
		a.mu.Unlock()
	}()

	a.stop()
	a.updateUI(func(s *uiState) { s.note = "Pairing..." })
	cfg, peer, err := trayPair(a)
	switch {
	case err == nil:
		a.log.Printf("Paired with %s", peer)
		a.mu.Lock()
		a.cfg = cfg
		a.mu.Unlock()
		notify("clipsync", "Paired with "+peer)
	case errors.Is(err, errCanceled) || a.ctx.Err() != nil:
	default:
		a.log.Printf("Pairing failed: %v", err)
		go alert("Pairing failed", err.Error())
	}
	a.updateUI(func(s *uiState) { s.note = "" })
	a.start()
}
