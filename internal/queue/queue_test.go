package queue

import (
	"errors"
	"sync"
	"testing"

	"github.com/moepig/flexi/internal/core"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Purpose: Verify that the Queue preserves insertion order across Enqueue calls.
// Method:  Enqueue tickets "a", "b", "c" in order, then take a Snapshot.
// Expect:  Snapshot returns the same ["a", "b", "c"] ordering.
func TestEnqueueOrder(t *testing.T) {
	q := New()
	require.NoError(t, q.Enqueue(core.Ticket{ID: "a"}))
	require.NoError(t, q.Enqueue(core.Ticket{ID: "b"}))
	require.NoError(t, q.Enqueue(core.Ticket{ID: "c"}))
	snap := q.Snapshot()
	got := []string{snap[0].ID, snap[1].ID, snap[2].ID}
	assert.Equal(t, []string{"a", "b", "c"}, got)
}

// Purpose: Verify that enqueueing the same ticket ID twice is rejected.
// Method:  Enqueue ID "a" twice in succession.
// Expect:  The second call returns ErrDuplicateTicket.
func TestDuplicate(t *testing.T) {
	q := New()
	require.NoError(t, q.Enqueue(core.Ticket{ID: "a"}))
	err := q.Enqueue(core.Ticket{ID: "a"})
	assert.True(t, errors.Is(err, ErrDuplicateTicket))
}

// Purpose: Verify that Cancel removes an existing ticket and returns ErrUnknownTicket for an unknown ID.
// Method:  Enqueue two tickets, Cancel one, then Cancel a non-existent ID.
// Expect:  First Cancel succeeds and Len becomes 1; second Cancel returns ErrUnknownTicket.
func TestCancel(t *testing.T) {
	q := New()
	_ = q.Enqueue(core.Ticket{ID: "a"})
	_ = q.Enqueue(core.Ticket{ID: "b"})
	require.NoError(t, q.Cancel("a"))
	assert.Equal(t, 1, q.Len())
	err := q.Cancel("nope")
	assert.True(t, errors.Is(err, ErrUnknownTicket))
}

// Purpose: Verify that Remove (bulk deletion used after a match forms) deletes only the target IDs and preserves order.
// Method:  Enqueue "a","b","c","d", call Remove(["b","d"]), then Snapshot.
// Expect:  Snapshot is ["a", "c"] in the original relative order.
func TestRemove(t *testing.T) {
	q := New()
	for _, id := range []string{"a", "b", "c", "d"} {
		_ = q.Enqueue(core.Ticket{ID: id})
	}
	q.Remove([]string{"b", "d"})
	snap := q.Snapshot()
	assert.Equal(t, []string{"a", "c"}, []string{snap[0].ID, snap[1].ID})
}

// Purpose: Verify that the Queue is safe under concurrent Enqueue calls with no lost or duplicated entries.
// Method:  Spawn 50 goroutines each enqueueing a unique ID simultaneously.
// Expect:  All enqueues succeed and the final Len equals 50 (run with -race to catch data races).
func TestConcurrent(t *testing.T) {
	q := New()
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = q.Enqueue(core.Ticket{ID: string(rune('A' + i%26)) + string(rune('0'+i/26))})
		}()
	}
	wg.Wait()
	assert.Equal(t, 50, q.Len())
}
