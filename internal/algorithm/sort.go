package algorithm

import (
	"sort"

	"github.com/moepig/flexi/internal/core"
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
	out := append([]core.Ticket(nil), tickets...)

	if rs.Algorithm.BatchingPreference == "sorted" && len(rs.Algorithm.SortByAttributes) > 0 {
		attrs := rs.Algorithm.SortByAttributes
		sort.SliceStable(out, func(i, j int) bool {
			return lessByAttrs(out[i], out[j], attrs)
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
	rest := append([]core.Ticket(nil), tickets[1:]...)
	agg := r.PartyAggregation
	anchorVal, _ := ticketScalar(anchor, r.SortAttribute, r.MapKey, agg)
	keyOf := func(t core.Ticket) float64 {
		v, _ := ticketScalar(t, r.SortAttribute, r.MapKey, agg)
		if r.Type == ruleset.RuleDistanceSort {
			d := v - anchorVal
			if d < 0 {
				d = -d
			}
			return d
		}
		return v
	}
	asc := r.SortDirection == "ascending"
	sort.SliceStable(rest, func(i, j int) bool {
		if asc {
			return keyOf(rest[i]) < keyOf(rest[j])
		}
		return keyOf(rest[i]) > keyOf(rest[j])
	})
	return append([]core.Ticket{anchor}, rest...)
}

// lessByAttrs orders two tickets by the given attributes in priority order.
// Numeric and map attributes compare by value (avg over the ticket's players);
// string attributes compare lexicographically.
func lessByAttrs(a, b core.Ticket, attrs []string) bool {
	for _, attr := range attrs {
		// Try numeric/map first.
		an, aok := ticketScalar(a, attr, "", "avg")
		bn, bok := ticketScalar(b, attr, "", "avg")
		if aok || bok {
			if an != bn {
				return an < bn
			}
			continue
		}
		as := ticketString(a, attr)
		bs := ticketString(b, attr)
		if as != bs {
			return as < bs
		}
	}
	return false
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
	return aggFloat(vals, agg), true
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
	first := true
	var best float64
	for _, v := range m {
		if first {
			best, first = v, false
			continue
		}
		if mapKey == "maxValue" {
			if v > best {
				best = v
			}
		} else { // default / minValue
			if v < best {
				best = v
			}
		}
	}
	return best, true
}

func aggFloat(vals []float64, agg string) float64 {
	if len(vals) == 0 {
		return 0
	}
	switch agg {
	case "min":
		m := vals[0]
		for _, v := range vals[1:] {
			if v < m {
				m = v
			}
		}
		return m
	case "max":
		m := vals[0]
		for _, v := range vals[1:] {
			if v > m {
				m = v
			}
		}
		return m
	default:
		var s float64
		for _, v := range vals {
			s += v
		}
		return s / float64(len(vals))
	}
}
