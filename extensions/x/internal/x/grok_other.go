//go:build !unix

package x

import "os/exec"

// ownGroup: without process groups, cancelling kills grok itself (exec's default).
func ownGroup(*exec.Cmd) {}

// reapGroup: without process groups there is no group to kill.
func reapGroup(*exec.Cmd) {}
