package rule

import "github.com/moepig/flexi/internal/core"

// reduceFloat aggregates vals using agg ("min"|"max"|"avg"; default "avg").
func reduceFloat(vals []float64, agg string) float64 {
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
	default: // avg
		var s float64
		for _, v := range vals {
			s += v
		}
		return s / float64(len(vals))
	}
}

// aggregatePlayers reduces a party (one ticket's players) to a single synthetic
// player. Numeric attributes and per-region latencies are aggregated with agg;
// non-numeric attributes are taken from the first player. A single-player party
// is returned unchanged.
func aggregatePlayers(party []core.Player, agg string) core.Player {
	if len(party) == 1 {
		return party[0]
	}
	base := party[0]
	out := core.Player{ID: base.ID, Attributes: core.Attributes{}, Latencies: map[string]int{}}
	for name, a := range base.Attributes {
		if a.Kind == core.AttrNumber {
			vals := make([]float64, 0, len(party))
			for _, p := range party {
				if pa, ok := p.Attributes[name]; ok && pa.Kind == core.AttrNumber {
					vals = append(vals, pa.N)
				}
			}
			out.Attributes[name] = core.Attribute{Kind: core.AttrNumber, N: reduceFloat(vals, agg)}
		} else {
			out.Attributes[name] = a
		}
	}
	regions := map[string][]float64{}
	for _, p := range party {
		for r, v := range p.Latencies {
			regions[r] = append(regions[r], float64(v))
		}
	}
	for r, vs := range regions {
		out.Latencies[r] = int(reduceFloat(vs, agg))
	}
	return out
}

// aggregateCandidateWith returns a copy of c in which every team's parties are
// each collapsed to a single synthetic player via reduce. When c carries no
// party grouping the original candidate is returned unchanged.
func aggregateCandidateWith(c *Candidate, reduce func([]core.Player) core.Player) *Candidate {
	if len(c.TeamParties) == 0 {
		return c
	}
	nc := &Candidate{
		Teams:     make(map[string][]core.Player, len(c.TeamParties)),
		TeamOrder: c.TeamOrder,
		Region:    c.Region,
	}
	for name, parties := range c.TeamParties {
		syn := make([]core.Player, 0, len(parties))
		for _, party := range parties {
			syn = append(syn, reduce(party))
		}
		nc.Teams[name] = syn
	}
	for _, name := range c.TeamOrder {
		nc.Players = append(nc.Players, nc.Teams[name]...)
	}
	return nc
}

// aggregateCandidate collapses parties using numeric min/max/avg aggregation.
func aggregateCandidate(c *Candidate, agg string) *Candidate {
	if agg == "" {
		agg = "avg"
	}
	return aggregateCandidateWith(c, func(party []core.Player) core.Player {
		return aggregatePlayers(party, agg)
	})
}

// aggregatePartySets reduces a party to one synthetic player whose string_list
// attributes are the union or intersection of the party members' lists. Numeric
// and other attributes are taken from the first player.
func aggregatePartySets(party []core.Player, mode string) core.Player {
	if len(party) == 1 {
		return party[0]
	}
	base := party[0]
	out := core.Player{ID: base.ID, Attributes: core.Attributes{}, Latencies: base.Latencies}
	for name, a := range base.Attributes {
		if a.Kind != core.AttrStringList {
			out.Attributes[name] = a
			continue
		}
		out.Attributes[name] = core.Attribute{Kind: core.AttrStringList, SL: reduceSets(party, name, mode)}
	}
	return out
}

func reduceSets(party []core.Player, attr, mode string) []string {
	var acc []string
	first := true
	counts := map[string]int{}
	order := []string{}
	for _, p := range party {
		a, ok := p.Attributes[attr]
		if !ok || a.Kind != core.AttrStringList {
			continue
		}
		seen := map[string]struct{}{}
		for _, s := range a.SL {
			if _, dup := seen[s]; dup {
				continue
			}
			seen[s] = struct{}{}
			if _, known := counts[s]; !known {
				order = append(order, s)
			}
			counts[s]++
		}
		if first {
			acc = append(acc, a.SL...)
			first = false
		}
	}
	if mode == "intersection" {
		out := []string{}
		for _, s := range order {
			if counts[s] == len(party) {
				out = append(out, s)
			}
		}
		return out
	}
	// union: distinct values in first-seen order
	return order
}
