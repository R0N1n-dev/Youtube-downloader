# YT-DLP Downloader (Wails + Vue)

A desktop GUI for yt-dlp with a download list: paste one or many links, choose quality per item, and download several at once.

## Using it

- Paste one or more links (one per line) and press Enter. Each link is checked in the background with a spinner and a Cancel button.
- Every item has its own quality dropdown. New items start with your default quality.
- Download one item, or use "Download all". The "Downloads at once" setting (1 to 5) controls how many run in parallel; the rest wait in line.
- Pause and Resume work per item, plus "Pause all" / "Resume all".
- Cancel stops an item and deletes its partial files.
- The download folder and settings are saved and reused on the next launch.

### How pause works

Windows cannot freeze a process, so Pause stops yt-dlp and keeps the partial `.part` file. Resume starts yt-dlp again and it continues from that file. You may lose a few seconds of progress at the pause point, but the download does not restart from zero.

### Quality options

- "Best available" lets yt-dlp pick the highest quality. For 1440p and 4K on YouTube that is usually WebM or MKV, not MP4.
- 1080p, 720p, 480p and 360p prefer H.264 video and AAC audio in an MP4 container, which plays in any player. If a video has no such stream, yt-dlp falls back to the closest match.
- "Audio only" saves an MP3 (needs ffmpeg).

## Prerequisites (for building it)

- Go 1.21+
- Node 18+
- Wails CLI: `go install github.com/wailsapp/wails/v2/cmd/wails@latest`

You do not need yt-dlp or ffmpeg on your dev machine to build, only to test, because the app looks for them at runtime (see below).

## Run in dev mode

```bash
cd ytdlp-downloader
wails dev
```

The first run pulls npm dependencies and Go modules. It also generates `frontend/wailsjs/` (the JS bindings for the Go methods). Those are regenerated automatically whenever the Go API changes.

## Build for Windows, runnable on any Windows machine

```bash
wails build -platform windows/amd64
```

This produces `build\bin\ytdlp-downloader.exe`. To make it fully standalone, run this afterwards:

```powershell
powershell -ExecutionPolicy Bypass -File scripts\fetch-binaries.ps1
```

It downloads `yt-dlp.exe` and `ffmpeg.exe` into `build\bin` next to your app. Zip the whole `build\bin` folder and it runs on a bare Windows machine with nothing installed. Do not send the .exe alone if you want it dependency free.

To do it by hand instead, download `yt-dlp.exe` from https://github.com/yt-dlp/yt-dlp/releases/latest and `ffmpeg.exe` from a Windows build such as https://www.gyan.dev/ffmpeg/builds/ and drop both next to the app exe.

## How the binary lookup works

`resolveBinary` in `app.go` checks the folder containing the running exe first, then falls back to PATH. So a bundled copy always wins, and a machine that already has yt-dlp on PATH works without bundling.

## Code layout

- `app.go`: everything the frontend can call. Links are checked with `FetchInfo`, downloads run with `StartDownload`, `PauseDownload`, `CancelDownload`. Each item has an id, and progress arrives as `job-progress` and `job-status` events.
- `proc_windows.go` / `proc_other.go`: hides the console window on Windows and kills yt-dlp together with everything it spawned (`taskkill /T` on Windows, process groups elsewhere).
- `frontend/src/App.vue`: the list, the scheduler that enforces the parallel limit, and the UI.

## Notes

- Closing the app stops any active downloads. The list itself is not saved between launches. Partial files stay in the folder, and adding the same link again lets yt-dlp continue them.
- Only the first video of a playlist link is used. Playlists are not expanded into separate items.
- Re-run `scripts\fetch-binaries.ps1` now and then. YouTube changes often, and yt-dlp needs to stay current to keep working.
- This wraps local yt-dlp and ffmpeg binaries. It does not bypass any platform restrictions on its own.
