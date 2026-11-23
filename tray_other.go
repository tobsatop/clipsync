//go:build !darwin && !windows

package main

import (
	"fmt"
	"log"
	"os"
	"os/exec"

	"fyne.io/systray"

	"clipsync/internal/icons"
)

func setTrayIcon(connected bool) {
	systray.SetIcon(icons.PNG(icons.Render(32, icons.WinTray(connected))))
}

func alert(title, msg string)  { fmt.Fprintf(os.Stderr, "%s: %s\n", title, msg) }
func notify(title, msg string) {}
func firstRunHint()            {}
func openPath(p string)        { exec.Command("xdg-open", p).Start() }

func newTrayPairUI(*log.Logger) (pairUI, func(), error) { return consoleUI{}, func() {}, nil }

func autostartEnabled() bool  { return false }
func setAutostart(bool) error { return nil }
