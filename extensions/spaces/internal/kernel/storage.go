package kernel

import "time"

// Storage is where a Base lives. The file store implements it today; a hub-provided store can replace it.
type Storage interface {
	View(f func(b *Base) error) error                  // read-only; f must not keep b
	Update(f func(b *Base, now time.Time) error) error // all-or-nothing; an error saves nothing
	Changed() bool                                     // the stored Base differs from what this process last saw
}
