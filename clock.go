package flexi

import (
	"sync"
	"time"
)

// Clock is the source of "now" used by the matchmaker.
//
// It exists so that callers — and especially tests — can control what time
// looks like. All time-dependent behaviour in flexi (computing how long a
// ticket has been waiting, deciding which expansion step is active) reads
// the current time through this interface rather than calling time.Now
// directly.
//
// Implementations must be safe for concurrent use.
type Clock interface {
	// Now returns the current time as observed by this Clock.
	Now() time.Time
}

// SystemClock is the default [Clock]; it returns the wall-clock time from
// the standard library's time.Now.
//
// SystemClock has no state and a zero value is ready to use.
type SystemClock struct{}

// Now returns time.Now().
func (SystemClock) Now() time.Time { return time.Now() }

// FakeClock is a [Clock] whose value is set explicitly and never advances on
// its own. It is intended for tests: construct one with [NewFakeClock], then
// call [FakeClock.Advance] or [FakeClock.Set] to move time forward between
// matchmaker ticks.
//
// FakeClock is safe for concurrent use.
type FakeClock struct {
	mu  sync.Mutex
	now time.Time
}

// NewFakeClock returns a FakeClock anchored at t.
//
// Choose any wall-clock value you find convenient for diagnostics; flexi only
// looks at differences between Now() readings, never at absolute time.
func NewFakeClock(t time.Time) *FakeClock {
	return &FakeClock{now: t}
}

// Now returns the FakeClock's current value.
func (c *FakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

// Advance moves the FakeClock forward by d.
//
// Use this between calls to [Matchmaker.Tick] in tests to simulate the
// passage of time and trigger expansion steps.
func (c *FakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// Set replaces the FakeClock's current value with t.
//
// Useful when a test needs to jump to an absolute moment rather than a
// relative offset.
func (c *FakeClock) Set(t time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = t
}
