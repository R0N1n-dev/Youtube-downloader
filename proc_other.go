//go:build !windows

package main

import (
	"os/exec"
	"syscall"
)

// prepareCmd puts the subprocess in its own process group so the whole
// group (yt-dlp plus ffmpeg) can be killed together.
func prepareCmd(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

func killProcessTree(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	_ = cmd.Process.Kill()
}
