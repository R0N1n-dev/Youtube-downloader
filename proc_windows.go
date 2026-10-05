//go:build windows

package main

import (
	"os/exec"
	"strconv"
	"syscall"
)

const createNoWindow = 0x08000000

// prepareCmd stops Windows from opening a console window for the subprocess.
func prepareCmd(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: createNoWindow,
	}
}

// killProcessTree kills the process and everything it spawned. yt-dlp.exe
// launches child processes (and ffmpeg), so killing only the parent can leave
// a download running invisibly in the background.
func killProcessTree(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	kill := exec.Command("taskkill", "/T", "/F", "/PID", strconv.Itoa(cmd.Process.Pid))
	prepareCmd(kill)
	_ = kill.Run()
	_ = cmd.Process.Kill()
}
