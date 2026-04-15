# internal/core

Shared value types used by both the public `flexi` package and the other
`internal/*` packages.

This package exists purely to break an import cycle: the public `flexi`
package would otherwise need to import the `internal/algorithm`, `rule`, etc.
packages while those packages need access to `Player`, `Ticket`, `Match`,
and `Attribute`. Putting the types here lets `flexi` re-export them as type
aliases.

## Contents

- `AttributeKind`, `Attribute`, `Attributes` — tagged-union representation
  of a FlexMatch player attribute (`string` / `number` / `string_list` /
  `string_number_map`).
- `Player` — a participant in a `Ticket`, carrying attributes and per-region
  latencies.
- `Ticket` — a matchmaking request (one or more players) plus the time it
  was enqueued.
- `Match` — a successful pairing: team name → players, plus the IDs of the
  consumed tickets.
- `TicketStatus` and its constants (`StatusQueued`, `StatusSearching`,
  `StatusRequiresAcceptance`, `StatusPlacing`, `StatusCompleted`,
  `StatusFailed`, `StatusCancelled`, `StatusTimedOut`) — the FlexMatch
  ticket lifecycle values, re-exported via type alias by the public
  `flexi` package. Transitions and the acceptance state machine itself
  live in `flexi`, not here.

## Notes for contributors

- This package must stay dependency-free (no imports of other `internal/*`
  packages, no third-party libraries) so anyone can import it.
- New fields here become part of the public API by way of the type aliases
  in `flexi`. Treat additions as you would any other public API change.
- Constructors and helpers belong in the `flexi` package, not here. Keep
  this package to plain data types only.
