package audio

import (
	"sync"
	"time"
)

// Cursor tracks a shared audio timeline in wall-clock terms so that
// multiple outputs (WebRTC, HLS) can stay aligned.
type Cursor struct {
	mu       sync.Mutex
	started  time.Time
	position time.Duration
}

type Snapshot struct {
	StartedAt time.Time
	Position  time.Duration
	WallClock time.Time
}

func NewCursor() *Cursor {
	now := time.Now()
	return &Cursor{
		started:  now,
		position: 0,
	}
}

// Advance increments the cursor and returns the new absolute position.
func (c *Cursor) Advance(d time.Duration) time.Duration {
	if d <= 0 {
		return c.Position()
	}

	c.mu.Lock()
	c.position += d
	pos := c.position
	c.mu.Unlock()

	return pos
}

// Position returns the current offset from the start of the stream.
func (c *Cursor) Position() time.Duration {
	c.mu.Lock()
	pos := c.position
	c.mu.Unlock()

	return pos
}

// StartedAt returns the wall clock time when the cursor began.
func (c *Cursor) StartedAt() time.Time {
	c.mu.Lock()
	start := c.started
	c.mu.Unlock()

	return start
}

// Snapshot returns a thread-safe snapshot of the cursor state.
func (c *Cursor) Snapshot() Snapshot {
	c.mu.Lock()
	start := c.started
	pos := c.position
	c.mu.Unlock()

	return Snapshot{
		StartedAt: start,
		Position:  pos,
		WallClock: start.Add(pos),
	}
}
