//go:build !darwin

package hubapi

import (
	"os"
	"time"
)

// birthTime is zero where the system does not say when a file was created: the memory's created time is then left
// empty, as H2 leaves it.
func birthTime(os.FileInfo) time.Time { return time.Time{} }
