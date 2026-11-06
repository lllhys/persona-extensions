package main

import (
	"encoding/json"
	"errors"
	"os"
)

// readState decodes the JSON in file into a new T. A missing file gives the zero value and is not logged. One that
// exists but cannot be read or parsed is renamed to <file>.corrupt (replacing an older one) and logged, and gives the
// zero value too: the next save would otherwise replace what it held with only that change (#274).
func readState[T any](file string, logf func(string, ...any)) (T, bool) {
	var v T
	raw, err := os.ReadFile(file)
	if errors.Is(err, os.ErrNotExist) {
		return v, false
	}
	if err == nil {
		if err = json.Unmarshal(raw, &v); err == nil {
			return v, true
		}
		var zero T
		v = zero // a type error leaves part of the file decoded
	}
	if rerr := os.Rename(file, file+".corrupt"); rerr != nil {
		logf("state: %s unreadable (%v), and it could not be moved aside: %v", file, err, rerr)
	} else {
		logf("state: %s unreadable (%v), moved to %s.corrupt", file, err, file)
	}
	return v, false
}
