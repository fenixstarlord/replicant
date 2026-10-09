#!/usr/bin/env bash
# Build dist/Shelf.app: the SwiftUI menu bar app with the Go `shelf` CLI
# bundled inside it. Needs the Swift toolchain (Command Line Tools) and Go.
set -euo pipefail
cd "$(dirname "$0")/.."
VERSION="$(git describe --tags --always --dirty 2>/dev/null || echo dev)"
# --standalone also bundles shelf-server: one app that runs its own
# catalog on this Mac (no server, no API keys).
STANDALONE=0
if [[ "${1:-}" == "--standalone" ]]; then STANDALONE=1; fi
APP=dist/Shelf.app
NAME=Shelf
BUNDLE_ID=com.fenixstarlord.shelf.menubar
if [[ $STANDALONE == 1 ]]; then APP="dist/Shelf Standalone.app"; NAME="Shelf Standalone"; BUNDLE_ID=com.fenixstarlord.shelf.standalone; fi
universal() { # universal <cmd> <out>
  CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -ldflags "-s -w -X main.version=$VERSION" -o "$2-arm64" "$1"
  CGO_ENABLED=0 GOOS=darwin GOARCH=amd64 go build -ldflags "-s -w -X main.version=$VERSION" -o "$2-amd64" "$1"
  lipo -create -output "$2" "$2-arm64" "$2-amd64"
  rm -f "$2-arm64" "$2-amd64"
}
echo "building shelf CLI (universal)"
universal ./cmd/shelf dist/shelf
if [[ $STANDALONE == 1 ]]; then
  echo "building shelf-server (universal)"
  universal ./cmd/shelf-server dist/shelf-server
fi
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
EXTRA_PLIST=""
if [[ $STANDALONE == 1 ]]; then
  cp dist/shelf-server "$APP/Contents/MacOS/shelf-server"
  EXTRA_PLIST="  <key>ShelfStandalone</key><true/>"
fi
cat > "$APP/Contents/Info.plist" <<PLIST
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
  <key>CFBundleName</key><string>${NAME}</string>
  <key>CFBundleDisplayName</key><string>${NAME}</string>
  <key>CFBundleIdentifier</key><string>${BUNDLE_ID}</string>
${EXTRA_PLIST}
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
