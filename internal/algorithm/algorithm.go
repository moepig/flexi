// Package algorithm builds matches from a queue of tickets according to a
// FlexMatch rule set's algorithm settings.
package algorithm

import (
	"sort"

	"github.com/moepig/flexi/internal/core"
	"github.com/moepig/flexi/internal/rule"
	"github.com/moepig/flexi/internal/ruleset"
)

// Result is one formed match.
type Result struct {
	Teams     map[string][]core.Player
	TicketIDs []string
	Region    string
}

// Build forms as many matches as possible from the given tickets, returning
// each formed match and the remaining tickets in queue order.
func Build(rs *ruleset.RuleSet, evals []rule.Evaluator, tickets []core.Ticket) ([]Result, []core.Ticket) {
	remaining := append([]core.Ticket(nil), tickets...)
	var out []Result
	for {
		res, used, ok := formOne(rs, evals, remaining)
		if !ok {
			break
		}
		out = append(out, res)
		remaining = removeTickets(remaining, used)
	}
	return out, remaining
}

func removeTickets(in []core.Ticket, used map[string]struct{}) []core.Ticket {
	out := in[:0]
	for _, t := range in {
		if _, drop := used[t.ID]; !drop {
			out = append(out, t)
		}
	}
	return out
}

// teamSlot is a concrete team instance after quantity expansion.
type teamSlot struct {
	Name       string
	BaseName   string
	MinPlayers int
	MaxPlayers int
	Players    []core.Player
}

func expandTeams(rs *ruleset.RuleSet) []teamSlot {
	var slots []teamSlot
	for _, t := range rs.Teams {
		q := t.Quantity
		if q <= 0 {
			q = 1
		}
		for i := 0; i < q; i++ {
			name := t.Name
			if q > 1 {
				name = t.Name + "_" + itoa(i+1)
			}
			slots = append(slots, teamSlot{
				Name:       name,
				BaseName:   t.Name,
				MinPlayers: t.MinPlayers,
				MaxPlayers: t.MaxPlayers,
			})
		}
	}
	return slots
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	digits := []byte{}
	neg := i < 0
	if neg {
		i = -i
	}
	for i > 0 {
		digits = append([]byte{byte('0' + i%10)}, digits...)
		i /= 10
	}
	if neg {
		digits = append([]byte{'-'}, digits...)
	}
	return string(digits)
}

// formOne attempts to build exactly one match from the head of tickets.
func formOne(rs *ruleset.RuleSet, evals []rule.Evaluator, tickets []core.Ticket) (Result, map[string]struct{}, bool) {
	if len(tickets) == 0 {
		return Result{}, nil, false
	}
	slots := expandTeams(rs)
	if len(slots) == 0 {
		return Result{}, nil, false
	}
	balancedAttr := ""
	if rs.Algorithm.Strategy == "balanced" {
		balancedAttr = rs.Algorithm.BalancedAttribute
	}

	// When the balanced strategy is active, sort tickets by the balanced
	// attribute descending so the greedy "place into lowest-sum team" loop
	// produces an even split.
	if balancedAttr != "" && len(tickets) > 1 {
		ordered := append([]core.Ticket(nil), tickets...)
		sort.SliceStable(ordered, func(i, j int) bool {
			return partyAttrSum(ordered[i], balancedAttr) > partyAttrSum(ordered[j], balancedAttr)
		})
		tickets = ordered
	}

	used := map[string]struct{}{}
	for _, t := range tickets {
		// Try every team in priority order; pick the first one that accepts the
		// whole party while keeping the ruleset satisfied.
		order := teamOrder(slots, t, balancedAttr)
		placed := false
		for _, idx := range order {
			if !canAdd(slots[idx], t) {
				continue
			}
			slots[idx].Players = append(slots[idx].Players, t.Players...)
			if rulesPass(rs, evals, slots) {
				used[t.ID] = struct{}{}
				placed = true
				break
			}
			slots[idx].Players = slots[idx].Players[:len(slots[idx].Players)-len(t.Players)]
		}
		if !placed && len(used) == 0 {
			return Result{}, nil, false
		}
		if allFull(slots) {
			break
		}
	}

	if !allMinSatisfied(slots) {
		return Result{}, nil, false
	}
	if !rulesPass(rs, evals, slots) {
		return Result{}, nil, false
	}

	out := Result{Teams: make(map[string][]core.Player, len(slots)), Region: sharedRegion(slots)}
	for _, s := range slots {
		out.Teams[s.Name] = s.Players
	}
	for id := range used {
		out.TicketIDs = append(out.TicketIDs, id)
	}
	sort.Strings(out.TicketIDs)
	return out, used, true
}

func partyAttrSum(t core.Ticket, attr string) float64 {
	var s float64
	for _, p := range t.Players {
		if a, ok := p.Attributes[attr]; ok {
			s += a.N
		}
	}
	return s
}

func canAdd(s teamSlot, t core.Ticket) bool {
	return len(s.Players)+len(t.Players) <= s.MaxPlayers
}

func allFull(slots []teamSlot) bool {
	for _, s := range slots {
		if len(s.Players) < s.MaxPlayers {
			return false
		}
	}
	return true
}

func allMinSatisfied(slots []teamSlot) bool {
	for _, s := range slots {
		if len(s.Players) < s.MinPlayers {
			return false
		}
	}
	return true
}

func sharedRegion(slots []teamSlot) string {
	regions := map[string]int{}
	totalPlayers := 0
	for _, s := range slots {
		for _, p := range s.Players {
			totalPlayers++
			for r := range p.Latencies {
				regions[r]++
			}
		}
	}
	if totalPlayers == 0 {
		return ""
	}
	best := ""
	bestCount := 0
	for r, c := range regions {
		if c == totalPlayers && c > bestCount {
			best, bestCount = r, c
		}
	}
	return best
}

func rulesPass(rs *ruleset.RuleSet, evals []rule.Evaluator, slots []teamSlot) bool {
	// Region is left empty so latency rules pick any satisfying region.
	cand := buildCandidate(slots, "")
	for _, e := range evals {
		ok, err := e.Evaluate(cand)
		if err != nil || !ok {
			return false
		}
	}
	return true
}

func buildCandidate(slots []teamSlot, region string) *rule.Candidate {
	all := []core.Player{}
	teams := map[string][]core.Player{}
	for _, s := range slots {
		all = append(all, s.Players...)
		teams[s.Name] = append([]core.Player(nil), s.Players...)
		// also expose under base name for teams[<base>] expressions
		teams[s.BaseName] = append(teams[s.BaseName], s.Players...)
	}
	return &rule.Candidate{Players: all, Teams: teams, Region: region}
}

// teamOrder returns slot indices in the order the algorithm should try when
// placing t. Order depends on strategy / batchingPreference.
func teamOrder(slots []teamSlot, t core.Ticket, balancedAttr string) []int {
	idx := make([]int, len(slots))
	for i := range idx {
		idx[i] = i
	}
	if balancedAttr != "" {
		// For balanced strategy: prefer the team with lowest current sum of
		// the balanced attribute, so the new party offsets it.
		sums := make([]float64, len(slots))
		for i, s := range slots {
			for _, p := range s.Players {
				if a, ok := p.Attributes[balancedAttr]; ok {
					sums[i] += a.N
				}
			}
		}
		sort.SliceStable(idx, func(a, b int) bool { return sums[idx[a]] < sums[idx[b]] })
		return idx
	}
	// default: prefer least-full team to spread players evenly
	sort.SliceStable(idx, func(a, b int) bool {
		return len(slots[idx[a]].Players) < len(slots[idx[b]].Players)
	})
	return idx
}
