package rule

import (
	"fmt"

	"github.com/moepig/flexi/internal/core"
	"github.com/moepig/flexi/internal/ruleset"
)

type batchDistance struct {
	name             string
	attr             string
	maxDist          *float64
	minDist          *float64
	partyAggregation string // "min", "max", "avg" (default "avg")
}

func buildBatchDistance(r *ruleset.Rule) (Evaluator, error) {
	agg := r.PartyAggregation
	if agg == "" {
		agg = "avg"
	}
	return &batchDistance{
		name:             r.Name,
		attr:             r.BatchAttribute,
		maxDist:          r.MaxDistance,
		minDist:          r.MinDistance,
		partyAggregation: agg,
	}, nil
}

func (b *batchDistance) Name() string { return b.name }

func (b *batchDistance) Evaluate(c *Candidate) (bool, error) {
	if b.maxDist == nil && b.minDist == nil {
		return true, nil
	}

	// String attributes are batched by value equivalency rather than numeric
	// spread: the distance is the count of distinct values present in the match
	// minus one (so maxDistance=0 requires every player to share one value).
	if b.isStringMode(c.Players) {
		return b.evaluateString(c.Players)
	}

	parties := c.Parties
	if len(parties) == 0 {
		// fallback: treat each player as its own party
		parties = make([][]core.Player, len(c.Players))
		for i, p := range c.Players {
			parties[i] = []core.Player{p}
		}
	}
	if len(parties) < 2 {
		return true, nil
	}

	values := make([]float64, 0, len(parties))
	for _, party := range parties {
		v, err := b.aggregateParty(party)
		if err != nil {
			return false, err
		}
		values = append(values, v)
	}

	min, max := values[0], values[0]
	for _, v := range values[1:] {
		if v < min {
			min = v
		}
		if v > max {
			max = v
		}
	}
	spread := max - min

	if b.maxDist != nil && spread > *b.maxDist {
		return false, nil
	}
	if b.minDist != nil && spread < *b.minDist {
		return false, nil
	}
	return true, nil
}

// isStringMode reports whether the batch attribute is carried as a string by
// the players, in which case batching uses value equivalency. The kind is read
// from the first player that declares the attribute.
func (b *batchDistance) isStringMode(players []core.Player) bool {
	for _, p := range players {
		if a, ok := p.Attributes[b.attr]; ok {
			return a.Kind == core.AttrString
		}
	}
	return false
}

// evaluateString admits a match when the number of distinct string values for
// the batch attribute, minus one, stays within the configured bounds.
func (b *batchDistance) evaluateString(players []core.Player) (bool, error) {
	seen := make(map[string]struct{})
	for _, p := range players {
		a, ok := p.Attributes[b.attr]
		if !ok || a.Kind != core.AttrString {
			return false, fmt.Errorf("batchDistance %q: player %q lacks string attr %q", b.name, p.ID, b.attr)
		}
		seen[a.S] = struct{}{}
	}
	if len(seen) == 0 {
		return true, nil
	}
	distance := float64(len(seen) - 1)
	if b.maxDist != nil && distance > *b.maxDist {
		return false, nil
	}
	if b.minDist != nil && distance < *b.minDist {
		return false, nil
	}
	return true, nil
}

func (b *batchDistance) aggregateParty(players []core.Player) (float64, error) {
	if len(players) == 0 {
		return 0, fmt.Errorf("batchDistance %q: empty party", b.name)
	}
	vals := make([]float64, 0, len(players))
	for _, p := range players {
		a, ok := p.Attributes[b.attr]
		if !ok || a.Kind != core.AttrNumber {
			return 0, fmt.Errorf("batchDistance %q: player %q lacks numeric attr %q", b.name, p.ID, b.attr)
		}
		vals = append(vals, a.N)
	}
	switch b.partyAggregation {
	case "min":
		v := vals[0]
		for _, x := range vals[1:] {
			if x < v {
				v = x
			}
		}
		return v, nil
	case "max":
		v := vals[0]
		for _, x := range vals[1:] {
			if x > v {
				v = x
			}
		}
		return v, nil
	default: // "avg"
		var sum float64
		for _, x := range vals {
			sum += x
		}
		return sum / float64(len(vals)), nil
	}
}
