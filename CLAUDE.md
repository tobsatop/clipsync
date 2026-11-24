# CLAUDE.md

clipsync keeps the clipboard (text and images) of a Windows PC and a Mac in sync over the same
Wi-Fi. One Go program per platform, running as a menu-bar / notification-area app.
User docs: README.md (Thai) and CHANGELOG.md (Thai).

## Commands

```
go test -race ./...                                  # unit + loopback tests (in-memory clipboards)
GOOS=windows GOARCH=amd64 go vet ./...               # vet the Windows-only files from macOS/Linux
./scripts/build.sh                                   # dist/clipsync.app + dist/clipsync-windows-{amd64,arm64}.exe
CLIPSYNC_REAL_CLIPBOARD=1 go test -run TestRealMac ./internal/node/   # real Mac pasteboard, see Gotchas
```

## Layout

- `main.go` (commands, console mode), `pairing.go` (pairing UI: console or dialogs), `tray*.go`
  (menu; start at login: a LaunchAgent on macOS, HKCU Run plus a copy in %LocalAppData% on Windows),
  `lock_*.go` (one instance per user), `console_windows.go` (the Windows build is a GUI program;
  commands attach or allocate a console; tray pairing runs `clipsync pair-window` in its own console).
- `internal/wire`: CPace pairing (`filippo.io/cpace`), X25519 + AES-256-GCM sessions, protocol version.
- `internal/node`: clipboard polling, send/receive, echo prevention (hash + ignoreSeq), reconnect,
  pause and status. `internal/clip`: platform clipboards (golang.design/x/clipboard plus an own
  Windows DIB reader).
- `internal/lan`: UDP broadcast discovery. `internal/icons` and `tools/genicons`: icons drawn in code
  and a small COFF writer for the .exe icon. `docs/`: README icon and SVG illustrations.

## Releasing

1. Bump `version` in `main.go` (protocol changes also bump `protoVersion` in `internal/wire`), add a
   CHANGELOG.md entry in Thai, and update the README if the steps changed.
2. Commit, push `main`, then push a tag `vX.Y.Z`. `.github/workflows/release.yml` (macOS runner)
   checks the tag, tests, runs `scripts/build.sh` and publishes the Release with the three files.

## Gotchas

- Real-clipboard tests overwrite the developer's clipboard. They snapshot and restore every
  pasteboard type; still, warn whoever is at the machine before running them.
- Programs started by an agent's shell may be denied outbound LAN access by macOS Local Network
  privacy; start the app with `open` (or from Terminal) when testing discovery.
- Rebuilding replaces the ad-hoc signature, so macOS may ask for Local Network permission again.
- Unsigned builds: macOS Gatekeeper and Windows SmartScreen warn on first run; the README covers both.

## Open ideas

- Version details in the Windows .exe (the sibling project discord-activity's `tools/genicons` does this).
- Code signing, to remove the first-run warnings.
- The Windows ARM64 build has not run on real hardware yet.
- Copying at the same moment on both machines can swap the items (no tie-break yet).
