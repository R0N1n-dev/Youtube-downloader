# Downloads yt-dlp.exe and ffmpeg.exe and drops them next to your built app
# in build\bin, so the app runs standalone on any Windows machine with no
# separate install required.
#
# Run this AFTER `wails build`, from the project root:
#   powershell -ExecutionPolicy Bypass -File scripts\fetch-binaries.ps1

$ErrorActionPreference = "Stop"

$outDir = Join-Path $PSScriptRoot "..\build\bin"
New-Item -ItemType Directory -Force -Path $outDir | Out-Null

Write-Host "Downloading yt-dlp.exe..."
Invoke-WebRequest -Uri "https://github.com/yt-dlp/yt-dlp/releases/latest/download/yt-dlp.exe" `
    -OutFile (Join-Path $outDir "yt-dlp.exe")

Write-Host "Downloading ffmpeg (this is a few dozen MB, may take a moment)..."
$ffmpegZip = Join-Path $env:TEMP "ffmpeg-download.zip"
$ffmpegExtract = Join-Path $env:TEMP "ffmpeg-extract"
Invoke-WebRequest -Uri "https://www.gyan.dev/ffmpeg/builds/ffmpeg-release-essentials.zip" -OutFile $ffmpegZip
if (Test-Path $ffmpegExtract) { Remove-Item $ffmpegExtract -Recurse -Force }
Expand-Archive -Path $ffmpegZip -DestinationPath $ffmpegExtract -Force

$ffmpegExe = Get-ChildItem -Path $ffmpegExtract -Recurse -Filter "ffmpeg.exe" | Select-Object -First 1
if (-not $ffmpegExe) {
    throw "Could not find ffmpeg.exe in the downloaded archive. The gyan.dev build layout may have changed; check https://www.gyan.dev/ffmpeg/builds/ manually."
}
Copy-Item $ffmpegExe.FullName -Destination (Join-Path $outDir "ffmpeg.exe") -Force

Remove-Item $ffmpegZip -Force
Remove-Item $ffmpegExtract -Recurse -Force

Write-Host ""
Write-Host "Done. yt-dlp.exe and ffmpeg.exe are now in build\bin next to your app exe."
Write-Host "Zip up everything in build\bin and it will run on any Windows machine, no install needed."
