package hubapi

import (
	"os"
	"syscall"
	"time"
)

// birthTime is when the file was created (macOS keeps it), or zero.
func birthTime(info os.FileInfo) time.Time {
	if st, ok := info.Sys().(*syscall.Stat_t); ok && st.Birthtimespec.Sec > 0 {
		return time.Unix(st.Birthtimespec.Sec, st.Birthtimespec.Nsec)
	}
	return time.Time{}
}
