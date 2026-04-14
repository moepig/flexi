# internal/queue

Goroutine-safe in-memory ticket store.

## Responsibility

Hold pending tickets in insertion order so the matchmaker can take a stable
snapshot, attempt matches against it, and remove only the tickets actually
consumed. Cancellation by external producers happens through the same
store.

## Contents

- `Queue` — the store. Construct with `New()`.
- `Enqueue(t core.Ticket) error` — append, rejecting duplicate IDs with
  `ErrDuplicateTicket`.
- `Cancel(id string) error` — remove by ID, returning `ErrUnknownTicket`
  if the ID isn't present.
- `Remove(ids []string)` — bulk-delete used by the matchmaker after
  forming a match. Unknown IDs are ignored.
- `Snapshot() []core.Ticket` — copy of the queue contents in insertion
  order; safe to mutate by the caller.
- `Len() int` — number of pending tickets.

## Design notes

- All mutating methods take a single mutex. The expected workload is
  low-contention (a handful of producers, one Tick goroutine), so a
  simpler design beats anything fancier here.
- `Snapshot` allocates a fresh slice every call so the matchmaker can
  iterate without holding the lock — important because rule evaluation
  during a tick is the slow part.
- This package is deliberately scope-limited. Persistence, expiry,
  prioritisation, and TTLs all belong elsewhere: the matchmaker layer
  decides when a ticket is "too old", not the queue.
