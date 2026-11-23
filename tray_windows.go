//go:build windows

package main

import (
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"unsafe"

	"fyne.io/systray"
	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"

	"clipsync/internal/config"
	"clipsync/internal/icons"
)

var winIcons = sync.OnceValue(func() [2][]byte {
	sizes := []int{16, 20, 24, 32, 48}
	return [2][]byte{icons.ICO(sizes, icons.WinTray(false)), icons.ICO(sizes, icons.WinTray(true))}
})

func setTrayIcon(connected bool) {
	i := 0
	if connected {
		i = 1
	}
	systray.SetIcon(winIcons()[i])
}

var procMessageBoxW = windows.NewLazySystemDLL("user32.dll").NewProc("MessageBoxW")

const (
	mbIconInformation = 0x40
	mbTopmost         = 0x40000
	mbSetForeground   = 0x10000
)

func alert(title, msg string) {
	t, _ := windows.UTF16PtrFromString(title)
	m, _ := windows.UTF16PtrFromString(msg)
	procMessageBoxW.Call(0, uintptr(unsafe.Pointer(m)), uintptr(unsafe.Pointer(t)), mbIconInformation|mbTopmost|mbSetForeground)
}

// notify has no quiet equivalent here; the tray menu shows the result.
func notify(title, msg string) {}

func firstRunHint() {
	alert("clipsync is running", "clipsync now runs in the background.\n\n"+
		"Its icon is near the clock at the bottom right (click ^ if you don't see it). Click it for the menu.\n\n"+
		"It will also start by itself when you log in.")
}

func openPath(p string) { exec.Command("notepad.exe", p).Start() }

// trayPair runs pairing in a separate clipsync process with its own console
// window, so closing that window or pressing Ctrl+C there cannot quit the
// tray. The new pairing is read back from the settings file.
func trayPair(a *trayApp) (*config.Config, string, error) {
	a.mu.Lock()
	before := ""
	if a.cfg.Peer != nil {
		before = a.cfg.Peer.ID + string(a.cfg.Peer.Key)
	}
	a.mu.Unlock()
	exe, err := os.Executable()
	if err != nil {
		return nil, "", err
	}
	cmd := exec.CommandContext(a.ctx, exe, "pair-window")
	cmd.Run() // its window shows any error itself
	cfg, err := config.Load(a.dir)
	if err != nil {
		return nil, "", err
	}
	if cfg.Peer == nil || cfg.Peer.ID+string(cfg.Peer.Key) == before {
		return nil, "", errCanceled
	}
	return cfg, cfg.Peer.Name, nil
}

// prepareTray sets up start at login the first time, and makes sure the
// copy that runs is the installed one in %LocalAppData%: Windows Firewall
// remembers "Allow" per program path, so running from Downloads now and from
// the installed copy at the next login would ask again (and, if missed,
// block the Mac). A copy started from anywhere else installs itself, starts
// the installed copy, and exits (handoff).
func prepareTray(cfg *config.Config, dir string) (handoff, first bool, err error) {
	first = !cfg.AutostartSet
	if first {
		cfg.AutostartSet = true
		if err := cfg.Save(dir); err != nil {
			return false, false, err
		}
	}
	if !first && !autostartEnabled() {
		return false, first, nil // the person turned start at login off
	}
	exe, err := os.Executable()
	if err != nil {
		return false, first, nil
	}
	dst := installedExe()
	if strings.EqualFold(filepath.Clean(exe), filepath.Clean(dst)) {
		setRunKey(dst)
		return false, first, nil
	}
	if err := copyFile(exe, dst); err != nil {
		return false, first, nil // e.g. the installed copy is running; the lock reports it
	}
	setRunKey(dst)
	args := []string{}
	if first {
		args = append(args, "-first-run")
	}
	if err := exec.Command(dst, args...).Start(); err != nil {
		return false, first, nil // run from here instead
	}
	return true, first, nil
}

// --- start at login (HKCU Run key, from a copy in %LocalAppData%) ----------

const (
	runKey   = `Software\Microsoft\Windows\CurrentVersion\Run`
	runValue = "clipsync"
)

func installedExe() string {
	return filepath.Join(os.Getenv("LOCALAPPDATA"), "Programs", "clipsync", "clipsync.exe")
}

func autostartEnabled() bool {
	k, err := registry.OpenKey(registry.CURRENT_USER, runKey, registry.QUERY_VALUE)
	if err != nil {
		return false
	}
	defer k.Close()
	v, _, err := k.GetStringValue(runValue)
	return err == nil && v != ""
}

// setAutostart registers the installed copy (installing this program there
// first if it runs from elsewhere) or removes the login entry.
func setAutostart(on bool) error {
	if !on {
		k, err := registry.OpenKey(registry.CURRENT_USER, runKey, registry.SET_VALUE)
		if err != nil {
			return nil
		}
		defer k.Close()
		if err := k.DeleteValue(runValue); err != nil && !errors.Is(err, registry.ErrNotExist) {
			return err
		}
		return nil
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	dst := installedExe()
	if !strings.EqualFold(filepath.Clean(exe), filepath.Clean(dst)) {
		if err := copyFile(exe, dst); err != nil {
			return err
		}
	}
	return setRunKey(dst)
}

func setRunKey(path string) error {
	k, _, err := registry.CreateKey(registry.CURRENT_USER, runKey, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()
	return k.SetStringValue(runValue, `"`+path+`"`)
}

func copyFile(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	tmp := dst + ".new"
	out, err := os.Create(tmp)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		os.Remove(tmp)
		return err
	}
	if err := out.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, dst)
}
