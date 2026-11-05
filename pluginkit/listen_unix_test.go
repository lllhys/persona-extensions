//go:build unix

package pluginkit

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func TestListenUnixSocketMode(t *testing.T) {
	// Force a permissive umask to prove listenUnix overrides it at socket birth.
	saved := syscall.Umask(0o022)
	defer syscall.Umask(saved)

	path := filepath.Join(t.TempDir(), "test.sock")
	ln, err := listenUnix(path)
	if err != nil {
		t.Fatalf("listenUnix: %v", err)
	}
	defer ln.Close()

	fi, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat socket: %v", err)
	}
	// umask 0077 must strip every group/other bit at birth; ServeHTTP's Chmod
	// then narrows the owner bits from 7 to 6 as defense in depth.
	if mode := fi.Mode().Perm(); mode&0o077 != 0 {
		t.Errorf("socket mode = %o, want no group/other bits", mode)
	}
}
