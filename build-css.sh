#!/usr/bin/env bash
# Build internal/web/static/app.css from src/app.css with the standalone
# Tailwind CSS CLI and daisyUI. No Node at runtime; this is a one-time
# build step whose output is committed. Pinned versions below.
set -euo pipefail
cd "$(dirname "$0")"
TAILWIND_VERSION="${TAILWIND_VERSION:-v4.1.14}"
DAISYUI_VERSION="${DAISYUI_VERSION:-5.1.29}"
TOOLS=.tools; mkdir -p "$TOOLS"
case "$(uname -s)-$(uname -m)" in
  Darwin-arm64) BIN=tailwindcss-macos-arm64 ;;
  Darwin-x86_64) BIN=tailwindcss-macos-x64 ;;
  Linux-x86_64) BIN=tailwindcss-linux-x64 ;;
  Linux-aarch64) BIN=tailwindcss-linux-arm64 ;;
  *) echo "unsupported platform"; exit 1 ;;
esac
if [ ! -x "$TOOLS/tailwindcss" ]; then
  echo "downloading tailwindcss $TAILWIND_VERSION ($BIN)"
  curl -sSL -o "$TOOLS/tailwindcss" "https://github.com/tailwindlabs/tailwindcss/releases/download/$TAILWIND_VERSION/$BIN"
  chmod +x "$TOOLS/tailwindcss"
fi
if [ ! -f "$TOOLS/daisyui.js" ]; then
  echo "downloading daisyui $DAISYUI_VERSION"
  curl -sSL -o "$TOOLS/daisyui.js" "https://github.com/saadeghi/daisyui/releases/download/v$DAISYUI_VERSION/daisyui.js"
  curl -sSL -o "$TOOLS/daisyui-theme.js" "https://github.com/saadeghi/daisyui/releases/download/v$DAISYUI_VERSION/daisyui-theme.js"
fi
# The standalone CLI resolves @plugin "daisyui" relative to the CSS file;
# point it at the downloaded bundle via a sibling copy.
cp "$TOOLS/daisyui.js" internal/web/static/src/daisyui.js
cp "$TOOLS/daisyui-theme.js" internal/web/static/src/daisyui-theme.js
sed -e 's#@plugin "daisyui" {#@plugin "./daisyui.js" {#' -e 's#@plugin "daisyui/theme" {#@plugin "./daisyui-theme.js" {#' \
  internal/web/static/src/app.css > internal/web/static/src/.app.build.css
"$TOOLS/tailwindcss" -i internal/web/static/src/.app.build.css -o internal/web/static/app.css --minify \
  --content 'internal/web/templates/**/*.html'
rm -f internal/web/static/src/.app.build.css
echo "built internal/web/static/app.css ($(wc -c < internal/web/static/app.css) bytes)"
