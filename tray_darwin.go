//go:build darwin

package main

import (
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	"fyne.io/systray"

	"clipsync/internal/config"
	"clipsync/internal/icons"
)

// The menu bar must be driven from the main thread.
func init() { runtime.LockOSThread() }

var macIcons = sync.OnceValue(func() [2][]byte {
	return [2][]byte{icons.PNG(icons.Render(64, icons.MacTray(false))), icons.PNG(icons.Render(64, icons.MacTray(true)))}
})

func setTrayIcon(connected bool) {
	i := 0
	if connected {
		i = 1
	}
	icon := macIcons()[i]
	systray.SetTemplateIcon(icon, icon)
}

// --- dialogs (AppleScript, shown by the system's osascript) ----------------

func asQuote(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	return `"` + s + `"`
}

func osa(ctx context.Context, script string) (string, error) {
	out, err := exec.CommandContext(ctx, "osascript", "-e", script).Output()
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) && strings.Contains(string(ee.Stderr), "-128") {
			return "", errCanceled // the Cancel button
		}
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		return "", fmt.Errorf("dialog failed: %v", err)
	}
	return strings.TrimSpace(string(out)), nil
}

func alert(title, msg string) {
	osa(context.Background(), "display alert "+asQuote(title)+" message "+asQuote(msg))
}

func notify(title, msg string) {
	osa(context.Background(), "display notification "+asQuote(msg)+" with title "+asQuote(title))
}

func firstRunHint() {
	notify("clipsync is running", "Its icon is in the menu bar at the top of the screen.")
}

func openPath(p string) { exec.Command("open", "-t", p).Start() }

// prepareTray turns start-at-login on the first time clipsync runs as an
// app, and afterwards keeps the login item pointing at this copy.
func prepareTray(cfg *config.Config, dir string, _ []string, _ func()) (handoff, first bool, err error) {
	first = !cfg.AutostartSet
	if first {
		cfg.AutostartSet = true
		if err := cfg.Save(dir); err != nil {
			return false, false, err
		}
	}
	if (first || autostartEnabled()) && inAppBundle() {
		setAutostart(true) // best effort; the menu shows the result
	}
	return false, first, nil
}

// inAppBundle reports whether this is clipsync.app at a stable place, the
// only kind of copy worth starting at login (a bare binary has no Local
// Network permission text; a translocated app lives in a temporary path).
func inAppBundle() bool {
	exe, err := os.Executable()
	if err != nil {
		return false
	}
	return strings.Contains(exe, ".app/Contents/MacOS/") && !strings.Contains(exe, "/AppTranslocation/")
}

// trayPair pairs with dialog boxes.
func trayPair(a *trayApp) (*config.Config, string, error) {
	a.mu.Lock()
	cfg := a.cfg
	a.mu.Unlock()
	res, err := pairInteractive(a.ctx, cfg, a.dir, "", dialogUI{ctx: a.ctx, log: a.log})
	if err != nil {
		return nil, "", err
	}
	return cfg, res.PeerName, nil
}

type dialogUI struct {
	ctx context.Context
	log *log.Logger
}

func (d dialogUI) Choose(ctx context.Context, thisName string) (bool, error) {
	msg := "Pair this Mac (" + thisName + ") with your other computer. You only need to do this once.\n\n" +
		"Show a code here and type it on the other computer, or the other way round."
	res, err := osa(ctx, `button returned of (display dialog `+asQuote(msg)+
		` buttons {"Cancel", "Type a code", "Show a code"} default button "Show a code" cancel button "Cancel" with title "clipsync")`)
	if err != nil {
		return false, err
	}
	return res == "Type a code", nil
}

func (d dialogUI) AskCode(ctx context.Context, note string) (string, error) {
	msg := "Type the 6-digit code shown on the other computer:"
	if note != "" {
		msg = note + "\n\n" + msg
	}
	return osa(ctx, `text returned of (display dialog `+asQuote(msg)+
		` default answer "" buttons {"Cancel", "Pair"} default button "Pair" cancel button "Cancel" with title "clipsync")`)
}

func (d dialogUI) ShowCode(code string, addrs []string) (<-chan struct{}, func()) {
	msg := "Pairing code:  " + code[:3] + " " + code[3:] + "\n\n" +
		"On the other computer, choose to type a code and enter this one."
	if len(addrs) > 0 {
		msg += "\n\n(This Mac's address: " + strings.Join(addrs, ", ") + ")"
	}
	cmd := exec.CommandContext(d.ctx, "osascript", "-e", `display dialog `+asQuote(msg)+
		` buttons {"Cancel"} default button "Cancel" cancel button "Cancel" with title "clipsync" giving up after 600`)
	cancelled := make(chan struct{})
	var closing sync.Once
	closedByUs := make(chan struct{})
	if err := cmd.Start(); err != nil {
		d.log.Printf("Could not show the pairing code: %v", err)
		return cancelled, func() {}
	}
	go func() {
		cmd.Wait()
		select {
		case <-closedByUs:
		default:
			close(cancelled) // the person pressed Cancel (or it timed out)
		}
	}()
	return cancelled, func() {
		closing.Do(func() {
			close(closedByUs)
			cmd.Process.Kill()
		})
	}
}

func (d dialogUI) Info(msg string) { d.log.Print(msg) }

// --- start at login (a LaunchAgent) ----------------------------------------

const agentLabel = "io.github.tobsatop.clipsync"

func agentPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "Library", "LaunchAgents", agentLabel+".plist")
}

func autostartEnabled() bool {
	_, err := os.Stat(agentPath())
	return err == nil
}

func setAutostart(on bool) error {
	p := agentPath()
	if !on {
		if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return nil
	}
	if !inAppBundle() {
		return errors.New("start at login works when clipsync runs as clipsync.app from the Applications folder")
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	var esc strings.Builder
	xml.EscapeText(&esc, []byte(exe))
	content := `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>` + agentLabel + `</string>
	<key>ProgramArguments</key>
	<array>
		<string>` + esc.String() + `</string>
	</array>
	<key>RunAtLoad</key>
	<true/>
	<key>ProcessType</key>
	<string>Interactive</string>
	<key>LimitLoadToSessionType</key>
	<string>Aqua</string>
</dict>
</plist>
`
	if old, err := os.ReadFile(p); err == nil && string(old) == content {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	return os.WriteFile(p, []byte(content), 0o644)
}
