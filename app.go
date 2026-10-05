package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	goruntime "runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/wailsapp/wails/v2/pkg/runtime"
)

type App struct {
	ctx context.Context

	mu       sync.Mutex
	fetches  map[string]context.CancelFunc  // in-flight link checks, by job id
	runs     map[string]*downloadRun        // in-flight downloads, by job id
	partials map[string]map[string]struct{} // files yt-dlp created per job, for cleanup on cancel
}

type downloadRun struct {
	cancel     context.CancelFunc
	cmd        *exec.Cmd
	stopReason string // "", "pause" or "cancel"
}

func NewApp() *App {
	return &App{
		fetches:  map[string]context.CancelFunc{},
		runs:     map[string]*downloadRun{},
		partials: map[string]map[string]struct{}{},
	}
}

func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
}

// shutdown makes sure closing the window never leaves yt-dlp running invisibly.
func (a *App) shutdown(ctx context.Context) {
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, cancel := range a.fetches {
		cancel()
	}
	for _, run := range a.runs {
		killProcessTree(run.cmd)
	}
}

// ---------- binary lookup ----------

// resolveBinary looks for <name>(.exe) next to the running executable first
// (so a bundled copy of yt-dlp/ffmpeg is always preferred), and falls back to
// whatever is on PATH if no local copy is found.
func resolveBinary(name string) string {
	binName := name
	if goruntime.GOOS == "windows" {
		binName = name + ".exe"
	}
	if exePath, err := os.Executable(); err == nil {
		local := filepath.Join(filepath.Dir(exePath), binName)
		if _, err := os.Stat(local); err == nil {
			return local
		}
	}
	return name
}

func ytdlpPath() string {
	return resolveBinary("yt-dlp")
}

// ffmpegDir returns the directory of a bundled ffmpeg, if one sits next to
// the executable, so it can be passed to yt-dlp via --ffmpeg-location.
func ffmpegDir() string {
	binName := "ffmpeg"
	if goruntime.GOOS == "windows" {
		binName = "ffmpeg.exe"
	}
	if exePath, err := os.Executable(); err == nil {
		dir := filepath.Dir(exePath)
		if _, err := os.Stat(filepath.Join(dir, binName)); err == nil {
			return dir
		}
	}
	return ""
}

// ---------- settings ----------

type Settings struct {
	DownloadFolder string `json:"downloadFolder"`
	MaxConcurrent  int    `json:"maxConcurrent"`
	DefaultQuality string `json:"defaultQuality"`
}

func defaultSettings() Settings {
	return Settings{MaxConcurrent: 2, DefaultQuality: "best"}
}

func settingsPath() (string, error) {
	configDir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(configDir, "ytdlp-downloader")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	return filepath.Join(dir, "settings.json"), nil
}

// GetSettings loads saved settings, filling in defaults for anything missing.
func (a *App) GetSettings() (*Settings, error) {
	s := defaultSettings()
	path, err := settingsPath()
	if err != nil {
		return &s, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return &s, nil
	}
	_ = json.Unmarshal(data, &s)
	if s.MaxConcurrent < 1 || s.MaxConcurrent > 5 {
		s.MaxConcurrent = 2
	}
	if s.DefaultQuality == "" {
		s.DefaultQuality = "best"
	}
	return &s, nil
}

func (a *App) SaveSettings(s Settings) error {
	path, err := settingsPath()
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

// SelectDownloadFolder opens a native OS directory picker.
func (a *App) SelectDownloadFolder() (string, error) {
	return runtime.OpenDirectoryDialog(a.ctx, runtime.OpenDialogOptions{
		Title: "Select download folder",
	})
}

// CheckYtDlp verifies yt-dlp (bundled or on PATH) is usable and returns its version.
func (a *App) CheckYtDlp() (string, error) {
	cmd := exec.Command(ytdlpPath(), "--version")
	prepareCmd(cmd)
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("yt-dlp not found (checked next to the app and on PATH): %v", err)
	}
	return strings.TrimSpace(string(out)), nil
}

// ---------- link checking ----------

type VideoInfo struct {
	Title     string  `json:"title"`
	Duration  float64 `json:"duration"`
	Thumbnail string  `json:"thumbnail"`
	Uploader  string  `json:"uploader"`
}

// runCapture runs cmd, returning stdout. If ctx is cancelled the whole process
// tree is killed. Failures return yt-dlp's own "ERROR:" line when there is one.
func runCapture(ctx context.Context, cmd *exec.Cmd) ([]byte, error) {
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return nil, err
	}

	done := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			killProcessTree(cmd)
		case <-done:
		}
	}()

	err := cmd.Wait()
	close(done)

	if ctxErr := ctx.Err(); ctxErr != nil {
		return nil, ctxErr
	}
	if err != nil {
		return nil, extractError(stderr.String(), err)
	}
	return stdout.Bytes(), nil
}

func extractError(stderr string, fallback error) error {
	lines := strings.Split(stderr, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		l := strings.TrimSpace(lines[i])
		if strings.HasPrefix(l, "ERROR:") {
			return errors.New(strings.TrimSpace(strings.TrimPrefix(l, "ERROR:")))
		}
	}
	return fallback
}

// FetchInfo checks a link and returns its title/thumbnail. It blocks until
// done, but can be aborted from the UI with CancelFetch(id).
func (a *App) FetchInfo(id string, url string) (*VideoInfo, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	a.mu.Lock()
	a.fetches[id] = cancel
	a.mu.Unlock()
	defer func() {
		a.mu.Lock()
		delete(a.fetches, id)
		a.mu.Unlock()
		cancel()
	}()

	cmd := exec.Command(ytdlpPath(), "-j", "--no-playlist", "--playlist-items", "1", "--no-warnings", url)
	prepareCmd(cmd)
	out, err := runCapture(ctx, cmd)
	if err != nil {
		switch {
		case errors.Is(err, context.DeadlineExceeded):
			return nil, errors.New("timed out while checking the link")
		case errors.Is(err, context.Canceled):
			return nil, errors.New("cancelled")
		}
		return nil, err
	}

	var info VideoInfo
	if err := json.NewDecoder(bytes.NewReader(out)).Decode(&info); err != nil {
		return nil, fmt.Errorf("could not read video info: %v", err)
	}
	return &info, nil
}

func (a *App) CancelFetch(id string) {
	a.mu.Lock()
	cancel := a.fetches[id]
	a.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

// ---------- downloading ----------

var (
	destRe    = regexp.MustCompile(`^\[(?:download|ExtractAudio)\] Destination: (.+)$`)
	mergeRe   = regexp.MustCompile(`^\[Merger\] Merging formats into "(.+)"$`)
	percentRe = regexp.MustCompile(`^\[download\]\s+(\d+(?:\.\d+)?)%`)
	sizeRe    = regexp.MustCompile(`\bof\s+~?\s*(\S+)`)
	speedRe   = regexp.MustCompile(`\bat\s+(\S+)`)
	etaRe     = regexp.MustCompile(`\bETA\s+(\S+)`)
)

func firstGroup(re *regexp.Regexp, s string) string {
	if m := re.FindStringSubmatch(s); m != nil {
		return m[1]
	}
	return ""
}

func cleanUnknown(s string) string {
	if strings.HasPrefix(s, "Unknown") || s == "N/A" {
		return ""
	}
	return s
}

// qualityArgs maps a quality preset from the UI to yt-dlp arguments.
// Numbered presets prefer H.264/AAC so the result is a normal MP4 that plays
// everywhere. "best" lets yt-dlp pick the highest quality, which for 1440p/4K
// on YouTube means VP9/AV1 in a WebM or MKV container.
func qualityArgs(quality string) []string {
	switch quality {
	case "audio":
		return []string{"-f", "ba/b", "-x", "--audio-format", "mp3"}
	case "", "best":
		return nil
	}
	if h, err := strconv.Atoi(quality); err == nil && h > 0 {
		return []string{
			"-S", fmt.Sprintf("vcodec:h264,res:%d,acodec:m4a", h),
			"--merge-output-format", "mp4",
		}
	}
	return nil
}

func (a *App) emitStatus(id, status, errMsg string) {
	runtime.EventsEmit(a.ctx, "job-status", map[string]string{
		"id": id, "status": status, "error": errMsg,
	})
}

// StartDownload launches yt-dlp for one job and returns immediately. Progress
// and results arrive as "job-progress" and "job-status" events. Starting a job
// that has partial files from an earlier pause resumes it (yt-dlp continues
// from the .part file).
func (a *App) StartDownload(id string, url string, quality string, outputDir string) error {
	if outputDir == "" {
		return errors.New("no download folder set")
	}

	ctx, cancel := context.WithCancel(context.Background())
	run := &downloadRun{cancel: cancel}

	a.mu.Lock()
	if _, busy := a.runs[id]; busy {
		a.mu.Unlock()
		cancel()
		return errors.New("this item is already downloading")
	}
	a.runs[id] = run
	a.mu.Unlock()

	fail := func(err error) error {
		a.mu.Lock()
		delete(a.runs, id)
		a.mu.Unlock()
		cancel()
		return err
	}

	args := []string{
		"--newline", "--no-playlist", "--continue",
		"-o", filepath.Join(outputDir, "%(title)s.%(ext)s"),
	}
	if dir := ffmpegDir(); dir != "" {
		args = append(args, "--ffmpeg-location", dir)
	}
	args = append(args, qualityArgs(quality)...)
	args = append(args, url)

	cmd := exec.Command(ytdlpPath(), args...)
	prepareCmd(cmd)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fail(err)
	}
	cmd.Stderr = cmd.Stdout

	a.mu.Lock()
	run.cmd = cmd
	a.mu.Unlock()

	if err := cmd.Start(); err != nil {
		return fail(err)
	}

	a.emitStatus(id, "downloading", "")
	go a.monitor(id, run, ctx, cmd, stdout)
	return nil
}

func (a *App) monitor(id string, run *downloadRun, ctx context.Context, cmd *exec.Cmd, stdout io.Reader) {
	done := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			killProcessTree(cmd)
		case <-done:
		}
	}()

	var (
		lastErr    string
		processing bool
		stage      int
		lastEmit   time.Time
	)

	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Text()

		if !processing && (strings.HasPrefix(line, "[Merger]") ||
			strings.HasPrefix(line, "[ExtractAudio]") ||
			strings.HasPrefix(line, "[Fixup")) {
			processing = true
			a.emitStatus(id, "processing", "")
		}

		if m := destRe.FindStringSubmatch(line); m != nil {
			a.trackPartial(id, m[1])
			if strings.HasPrefix(line, "[download]") {
				stage++
			}
			continue
		}
		if m := mergeRe.FindStringSubmatch(line); m != nil {
			a.trackPartial(id, m[1])
			continue
		}
		if strings.HasPrefix(line, "[download]") && strings.HasSuffix(line, "has already been downloaded") {
			stage++
			continue
		}
		if m := percentRe.FindStringSubmatch(line); m != nil {
			pct, _ := strconv.ParseFloat(m[1], 64)
			if pct < 100 && time.Since(lastEmit) < 200*time.Millisecond {
				continue
			}
			lastEmit = time.Now()
			runtime.EventsEmit(a.ctx, "job-progress", map[string]any{
				"id":      id,
				"percent": pct,
				"size":    cleanUnknown(firstGroup(sizeRe, line)),
				"speed":   cleanUnknown(firstGroup(speedRe, line)),
				"eta":     cleanUnknown(firstGroup(etaRe, line)),
				"stage":   stage,
			})
			continue
		}
		if strings.HasPrefix(line, "ERROR:") {
			lastErr = strings.TrimSpace(strings.TrimPrefix(line, "ERROR:"))
		}
	}

	waitErr := cmd.Wait()
	close(done)
	run.cancel()

	a.mu.Lock()
	reason := run.stopReason
	delete(a.runs, id)
	a.mu.Unlock()

	switch {
	case waitErr == nil:
		a.takePartials(id) // finished cleanly, nothing to clean up later
		a.emitStatus(id, "done", "")
	case reason == "pause":
		a.emitStatus(id, "paused", "")
	case reason == "cancel":
		a.removePartials(id)
		a.emitStatus(id, "cancelled", "")
	default:
		msg := lastErr
		if msg == "" {
			msg = waitErr.Error()
		}
		a.emitStatus(id, "error", msg)
	}
}

// PauseDownload stops yt-dlp but keeps the partial files, so starting the same
// job again continues from where it stopped.
func (a *App) PauseDownload(id string) {
	a.mu.Lock()
	run := a.runs[id]
	if run != nil && run.stopReason == "" {
		run.stopReason = "pause"
	}
	a.mu.Unlock()
	if run != nil {
		run.cancel()
	}
}

// CancelDownload stops a running job, or discards a paused/failed one, and
// deletes whatever partial files it left behind.
func (a *App) CancelDownload(id string) {
	a.mu.Lock()
	run := a.runs[id]
	if run != nil {
		run.stopReason = "cancel"
	}
	a.mu.Unlock()

	if run != nil {
		run.cancel() // monitor() does the cleanup and emits "cancelled"
		return
	}
	go func() {
		a.removePartials(id)
		a.emitStatus(id, "cancelled", "")
	}()
}

// ---------- partial file bookkeeping ----------

func (a *App) trackPartial(id, path string) {
	path = strings.TrimSpace(path)
	if path == "" {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.partials[id] == nil {
		a.partials[id] = map[string]struct{}{}
	}
	a.partials[id][path] = struct{}{}
}

func (a *App) takePartials(id string) []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	set := a.partials[id]
	delete(a.partials, id)
	paths := make([]string, 0, len(set))
	for p := range set {
		paths = append(paths, p)
	}
	return paths
}

func (a *App) removePartials(id string) {
	for _, p := range a.takePartials(id) {
		removeLeftovers(p)
	}
}

// removeLeftovers deletes a file yt-dlp was writing, plus its .part, .ytdl and
// fragment files. It scans the folder by prefix instead of using globbing,
// because video titles often contain [ ] characters that break glob patterns.
func removeLeftovers(path string) {
	dir, base := filepath.Split(path)
	if dir == "" {
		dir = "."
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		n := e.Name()
		if n == base || n == base+".ytdl" || strings.HasPrefix(n, base+".part") {
			removeWithRetry(filepath.Join(dir, n))
		}
	}
}

// Windows can hold a file handle for a moment after the process dies.
func removeWithRetry(p string) {
	for i := 0; i < 6; i++ {
		err := os.Remove(p)
		if err == nil || os.IsNotExist(err) {
			return
		}
		time.Sleep(250 * time.Millisecond)
	}
}
