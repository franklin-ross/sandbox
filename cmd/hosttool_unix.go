//go:build !windows

package cmd

import (
	"os/exec"
	"syscall"
)

// setSysProcAttr detaches the child process so it outlives the parent.
func setSysProcAttr(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}
