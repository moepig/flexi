// Package queue is the in-memory ticket store used by the matchmaker. It
// preserves insertion order and is safe for concurrent Enqueue / Cancel /
// Snapshot calls.
package queue

import (
	"errors"
	"sync"

	"github.com/moepig/flexi/internal/core"
)

// ErrDuplicateTicket is returned by Enqueue when a ticket with the same ID
// is already in the queue.
var ErrDuplicateTicket = errors.New("queue: duplicate ticket id")

// ErrUnknownTicket is returned by Cancel when the ticket id is not present.
var ErrUnknownTicket = errors.New("queue: unknown ticket id")

// Queue is a goroutine-safe ordered ticket store.
type Queue struct {
	mu    sync.Mutex
	order []string
	items map[string]core.Ticket
}

// New returns an empty Queue.
func New() *Queue {
	return &Queue{items: make(map[string]core.Ticket)}
}

// Enqueue adds t. ErrDuplicateTicket is returned if the id is already known.
func (q *Queue) Enqueue(t core.Ticket) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	if _, dup := q.items[t.ID]; dup {
		return ErrDuplicateTicket
	}
	q.items[t.ID] = t
	q.order = append(q.order, t.ID)
	return nil
}

// Cancel removes the ticket with id. ErrUnknownTicket is returned if absent.
func (q *Queue) Cancel(id string) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	if _, ok := q.items[id]; !ok {
		return ErrUnknownTicket
	}
	delete(q.items, id)
	for i, x := range q.order {
		if x == id {
			q.order = append(q.order[:i], q.order[i+1:]...)
			break
		}
	}
	return nil
}

// Remove deletes a set of ids (used by the matchmaker after forming a match).
// Unknown ids are silently ignored.
func (q *Queue) Remove(ids []string) {
	if len(ids) == 0 {
		return
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	gone := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		if _, ok := q.items[id]; ok {
			delete(q.items, id)
			gone[id] = struct{}{}
		}
	}
	if len(gone) == 0 {
		return
	}
	out := q.order[:0]
	for _, id := range q.order {
		if _, drop := gone[id]; !drop {
			out = append(out, id)
		}
	}
	q.order = out
}

// Snapshot returns the tickets in enqueue order. The returned slice is a copy
// safe for the caller to mutate.
func (q *Queue) Snapshot() []core.Ticket {
	q.mu.Lock()
	defer q.mu.Unlock()
	out := make([]core.Ticket, 0, len(q.order))
	for _, id := range q.order {
		out = append(out, q.items[id])
	}
	return out
}

// Len returns the number of pending tickets.
func (q *Queue) Len() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.order)
}
