#!/usr/bin/env bash
# Build dist/Shelf.app: the SwiftUI menu bar app with the Go `shelf` CLI
# bundled inside it. Needs the Swift toolchain (Command Line Tools) and Go.
set -euo pipefail
cd "$(dirname "$0")/.."
VERSION="$(git describe --tags --always --dirty 2>/dev/null || echo dev)"
APP=dist/Shelf.app
echo "building shelf CLI (universal)"
CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -ldflags "-s -w -X main.version=$VERSION" -o dist/shelf-arm64 ./cmd/shelf
CGO_ENABLED=0 GOOS=darwin GOARCH=amd64 go build -ldflags "-s -w -X main.version=$VERSION" -o dist/shelf-amd64 ./cmd/shelf
lipo -create -output dist/shelf dist/shelf-arm64 dist/shelf-amd64
rm -f dist/shelf-arm64 dist/shelf-amd64
# A universal (arm64 + x86_64) Swift build needs Xcode's build service;
# with only the Command Line Tools, build for this Mac's architecture.
ARCHS=()
if xcodebuild -version >/dev/null 2>&1; then ARCHS=(--arch arm64 --arch x86_64); fi
echo "building menu bar app (${ARCHS[*]:-native})"
(cd macos/ShelfMenu && swift build -c release ${ARCHS[@]+"${ARCHS[@]}"} 2>&1 | grep -E 'error|Build complete' || true)
BIN=$(cd macos/ShelfMenu && swift build -c release ${ARCHS[@]+"${ARCHS[@]}"} --show-bin-path)
rm -rf "$APP"
mkdir -p "$APP/Contents/MacOS" "$APP/Contents/Resources"
cp "$BIN/ShelfMenu" "$APP/Contents/MacOS/ShelfMenu"
cp dist/shelf "$APP/Contents/MacOS/shelf"
cat > "$APP/Contents/Info.plist" <<PLIST
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
  <key>CFBundleName</key><string>Shelf</string>
  <key>CFBundleDisplayName</key><string>Shelf</string>
  <key>CFBundleIdentifier</key><string>com.fenixstarlord.shelf.menubar</string>
  <key>CFBundleVersion</key><string>${VERSION}</string>
  <key>CFBundleShortVersionString</key><string>${VERSION}</string>
  <key>CFBundlePackageType</key><string>APPL</string>
  <key>CFBundleExecutable</key><string>ShelfMenu</string>
  <key>LSMinimumSystemVersion</key><string>14.0</string>
  <key>LSUIElement</key><true/>
  <key>NSHighResolutionCapable</key><true/>
  <key>NSHumanReadableCopyright</key><string>Shelf</string>
</dict></plist>
PLIST
echo "APPL????" > "$APP/Contents/PkgInfo"
codesign --force --deep --sign - "$APP" 2>/dev/null || echo "codesign (ad hoc) skipped"
echo "built $APP ($VERSION)"
