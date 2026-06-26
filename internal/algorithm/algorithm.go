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
	Teams                 map[string][]core.Player
	TicketIDs             []string
	Region                string
	RuleEvaluationMetrics []core.RuleMetric
}

// Build forms as many matches as possible from the given tickets, returning
// each formed match, the remaining tickets in queue order, and the per-ticket
// rule-evaluation metrics accumulated during this call.
//
// Every match-formation search evaluates each rule against the candidate it
// builds; the resulting pass/fail tallies are attributed to all tickets that
// were still in the queue at the time of that search (not only the ones that
// ended up in the match), so timed-out and cancelled tickets carry the metrics
// of every search they participated in.
func Build(rs *ruleset.RuleSet, evals []rule.Evaluator, tickets []core.Ticket) ([]Result, []core.Ticket, map[string][]core.RuleMetric) {
	remaining := append([]core.Ticket(nil), tickets...)
	var out []Result
	perTicket := make(map[string][]core.RuleMetric)
	for {
		res, used, searchMetrics, ok := formOne(rs, evals, remaining)
		for _, t := range remaining {
			perTicket[t.ID] = mergeMetrics(perTicket[t.ID], searchMetrics)
		}
		if !ok {
			break
		}
		out = append(out, res)
		remaining = removeTickets(remaining, used)
	}
	return out, remaining, perTicket
}

// metricsCollector tallies per-rule pass/fail counts over a single match
// formation search. Rule names are recorded in evaluator order (the rule set's
// rules order) so snapshots and merges remain deterministic.
type metricsCollector struct {
	order  []string
	passed map[string]int
	failed map[string]int
}

func newMetricsCollector(evals []rule.Evaluator) *metricsCollector {
	mc := &metricsCollector{
		order:  make([]string, 0, len(evals)),
		passed: make(map[string]int, len(evals)),
		failed: make(map[string]int, len(evals)),
	}
	for _, e := range evals {
		mc.order = append(mc.order, e.Name())
	}
	return mc
}

// snapshot returns the tallies in evaluator order, including rules with zero
// counts, so the output shape is stable across searches.
func (mc *metricsCollector) snapshot() []core.RuleMetric {
	out := make([]core.RuleMetric, 0, len(mc.order))
	for _, name := range mc.order {
		out = append(out, core.RuleMetric{
			RuleName:    name,
			PassedCount: mc.passed[name],
			FailedCount: mc.failed[name],
		})
	}
	return out
}

// mergeMetrics adds src's counts into dst by rule name, preserving dst's
// existing order and appending any names not yet present. dst may be nil.
func mergeMetrics(dst, src []core.RuleMetric) []core.RuleMetric {
	if len(src) == 0 {
		return dst
	}
	idx := make(map[string]int, len(dst))
	for i, m := range dst {
		idx[m.RuleName] = i
	}
	for _, s := range src {
		if i, ok := idx[s.RuleName]; ok {
			dst[i].PassedCount += s.PassedCount
			dst[i].FailedCount += s.FailedCount
			continue
		}
		idx[s.RuleName] = len(dst)
		dst = append(dst, s)
	}
	return dst
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
	Parties    [][]core.Player // each sub-slice = one ticket's players
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

// formOne attempts to build exactly one match from the head of tickets. It
// always returns the rule-evaluation metrics it accumulated during the search,
// whether or not a match was formed, so callers can attribute them to the
// tickets that participated.
func formOne(rs *ruleset.RuleSet, evals []rule.Evaluator, tickets []core.Ticket) (Result, map[string]struct{}, []core.RuleMetric, bool) {
	mc := newMetricsCollector(evals)
	if len(tickets) == 0 {
		return Result{}, nil, mc.snapshot(), false
	}
	slots := expandTeams(rs)
	if len(slots) == 0 {
		return Result{}, nil, mc.snapshot(), false
	}
	balancedAttr := ""
	if rs.Algorithm.Strategy == "balanced" {
		balancedAttr = rs.Algorithm.BalancedAttribute
	}

	// When the balanced strategy is active, sort tickets by the balanced
	// attribute descending so the greedy "place into lowest-sum team" loop
	// produces an even split. Otherwise apply batchingPreference pre-sorting and
	// any absoluteSort/distanceSort rules to order the batch.
	if balancedAttr != "" && len(tickets) > 1 {
		ordered := append([]core.Ticket(nil), tickets...)
		sort.SliceStable(ordered, func(i, j int) bool {
			return partyAttrSum(ordered[i], balancedAttr) > partyAttrSum(ordered[j], balancedAttr)
		})
		tickets = ordered
	} else if len(tickets) > 1 {
		tickets = orderBatch(rs, tickets)
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
			slots[idx].Parties = append(slots[idx].Parties, t.Players)
			if rulesPassAndRecord(evals, slots, mc) {
				used[t.ID] = struct{}{}
				placed = true
				break
			}
			slots[idx].Players = slots[idx].Players[:len(slots[idx].Players)-len(t.Players)]
			slots[idx].Parties = slots[idx].Parties[:len(slots[idx].Parties)-1]
		}
		if !placed && len(used) == 0 {
			return Result{}, nil, mc.snapshot(), false
		}
		if allFull(slots) {
			break
		}
	}

	if !allMinSatisfied(slots) {
		return Result{}, nil, mc.snapshot(), false
	}
	if !rulesPassAndRecord(evals, slots, mc) {
		return Result{}, nil, mc.snapshot(), false
	}

	out := Result{Teams: make(map[string][]core.Player, len(slots)), Region: sharedRegion(slots)}
	for _, s := range slots {
		out.Teams[s.Name] = s.Players
	}
	for id := range used {
		out.TicketIDs = append(out.TicketIDs, id)
	}
	sort.Strings(out.TicketIDs)
	out.RuleEvaluationMetrics = mc.snapshot()
	return out, used, out.RuleEvaluationMetrics, true
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

// rulesPassAndRecord evaluates every rule against the candidate built from
// slots, recording each pass/fail into mc, and reports whether the candidate is
// admissible (all rules passed). Unlike a short-circuiting check it always
// evaluates every rule so that each rule's failedCount is complete; the
// returned bool still matches "all rules passed", so match correctness is
// unchanged.
func rulesPassAndRecord(evals []rule.Evaluator, slots []teamSlot, mc *metricsCollector) bool {
	// Region is left empty so latency rules pick any satisfying region.
	cand := buildCandidate(slots, "")
	allOK := true
	for _, e := range evals {
		ok, err := e.Evaluate(cand)
		if err == nil && ok {
			mc.passed[e.Name()]++
		} else {
			mc.failed[e.Name()]++
			allOK = false
		}
	}
	return allOK
}

func buildCandidate(slots []teamSlot, region string) *rule.Candidate {
	all := []core.Player{}
	teams := map[string][]core.Player{}
	teamParties := map[string][][]core.Player{}
	order := make([]string, 0, len(slots))
	var parties [][]core.Player
	for _, s := range slots {
		all = append(all, s.Players...)
		teams[s.Name] = append(teams[s.Name], s.Players...)
		teamParties[s.Name] = append(teamParties[s.Name], s.Parties...)
		// also expose under base name for teams[<base>] expressions (only when
		// quantity expansion produced a distinct slot name, to avoid doubling).
		if s.BaseName != s.Name {
			teams[s.BaseName] = append(teams[s.BaseName], s.Players...)
			teamParties[s.BaseName] = append(teamParties[s.BaseName], s.Parties...)
		}
		order = append(order, s.Name)
		parties = append(parties, s.Parties...)
	}
	return &rule.Candidate{
		Players:     all,
		Teams:       teams,
		TeamOrder:   order,
		Parties:     parties,
		TeamParties: teamParties,
		Region:      region,
	}
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
