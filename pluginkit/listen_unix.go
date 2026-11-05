//go:build unix

package pluginkit

import (
	"net"
	"syscall"
)

// listenUnix creates the unix domain socket with a 0077 umask so the node is
// born with no group/other bits (0700 on Linux); the caller's Chmod narrows
// the owner bits to 0600 as defense in depth.
func listenUnix(path string) (net.Listener, error) {
	old := syscall.Umask(0o077)
	defer syscall.Umask(old)
	return net.Listen("unix", path)
}
