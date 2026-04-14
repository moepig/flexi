# flexi

[![Go Reference](https://pkg.go.dev/badge/github.com/moepig/flexi.svg)](https://pkg.go.dev/github.com/moepig/flexi)

`flexi` is an in-memory matchmaking library for Go that is compatible with Amazon GameLift FlexMatch rule sets.

It accepts the same JSON rule set you would pass to AWS's `CreateMatchmakingRuleSet` API and evaluates tickets locally — no GameLift hosting, no AWS connectivity, no networking. Drop the rule set in, enqueue tickets, drive the matchmaker, get matches.

## Features

- **AWS-compatible JSON**: feed the same rule set documented in the FlexMatch developer guide.
- **All seven rule kinds**: `comparison`, `distance`, `absoluteSort`, `batchDistance`, `collection`, `latency`, `compound`.
- **All four player attribute types**: `string`, `number`, `string_list`, `string_number_map`.
- **Algorithm strategies**: `exhaustiveSearch` and `balanced` (with `balancedAttribute`).
- **Expansions**: rule values loosen automatically as tickets wait.
- **Injectable clock**: tests advance time deterministically, no `time.Sleep`.
- **Zero external dependencies at runtime** (testify is test-only). No network or persistence.
- **Goroutine-safe**: producers may `Enqueue` / `Cancel` while another goroutine drives `Tick`.

> Backfill of in-progress matches is intentionally out of scope.

## Installation

```bash
go get github.com/moepig/flexi
```

Requires Go 1.22 or later.

## Quick start

```go
package main

import (
    "fmt"
    "github.com/moepig/flexi"
)

const ruleset = `{
  "name": "skill-balance",
  "ruleLanguageVersion": "1.0",
  "playerAttributes": [{"name": "skill", "type": "number"}],
  "teams": [
    {"name": "red",  "minPlayers": 2, "maxPlayers": 2},
    {"name": "blue", "minPlayers": 2, "maxPlayers": 2}
  ],
  "rules": [
    {"name": "FairSkill", "type": "distance",
     "measurements": ["avg(teams[red].players.skill)"],
     "referenceValue": "avg(teams[blue].players.skill)",
     "maxDistance": 10}
  ]
}`

func main() {
    mm, err := flexi.New([]byte(ruleset))
    if err != nil {
        panic(err)
    }

    for i, skill := range []float64{50, 52, 49, 51} {
        _ = mm.Enqueue(flexi.Ticket{
            ID: fmt.Sprintf("t%d", i),
            Players: []flexi.Player{{
                ID:         fmt.Sprintf("p%d", i),
                Attributes: flexi.Attributes{"skill": flexi.Number(skill)},
            }},
        })
    }

    matches, _ := mm.Tick()
    for _, m := range matches {
        fmt.Println("red:",  m.Teams["red"])
        fmt.Println("blue:", m.Teams["blue"])
    }
}
```

## Driving the matchmaker

`Matchmaker` does not start any goroutines or timers — the caller drives it by invoking `Tick()`. This keeps tests deterministic and lets you integrate with whatever scheduling, observability, and shutdown story you already have.

```go
ticker := time.NewTicker(time.Second)
defer ticker.Stop()

for range ticker.C {
    matches, err := mm.Tick()
    if err != nil {
        log.Printf("flexi tick: %v", err)
        continue
    }
    for _, m := range matches {
        dispatch(m) // hand off to your game server
    }
}
```

`Tick` returns every match that can be formed at this moment. Tickets consumed by a returned match are removed from the queue atomically; everything else stays queued for a future tick.

## Time and expansions

Anything time-dependent — most importantly the FlexMatch `expansions` block — reads the current time through a `Clock`. The default is the system clock; tests should pass `WithClock(NewFakeClock(...))` so they can advance time without sleeping.

```go
clock := flexi.NewFakeClock(time.Now())
mm, _ := flexi.New(rulesetJSON, flexi.WithClock(clock))

mm.Enqueue(ticket)
matches, _ := mm.Tick()           // before any expansion step

clock.Advance(60 * time.Second)
matches, _ = mm.Tick()            // expansion steps with waitTimeSeconds<=60 applied
```

## A richer rule set example

```json
{
  "name": "example",
  "ruleLanguageVersion": "1.0",
  "playerAttributes": [
    {"name": "skill", "type": "number"},
    {"name": "modes", "type": "string_list"}
  ],
  "algorithm": {
    "strategy": "balanced",
    "balancedAttribute": "skill"
  },
  "teams": [
    {"name": "team", "minPlayers": 3, "maxPlayers": 3, "quantity": 2}
  ],
  "rules": [
    {"name": "FairSkill", "type": "distance",
     "measurements": ["avg(teams[team_1].players.skill)"],
     "referenceValue": "avg(teams[team_2].players.skill)",
     "maxDistance": 10},
    {"name": "ModeOverlap", "type": "collection",
     "measurements": ["set_intersection(players.modes)"],
     "operation": "reference_intersection_count",
     "referenceValue": ["TDM", "CTF", "FFA"],
     "minCount": 1},
    {"name": "Ping", "type": "latency", "maxLatency": 150},
    {"name": "All", "type": "compound",
     "statement": {"condition": "and", "rules": ["FairSkill", "ModeOverlap", "Ping"]}}
  ],
  "expansions": [
    {"target": "rules[FairSkill].maxDistance",
     "steps": [
       {"waitTimeSeconds": 30, "value": 50},
       {"waitTimeSeconds": 60, "value": 200}
     ]}
  ]
}
```

## API at a glance

Full reference is on [pkg.go.dev](https://pkg.go.dev/github.com/moepig/flexi). The most-used surface:

| Symbol | Description |
| --- | --- |
| `flexi.New(rulesetJSON, opts...)` | Parse a rule set JSON document and return a `Matchmaker`. |
| `flexi.WithClock(c)` | Option that overrides the time source. |
| `Matchmaker.Enqueue(t)` | Add a ticket to the queue. |
| `Matchmaker.Cancel(id)` | Remove a queued ticket. |
| `Matchmaker.Tick()` | Form and return every match satisfiable right now. |
| `Matchmaker.Pending()` | Count of queued tickets. |
| `flexi.Number / String / StringList / StringNumberMap` | Constructors for the four `Attribute` variants. |
| `flexi.NewFakeClock(t)` | Test clock you can `Advance` or `Set`. |

## Testing

```bash
go test ./... -race
```

Each layer (rule set parser, expression evaluator, rule evaluators, expansions, algorithm, queue) has its own unit tests. End-to-end scenarios driven through the public API live in `examples_test.go`. Tests use [stretchr/testify](https://github.com/stretchr/testify).

## License

See `LICENSE` for license terms.
