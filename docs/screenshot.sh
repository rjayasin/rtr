#!/usr/bin/env bash
# Renders docs/screenshot.png, the README screenshot.
#
# The frame is rtr's real View() output, built from fabricated hosts, files and
# transfers (see TestScreenshot in internal/ui/screenshot_test.go), so nothing
# here touches SSH or the local filesystem. freeze draws that ANSI as a terminal
# window (SVG); headless Chrome composites it onto a gradient and rasterizes it
# at 2x. Requires Go and Google Chrome (or set CHROME=/path/to/chrome).
set -euo pipefail

root=$(cd "$(dirname "$0")/.." && pwd)
out=$root/docs/screenshot.png
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

chrome=${CHROME:-}
if [[ -z $chrome ]]; then
	for c in "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome" google-chrome chromium chromium-browser; do
		if command -v "$c" >/dev/null 2>&1 || [[ -x $c ]]; then
			chrome=$c
			break
		fi
	done
fi
[[ -n $chrome ]] || { echo "screenshot: Chrome not found (set CHROME)" >&2; exit 1; }

# freeze reads stdin whenever it isn't a TTY, even when given a file, so it gets
# /dev/null below; otherwise it hangs under a non-interactive parent.

# DejaVu Sans Mono covers every glyph rtr draws (➤ ✓ ✗, box drawing, the
# eighth-block progress bar), unlike freeze's bundled JetBrains Mono.
curl -fsSL -o "$work/font.ttf" \
	https://cdn.jsdelivr.net/npm/dejavu-fonts-ttf@2.37.3/ttf/DejaVuSansMono.ttf

(cd "$root" && RTR_SCREENSHOT="$work/frame.ansi" go test ./internal/ui -run '^TestScreenshot$' -count=1 >/dev/null)

(cd "$root" && go run github.com/charmbracelet/freeze@v0.2.2 \
	--language ansi --window --margin 0 --padding 20,24 \
	--background "#16161e" --border.radius 12 --border.width 1 --border.color "#3b3d57" \
	--font.file "$work/font.ttf" --font.family "DejaVu Sans Mono" \
	--font.size 14 --line-height 1.17 \
	-o "$work/window.svg" "$work/frame.ansi" </dev/null >/dev/null)

# freeze places background cells (the badge, the input cursor) on a grid of
# 1/1.68 em per column, but DejaVu Sans Mono advances 1233/2048 em per glyph, so
# the text drifts right of its backgrounds; tighten it onto freeze's grid.
sed -i.bak 's#<style>#<style>text { letter-spacing: -0.0068em; }#' "$work/window.svg"

# The page is sized to the window plus a margin of gradient on every side.
margin=56
read -r w h < <(sed -n 's/.*<svg width="\([0-9.]*\)" height="\([0-9.]*\)".*/\1 \2/p' "$work/window.svg" | head -1)
pw=$(printf '%.0f' "$(echo "$w + 2 * $margin" | bc)")
ph=$(printf '%.0f' "$(echo "$h + 2 * $margin" | bc)")

cat >"$work/page.html" <<EOF
<!doctype html>
<meta charset="utf-8">
<style>
  html, body { margin: 0; width: ${pw}px; height: ${ph}px; overflow: hidden; }
  body {
    display: grid; place-items: center;
    background:
      radial-gradient(ellipse at 12% 18%, #7c5cff 0, transparent 55%),
      radial-gradient(ellipse at 88% 12%, #ee6ff8 0, transparent 50%),
      radial-gradient(ellipse at 85% 92%, #3ec5ff 0, transparent 55%),
      radial-gradient(ellipse at 8% 95%, #5a56e0 0, transparent 50%),
      linear-gradient(135deg, #2a1459, #182a6e);
  }
  img { display: block; filter: drop-shadow(0 18px 36px rgba(8, 6, 30, 0.55)); }
</style>
<img src="window.svg" width="$w" height="$h">
EOF

"$chrome" --headless=new --disable-gpu --hide-scrollbars --force-device-scale-factor=2 \
	--allow-file-access-from-files --window-size="$pw,$ph" \
	--screenshot="$out" "file://$work/page.html" >/dev/null 2>&1

echo "wrote $out (${pw}x${ph} @2x)"
