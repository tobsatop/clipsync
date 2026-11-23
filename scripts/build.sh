#!/bin/sh
# Builds dist/clipsync.app (Mac, Apple silicon) and the Windows programs.
set -eu
cd "$(dirname "$0")/.."
VERSION=$(sed -n 's/^const version = "\(.*\)"/\1/p' main.go)
APP=dist/clipsync.app
rm -rf "$APP" build
mkdir -p "$APP/Contents/MacOS" "$APP/Contents/Resources" build

# App icon: Windows resources (linked into the .exe) and a macOS .icns.
go run ./tools/genicons -syso . -iconset build/clipsync.iconset
iconutil -c icns build/clipsync.iconset -o "$APP/Contents/Resources/clipsync.icns"

GOOS=darwin GOARCH=arm64 CGO_ENABLED=1 go build -trimpath -ldflags="-s -w" -o "$APP/Contents/MacOS/clipsync" .
cat > "$APP/Contents/Info.plist" <<PLIST
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>CFBundleIdentifier</key><string>io.github.tobsatop.clipsync</string>
	<key>CFBundleName</key><string>clipsync</string>
	<key>CFBundleDisplayName</key><string>clipsync</string>
	<key>CFBundleExecutable</key><string>clipsync</string>
	<key>CFBundleIconFile</key><string>clipsync</string>
	<key>CFBundlePackageType</key><string>APPL</string>
	<key>CFBundleShortVersionString</key><string>$VERSION</string>
	<key>CFBundleVersion</key><string>$VERSION</string>
	<key>LSMinimumSystemVersion</key><string>12.0</string>
	<key>LSUIElement</key><true/>
	<key>NSHighResolutionCapable</key><true/>
	<key>NSLocalNetworkUsageDescription</key><string>clipsync connects to your other computer on this Wi-Fi to share the clipboard.</string>
</dict>
</plist>
PLIST
codesign --force --deep --sign - "$APP"
cp "$APP/Contents/MacOS/clipsync" dist/clipsync-mac.tmp && mv dist/clipsync-mac.tmp dist/clipsync-mac # same program, for Terminal (new file, so a running copy is untouched)

for arch in amd64 arm64; do
	GOOS=windows GOARCH=$arch CGO_ENABLED=0 go build -trimpath -ldflags="-s -w -H=windowsgui" -o "dist/clipsync-windows-$arch.exe" .
done
echo "built clipsync $VERSION:"
ls -la dist
