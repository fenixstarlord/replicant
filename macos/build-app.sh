#!/usr/bin/env bash
# Build dist/Replicant.app: the SwiftUI menu bar app with the Go `replicant` CLI
# bundled inside it. Needs the Swift toolchain (Command Line Tools) and Go.
set -euo pipefail
cd "$(dirname "$0")/.."
VERSION="$(git describe --tags --always --dirty 2>/dev/null || echo dev)"
# --standalone also bundles replicant-server: one app that runs its own
# catalog on this Mac (no server, no API keys).
STANDALONE=0
if [[ "${1:-}" == "--standalone" ]]; then STANDALONE=1; fi
APP="dist/Replicant Client.app"
NAME="Replicant Client"
BUNDLE_ID=com.fenixstarlord.replicant.menubar
if [[ $STANDALONE == 1 ]]; then APP="dist/Replicant Standalone.app"; NAME="Replicant Standalone"; BUNDLE_ID=com.fenixstarlord.replicant.standalone; fi
universal() { # universal <cmd> <out>
  CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -ldflags "-s -w -X main.version=$VERSION" -o "$2-arm64" "$1"
  CGO_ENABLED=0 GOOS=darwin GOARCH=amd64 go build -ldflags "-s -w -X main.version=$VERSION" -o "$2-amd64" "$1"
  lipo -create -output "$2" "$2-arm64" "$2-amd64"
  rm -f "$2-arm64" "$2-amd64"
}
echo "building replicant CLI (universal)"
universal ./cmd/replicant dist/replicant
if [[ $STANDALONE == 1 ]]; then
  echo "building replicant-server (universal)"
  universal ./cmd/replicant-server dist/replicant-server
fi
# A universal (arm64 + x86_64) Swift build needs Xcode's build service;
# with only the Command Line Tools, build for this Mac's architecture.
ARCHS=()
if xcodebuild -version >/dev/null 2>&1; then ARCHS=(--arch arm64 --arch x86_64); fi
echo "building menu bar app (${ARCHS[*]:-native})"
(cd macos/ReplicantMenu && swift build -c release ${ARCHS[@]+"${ARCHS[@]}"} 2>&1 | grep -E 'error|Build complete' || true)
BIN=$(cd macos/ReplicantMenu && swift build -c release ${ARCHS[@]+"${ARCHS[@]}"} --show-bin-path)
rm -rf "$APP"
mkdir -p "$APP/Contents/MacOS" "$APP/Contents/Resources"
# App icon: macos/icon/AppIcon.png (1024 px, transparent outside the tile)
# if present, else drawn by macos/icon/make-icon.swift.
ICONSET=dist/AppIcon.iconset
if [[ ! -f dist/AppIcon.icns ]]; then
  rm -rf "$ICONSET" && mkdir -p "$ICONSET"
  if [[ -f macos/icon/AppIcon.png ]]; then
    for n in 16 32 128 256 512; do
      sips -z $n $n macos/icon/AppIcon.png --out "$ICONSET/icon_${n}x${n}.png" >/dev/null
      sips -z $((n*2)) $((n*2)) macos/icon/AppIcon.png --out "$ICONSET/icon_${n}x${n}@2x.png" >/dev/null
    done
  else
    swift macos/icon/make-icon.swift "$ICONSET" 2>&1 | grep -v warning || true
    rm -f "$ICONSET/preview.png"
  fi
  iconutil -c icns "$ICONSET" -o dist/AppIcon.icns
fi
cp dist/AppIcon.icns "$APP/Contents/Resources/AppIcon.icns"
cp "$BIN/ReplicantMenu" "$APP/Contents/MacOS/ReplicantMenu"
cp dist/replicant "$APP/Contents/MacOS/replicant"
EXTRA_PLIST=""
UI_ELEMENT=true   # menu bar only
if [[ $STANDALONE == 1 ]]; then
  UI_ELEMENT=false  # Dock icon and a main window
  cp dist/replicant-server "$APP/Contents/MacOS/replicant-server"
  EXTRA_PLIST="  <key>ReplicantStandalone</key><true/>"
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
  <key>CFBundleExecutable</key><string>ReplicantMenu</string>
  <key>CFBundleIconFile</key><string>AppIcon</string>
  <key>LSMinimumSystemVersion</key><string>14.0</string>
  <key>LSUIElement</key><${UI_ELEMENT}/>
  <key>NSHighResolutionCapable</key><true/>
  <key>NSHumanReadableCopyright</key><string>Replicant</string>
</dict></plist>
PLIST
echo "APPL????" > "$APP/Contents/PkgInfo"
codesign --force --deep --sign - "$APP" 2>/dev/null || echo "codesign (ad hoc) skipped"
echo "built $APP ($VERSION)"
