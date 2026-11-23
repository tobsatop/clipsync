package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"os"
	"path/filepath"
	"sync"

	"fyne.io/systray"

	"clipsync/internal/clip"
	"clipsync/internal/config"
	"clipsync/internal/node"
)

// trayApp is clipsync as a menu-bar (Mac) or notification-area (Windows)
// icon: no window to keep open, a small menu, and a log file.
type trayApp struct {
	ctx     context.Context
	cfg     *config.Config
	dir     string
	verbose bool
	log     *log.Logger
	logPath string
	clip    clip.Clipboard

	mu       sync.Mutex
	node     *node.Node
	stopNode context.CancelFunc
	paused   bool
	pairing  bool

	mStatus, mLast, mPause, mLogin, mPair, mLog, mQuit *systray.MenuItem
}

func runTray(ctx context.Context, cfg *config.Config, dir string, verbose bool) error {
	// One clipsync per computer: the sync port doubles as the lock.
	ln, err := net.Listen("tcp", fmt.Sprintf(":%d", cfg.Port))
	if err != nil {
		return fmt.Errorf("clipsync is already running (look for its icon), or another app uses port %d", cfg.Port)
	}
	ln.Close()

	logger, logPath, closeLog, err := openLog(dir)
	if err != nil {
		return err
	}
	defer closeLog()
	cb, err := clip.NewSystem()
	if err != nil {
		return fmt.Errorf("cannot use the clipboard: %w", err)
	}
	a := &trayApp{ctx: ctx, cfg: cfg, dir: dir, verbose: verbose, log: logger, logPath: logPath, clip: cb}
	logger.Printf("clipsync %s starting on %s", version, cfg.Name)
	systray.Run(a.onReady, a.stop)
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

	a.setupAutostart()
	go a.menuLoop()
	go func() {
		<-a.ctx.Done()
		systray.Quit()
	}()
	go func() {
		if a.cfg.Peer == nil {
			a.mStatus.SetTitle("Not paired yet")
			a.repair()
			return
		}
		a.start()
	}()
}

// setupAutostart turns start-at-login on the first time clipsync runs as an
// app, and afterwards keeps the login item pointing at this copy.
func (a *trayApp) setupAutostart() {
	if !a.cfg.AutostartSet {
		a.cfg.AutostartSet = true
		if err := setAutostart(true); err != nil {
			a.log.Printf("Could not set up start at login: %v", err)
		} else {
			a.mLogin.Check()
		}
		if err := a.cfg.Save(a.dir); err != nil {
			a.log.Printf("Could not save settings: %v", err)
		}
		go firstRunHint()
		return
	}
	if autostartEnabled() {
		if err := setAutostart(true); err != nil {
			a.log.Printf("Could not update start at login: %v", err)
		}
	}
}

func (a *trayApp) menuLoop() {
	for {
		select {
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
			systray.Quit()
			return
		}
	}
}

func (a *trayApp) setPaused(p bool) {
	a.mu.Lock()
	a.paused = p
	n := a.node
	a.mu.Unlock()
	if n != nil {
		n.SetPaused(p)
	} else {
		a.showIdle()
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
		a.mStatus.SetTitle("Not paired yet — choose Pair below")
		return
	}
	nctx, cancel := context.WithCancel(a.ctx)
	var n *node.Node
	n = node.New(a.cfg, a.dir, a.clip, a.log, node.Options{
		Verbose:  a.verbose,
		OnStatus: func(st node.Status) { a.onStatus(n, st) },
	})
	if err := n.Start(nctx); err != nil {
		a.mu.Unlock()
		cancel()
		a.log.Printf("Could not start: %v", err)
		a.mStatus.SetTitle("Not running — see log")
		go alert("clipsync could not start syncing", err.Error())
		return
	}
	a.node, a.stopNode = n, cancel
	paused := a.paused
	peer := a.cfg.Peer.Name
	a.mu.Unlock() // OnStatus takes a.mu, so nothing below may hold it

	a.mStatus.SetTitle("Waiting for " + peer + "...")
	systray.SetTooltip("clipsync — waiting for " + peer)
	if paused {
		n.SetPaused(true)
	}
}

// stop shuts the sync engine down (also called when the app quits).
func (a *trayApp) stop() {
	a.mu.Lock()
	n, cancel := a.node, a.stopNode
	a.node, a.stopNode = nil, nil
	a.mu.Unlock()
	if n != nil {
		cancel()
		n.Wait()
	}
	setTrayIcon(false)
}

func (a *trayApp) onStatus(n *node.Node, st node.Status) {
	a.mu.Lock()
	current := a.node == n
	a.mu.Unlock()
	if !current {
		return // a stopped engine reporting late
	}
	var title string
	switch {
	case st.Paused:
		title = "Paused"
	case st.Connected:
		title = "Connected to " + st.Peer
	default:
		title = "Waiting for " + st.Peer + "..."
	}
	a.mStatus.SetTitle(title)
	systray.SetTooltip("clipsync — " + title)
	setTrayIcon(st.Connected && !st.Paused)
	if st.Last != "" {
		a.mLast.SetTitle(fmt.Sprintf("Last: %s (%s)", st.Last, st.LastAt.Format("15:04")))
		a.mLast.Show()
	}
}

func (a *trayApp) showIdle() {
	a.mu.Lock()
	paused := a.paused
	a.mu.Unlock()
	if paused {
		a.mStatus.SetTitle("Paused")
	}
}

// repair stops syncing, pairs (with dialogs on the Mac, a console window on
// Windows), and starts syncing again.
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
	a.mStatus.SetTitle("Pairing...")
	ui, done, err := newTrayPairUI(a.log)
	if err != nil {
		go alert("clipsync", "Cannot show the pairing window: "+err.Error())
	} else {
		res, err := pairInteractive(a.ctx, a.cfg, a.dir, "", ui)
		done()
		switch {
		case err == nil:
			a.log.Printf("Paired with %s", res.PeerName)
			notify("clipsync", "Paired with "+res.PeerName)
		case errors.Is(err, errCanceled) || a.ctx.Err() != nil:
		default:
			a.log.Printf("Pairing failed: %v", err)
			go alert("Pairing failed", err.Error())
		}
	}
	a.start()
}
