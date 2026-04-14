package queue

import (
	"errors"
	"sync"
	"testing"

	"github.com/moepig/flexi/internal/core"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEnqueueOrder(t *testing.T) {
	q := New()
	require.NoError(t, q.Enqueue(core.Ticket{ID: "a"}))
	require.NoError(t, q.Enqueue(core.Ticket{ID: "b"}))
	require.NoError(t, q.Enqueue(core.Ticket{ID: "c"}))
	snap := q.Snapshot()
	got := []string{snap[0].ID, snap[1].ID, snap[2].ID}
	assert.Equal(t, []string{"a", "b", "c"}, got)
}

func TestDuplicate(t *testing.T) {
	q := New()
	require.NoError(t, q.Enqueue(core.Ticket{ID: "a"}))
	err := q.Enqueue(core.Ticket{ID: "a"})
	assert.True(t, errors.Is(err, ErrDuplicateTicket))
}

func TestCancel(t *testing.T) {
	q := New()
	_ = q.Enqueue(core.Ticket{ID: "a"})
	_ = q.Enqueue(core.Ticket{ID: "b"})
	require.NoError(t, q.Cancel("a"))
	assert.Equal(t, 1, q.Len())
	err := q.Cancel("nope")
	assert.True(t, errors.Is(err, ErrUnknownTicket))
}

func TestRemove(t *testing.T) {
	q := New()
	for _, id := range []string{"a", "b", "c", "d"} {
		_ = q.Enqueue(core.Ticket{ID: id})
	}
	q.Remove([]string{"b", "d"})
	snap := q.Snapshot()
	assert.Equal(t, []string{"a", "c"}, []string{snap[0].ID, snap[1].ID})
}

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
