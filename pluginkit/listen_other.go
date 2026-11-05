//go:build !unix

package pluginkit

import "net"

// listenUnix falls back to a plain listen on platforms without syscall.Umask.
func listenUnix(path string) (net.Listener, error) {
	return net.Listen("unix", path)
}
