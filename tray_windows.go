//go:build windows

package main

import (
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"unsafe"

	"fyne.io/systray"
	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"

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

// newTrayPairUI opens a console window for pairing, the same screens as
// the console version.
func newTrayPairUI(*log.Logger) (pairUI, func(), error) {
	closeConsole, err := openConsole()
	if err != nil {
		return nil, nil, err
	}
	fmt.Println("clipsync — pairing (closing this window quits clipsync)")
	fmt.Println()
	return consoleUI{}, func() {
		fmt.Print("\nPress Enter to close this window...")
		stdin.ReadString('\n')
		closeConsole()
	}, nil
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

// setAutostart copies this program to %LocalAppData%\Programs\clipsync (so
// deleting the download does not break it) and registers that copy. Running
// a newer download refreshes the copy.
func setAutostart(on bool) error {
	k, _, err := registry.CreateKey(registry.CURRENT_USER, runKey, registry.SET_VALUE|registry.QUERY_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()
	if !on {
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
	return k.SetStringValue(runValue, `"`+dst+`"`)
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
