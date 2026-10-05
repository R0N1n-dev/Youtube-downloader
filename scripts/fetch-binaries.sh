#!/usr/bin/env bash
# Downloads yt-dlp and a static ffmpeg/ffprobe build for Linux and drops them
# next to your built app, so it runs standalone with nothing installed.
#
# Run this AFTER `wails build`, from the project root:
#   bash scripts/fetch-binaries.sh
set -euo pipefail

OUT_DIR="$(dirname "$0")/../build/bin"
mkdir -p "$OUT_DIR"

echo "Downloading yt-dlp..."
curl -L "https://github.com/yt-dlp/yt-dlp/releases/latest/download/yt-dlp" -o "$OUT_DIR/yt-dlp"
chmod +x "$OUT_DIR/yt-dlp"

echo "Downloading ffmpeg/ffprobe (static build, amd64)..."
TMP=$(mktemp -d)
curl -L "https://johnvansickle.com/ffmpeg/releases/ffmpeg-release-amd64-static.tar.xz" -o "$TMP/ffmpeg.tar.xz"
tar -xf "$TMP/ffmpeg.tar.xz" -C "$TMP"
FFDIR=$(find "$TMP" -maxdepth 1 -type d -name "ffmpeg-*-static")
cp "$FFDIR/ffmpeg" "$OUT_DIR/ffmpeg"
cp "$FFDIR/ffprobe" "$OUT_DIR/ffprobe"
chmod +x "$OUT_DIR/ffmpeg" "$OUT_DIR/ffprobe"
rm -rf "$TMP"

echo
echo "Done. yt-dlp, ffmpeg and ffprobe are now in build/bin next to your app."
echo "Zip up everything in build/bin and it will run on any Linux x86_64 machine, no install needed."
