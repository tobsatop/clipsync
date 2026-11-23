//go:build !darwin && !windows

package main

import (
	"fmt"
	"os"
	"os/exec"

	"fyne.io/systray"

	"clipsync/internal/config"
	"clipsync/internal/icons"
)

func setTrayIcon(connected bool) {
	systray.SetIcon(icons.PNG(icons.Render(32, icons.WinTray(connected))))
}

func alert(title, msg string)  { fmt.Fprintf(os.Stderr, "%s: %s\n", title, msg) }
func notify(title, msg string) {}
func firstRunHint()            {}
func openPath(p string)        { exec.Command("xdg-open", p).Start() }

func prepareTray(*config.Config, string) (bool, bool, error) { return false, false, nil }

func trayPair(a *trayApp) (*config.Config, string, error) {
	res, err := pairInteractive(a.ctx, a.cfg, a.dir, "", consoleUI{})
	if err != nil {
		return nil, "", err
	}
	return a.cfg, res.PeerName, nil
}

func autostartEnabled() bool  { return false }
func setAutostart(bool) error { return nil }
