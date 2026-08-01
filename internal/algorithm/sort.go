package algorithm

import (
	"cmp"
	"maps"
	"math"
	"slices"

	"github.com/moepig/flexi/internal/core"
	"github.com/moepig/flexi/internal/rule"
	"github.com/moepig/flexi/internal/ruleset"
)

// orderBatch returns the tickets reordered for match formation according to the
// rule set's batchingPreference and any absoluteSort/distanceSort rules.
//
//   - batchingPreference "sorted" pre-sorts the whole pool by sortByAttributes.
//   - batchingPreference "random"/"largestPopulation"/"fastestRegion" keep the
//     incoming (queue) order; flexi favours determinism so "random" is a no-op.
//   - absoluteSort/distanceSort rules then order the batch relative to the
//     oldest ticket (kept first as the match anchor).
func orderBatch(rs *ruleset.RuleSet, tickets []core.Ticket) []core.Ticket {
	out := slices.Clone(tickets)

	if rs.Algorithm.BatchingPreference == "sorted" && len(rs.Algorithm.SortByAttributes) > 0 {
		attrs := rs.Algorithm.SortByAttributes
		slices.SortStableFunc(out, func(a, b core.Ticket) int {
			return compareByAttrs(a, b, attrs)
		})
	}

	for i := range rs.Rules {
		r := &rs.Rules[i]
		if r.Type == ruleset.RuleAbsoluteSort || r.Type == ruleset.RuleDistanceSort {
			out = applySortRule(out, r)
		}
	}
	return out
}

// applySortRule reorders tickets[1:] by a sort rule, keeping tickets[0] as the
// anchor (oldest ticket the match is built around).
func applySortRule(tickets []core.Ticket, r *ruleset.Rule) []core.Ticket {
	if len(tickets) < 2 {
		return tickets
	}
	anchor := tickets[0]
	rest := slices.Clone(tickets[1:])
	agg := r.PartyAggregation
	anchorVal, _ := ticketScalar(anchor, r.SortAttribute, r.MapKey, agg)

	// Compute each ticket's sort key once up front and sort an index slice:
	// evaluating the key inside the comparison function would repeat the
	// attribute lookup O(n log n) times instead of O(n).
	keys := make([]float64, len(rest))
	idx := make([]int, len(rest))
	for i, t := range rest {
		idx[i] = i
		v, _ := ticketScalar(t, r.SortAttribute, r.MapKey, agg)
		if r.Type == ruleset.RuleDistanceSort {
			v = math.Abs(v - anchorVal)
		}
		keys[i] = v
	}

	asc := r.SortDirection == "ascending"
	slices.SortStableFunc(idx, func(a, b int) int {
		if asc {
			return cmp.Compare(keys[a], keys[b])
		}
		return cmp.Compare(keys[b], keys[a])
	})

	out := make([]core.Ticket, 0, len(tickets))
	out = append(out, anchor)
	for _, i := range idx {
		out = append(out, rest[i])
	}
	return out
}

// compareByAttrs orders two tickets by the given attributes in priority order.
// Numeric and map attributes compare by value (avg over the ticket's players);
// string attributes compare lexicographically.
func compareByAttrs(a, b core.Ticket, attrs []string) int {
	for _, attr := range attrs {
		// Try numeric/map first.
		an, aok := ticketScalar(a, attr, "", "avg")
		bn, bok := ticketScalar(b, attr, "", "avg")
		if aok || bok {
			if c := cmp.Compare(an, bn); c != 0 {
				return c
			}
			continue
		}
		if c := cmp.Compare(ticketString(a, attr), ticketString(b, attr)); c != 0 {
			return c
		}
	}
	return 0
}

// ticketScalar reduces a ticket's players to a single numeric value for attr,
// using partyAggregation agg ("min"|"max"|"avg", default avg). For map
// attributes mapKey selects "minValue" or "maxValue" within each player's map.
func ticketScalar(t core.Ticket, attr, mapKey, agg string) (float64, bool) {
	var vals []float64
	for _, p := range t.Players {
		a, ok := p.Attributes[attr]
		if !ok {
			continue
		}
		switch a.Kind {
		case core.AttrNumber:
			vals = append(vals, a.N)
		case core.AttrStringNumberMap:
			if v, ok := mapScalar(a.SDM, mapKey); ok {
				vals = append(vals, v)
			}
		}
	}
	if len(vals) == 0 {
		return 0, false
	}
	return rule.ReduceFloat(vals, agg), true
}

func ticketString(t core.Ticket, attr string) string {
	for _, p := range t.Players {
		if a, ok := p.Attributes[attr]; ok && a.Kind == core.AttrString {
			return a.S
		}
	}
	return ""
}

func mapScalar(m map[string]float64, mapKey string) (float64, bool) {
	if len(m) == 0 {
		return 0, false
	}
	agg := "min" // default / minValue
	if mapKey == "maxValue" {
		agg = "max"
	}
	return rule.ReduceFloat(slices.Collect(maps.Values(m)), agg), true
}
