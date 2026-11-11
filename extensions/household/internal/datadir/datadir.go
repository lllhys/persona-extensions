// Package datadir finds the data directory of the household extension, for the hub-run extension and for the
// `household mcp` server the assistant starts. The hub-run extension records its directory in a locator file, so
// that the server finds the same records.
package datadir

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	envData  = "PERSONA_HOUSEHOLD_DATA"
	dataSub  = "data"
	locName  = "data-dir"
	zoneFile = "zone"
	appDir   = "persona-household"
	noConfig = "找不到用户配置目录，请用 --data 指定数据目录"
)

// Locator is the file that records the data directory of the hub-run extension. It is a variable so that tests
// never touch the real configuration directory.
var Locator = func() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, appDir, locName)
}

// Resolve returns the data directory: the flag, then PERSONA_HOUSEHOLD_DATA, then the directory recorded in the
// locator file when it exists, then <UserConfigDir>/persona-household/data.
func Resolve(flag string) (string, error) {
	if flag != "" {
		return flag, nil
	}
	if env := os.Getenv(envData); env != "" {
		return env, nil
	}
	loc := Locator()
	if loc == "" {
		return "", errors.New(noConfig)
	}
	if raw, err := os.ReadFile(loc); err == nil {
		if dir := strings.TrimSpace(string(raw)); dir != "" {
			if fi, err := os.Stat(dir); err == nil && fi.IsDir() {
				return dir, nil
			}
		}
	}
	return filepath.Join(filepath.Dir(loc), dataSub), nil
}

// Remember writes the data directory to the locator file. It is best effort: failures are ignored.
func Remember(dir string) {
	loc := Locator()
	if dir == "" || loc == "" {
		return
	}
	if err := os.MkdirAll(filepath.Dir(loc), 0o700); err != nil {
		return
	}
	_ = os.WriteFile(loc, []byte(dir+"\n"), 0o600)
}

// RememberZone writes the IANA name of the hub's time zone to the zone file in dir, so that `household mcp` counts
// days the same way as the hub-run extension. It replaces the file atomically (a temporary file, then a rename).
// The directory is created when it is missing. It is best effort: failures are ignored.
func RememberZone(dir, name string) {
	if dir == "" || name == "" {
		return
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return
	}
	tmp, err := os.CreateTemp(dir, ".zone-*")
	if err != nil {
		return
	}
	_, werr := tmp.WriteString(name + "\n")
	cerr := tmp.Close()
	if werr != nil || cerr != nil || os.Rename(tmp.Name(), filepath.Join(dir, zoneFile)) != nil {
		_ = os.Remove(tmp.Name())
	}
}

// Zone returns the time zone recorded in dir by RememberZone, or this machine's zone when the file is missing or
// does not name a zone.
func Zone(dir string) *time.Location {
	raw, err := os.ReadFile(filepath.Join(dir, zoneFile))
	if err != nil {
		return time.Local
	}
	name := strings.TrimSpace(string(raw))
	if name == "" {
		return time.Local
	}
	if loc, err := time.LoadLocation(name); err == nil {
		return loc
	}
	return time.Local
}
