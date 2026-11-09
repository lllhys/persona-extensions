//go:build unix

package x

import (
	"os/exec"
	"syscall"
)

// ownGroup starts cmd in a process group of its own, and makes cancelling it kill the whole group (grok and anything
// it started), not only grok.
func ownGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
}

// reapGroup kills what is left of cmd's process group once cmd has ended, however it ended: an MCP server or helper
// grok started does not outlive the run. The group's id stays grok's pid while any member lives, so it is never
// another process's; with none left the kill finds nothing (ESRCH).
func reapGroup(cmd *exec.Cmd) {
	if cmd.Process != nil {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
}
