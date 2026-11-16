package curate

import (
	"errors"
	"fmt"
	"time"

	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/store"
)

// ErrModelDown: a day's pass found the model unavailable before it had read every admitted unit, or could not read
// the day at all because the assistant's computer was away (its conversations are one of the day's sources). The day is not
// counted as done: the first-run replay stays on it, a rerun is not marked, and the passes wait (State.ModelPause)
// until the next try. What was read before the model went away is kept (the state is saved after every unit).
var ErrModelDown = errors.New("model unavailable: the day is kept to run again")

// PausedAfter is how many passes in a row must find the model unavailable before the page says the curator is paused
// (one or two are a hiccup the backoff absorbs).
const PausedAfter = 3

// pauseBackoff is the wait before the next try after fails failures in a row: 10 minutes, doubling, at most 2 hours.
func pauseBackoff(fails int) time.Duration {
	d := 10 * time.Minute
	for i := 1; i < fails && d < 2*time.Hour; i++ {
		d *= 2
	}
	return min(d, 2*time.Hour)
}

// notePause records a pass that found the model unavailable (the caller holds the lock) and returns the error the
// pass ends with.
func (c *Curator) notePause(day string, cause error) error {
	return c.notePauseOf(day, cause, "")
}

// notePauseOf is notePause for what was away ("" the model, "assistant" the assistant's computer).
func (c *Curator) notePauseOf(day string, cause error, what string) error {
	now := c.now().UTC()
	p := c.St.S.ModelPause
	if p == nil {
		p = &store.ModelPause{Since: now.Format(time.RFC3339)}
		c.St.S.ModelPause = p
	}
	p.Fails++
	p.Day, p.Reason, p.What = day, modelReason(cause), what
	p.NextTry = now.Add(pauseBackoff(p.Fails)).Format(time.RFC3339)
	return fmt.Errorf("%w: %s (%s)", ErrModelDown, day, p.Reason)
}

// clearPause: a pass reached the model; the passes go on as usual (the caller holds the lock).
func (c *Curator) clearPause() { c.St.S.ModelPause = nil }

// ModelWait says until when passes that need the model wait, and whether they wait now.
func (c *Curator) ModelWait(now time.Time) (time.Time, bool) {
	c.St.Lock()
	defer c.St.Unlock()
	p := c.St.S.ModelPause
	if p == nil {
		return time.Time{}, false
	}
	next, err := time.Parse(time.RFC3339, p.NextTry)
	if err != nil {
		return time.Time{}, false
	}
	return next, now.Before(next)
}
