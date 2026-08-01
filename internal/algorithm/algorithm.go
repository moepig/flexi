// Package algorithm builds matches from a queue of tickets according to a
// FlexMatch rule set's algorithm settings.
package algorithm

import (
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"

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
//
// Tickets marked Backfill carry the roster of a match already in progress. A
// search may seat at most one of them — FlexMatch never matches two backfill
// tickets together — and only emits the result if at least one regular ticket
// joined, since a backfill that admits nobody new is not a match. Which
// backfill tickets a search reaches for, and in what order, is decided by
// algorithm.backfillPriority; see backfillAttempts.
func Build(rs *ruleset.RuleSet, evals []rule.Evaluator, tickets []core.Ticket) ([]Result, []core.Ticket, map[string][]core.RuleMetric) {
	remaining := slices.Clone(tickets)
	reqs := buildTeamReqs(rs)
	var out []Result
	perTicket := make(map[string][]core.RuleMetric)
	for {
		res, used, searchMetrics, ok := formNext(rs, evals, reqs, remaining)
		for _, t := range remaining {
			perTicket[t.ID] = MergeMetrics(perTicket[t.ID], searchMetrics)
		}
		if !ok {
			break
		}
		res.RuleEvaluationMetrics = searchMetrics
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

// MergeMetrics adds src's counts into dst by rule name, preserving dst's
// existing order and appending any names not yet present. dst may be nil, in
// which case a freshly allocated slice is returned; src is never modified.
func MergeMetrics(dst, src []core.RuleMetric) []core.RuleMetric {
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
		for i := range q {
			name := t.Name
			if q > 1 {
				name = t.Name + "_" + strconv.Itoa(i+1)
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

// formNext attempts one match formation, honouring algorithm.backfillPriority
// to decide whether — and in which order — the pool's backfill tickets are
// offered to formOne as the roster to seat the match around. It returns the
// metrics of every attempt it made merged together: from the queue's point of
// view they are all one search.
func formNext(rs *ruleset.RuleSet, evals []rule.Evaluator, reqs map[string]teamReq, tickets []core.Ticket) (Result, map[string]struct{}, []core.RuleMetric, bool) {
	var metrics []core.RuleMetric
	for i, bf := range backfillAttempts(rs, tickets) {
		res, used, m, ok := formOne(rs, evals, reqs, tickets, bf)
		if i == 0 {
			// Adopt the first attempt's snapshot rather than merging into nil, so
			// a search that makes a single attempt reports exactly what formOne
			// produced — including the empty-but-not-nil slice a rule set with no
			// rules yields, which MergeMetrics would flatten to nil.
			metrics = m
		} else {
			metrics = MergeMetrics(metrics, m)
		}
		if ok {
			return res, used, metrics, true
		}
	}
	return Result{}, nil, metrics, false
}

// backfillAttempts returns the backfill tickets a single search should try to
// seat a match around, in the order algorithm.backfillPriority calls for. A nil
// entry is the plain attempt that forms a new match from regular tickets alone,
// and is the only entry when the pool holds no backfill ticket.
//
//   - "high" offers every backfill ticket, oldest first, before falling back to
//     a new match, so new players are steered into games already in progress.
//   - "low" only reaches for a backfill ticket once no new match can be formed,
//     making backfill the last resort.
//   - "normal" (the default) gives backfill tickets no special standing: one is
//     offered only when it is the oldest ticket in the pool, and therefore the
//     ticket the search would have been built around in any case.
//
// The balanced strategy is always treated as "normal". FlexMatch documents
// backfillPriority as "only used when pre-sorting with the exhaustive search
// strategy", so a priority declared alongside balanced is carried but ignored
// rather than rejected — the same treatment sortByAttributes gets.
func backfillAttempts(rs *ruleset.RuleSet, tickets []core.Ticket) []*core.Ticket {
	var bf []*core.Ticket
	for i := range tickets {
		if tickets[i].Backfill {
			bf = append(bf, &tickets[i])
		}
	}
	if len(bf) == 0 {
		return []*core.Ticket{nil}
	}
	priority := rs.Algorithm.BackfillPriority
	if rs.Algorithm.Strategy == "balanced" {
		priority = "normal"
	}
	switch priority {
	case "high":
		return append(bf, nil)
	case "low":
		return append([]*core.Ticket{nil}, bf...)
	default:
		if tickets[0].Backfill {
			return []*core.Ticket{&tickets[0], nil}
		}
		return []*core.Ticket{nil}
	}
}

// formOne attempts to build exactly one match from the head of tickets. It
// always returns the rule-evaluation metrics it accumulated during the search,
// whether or not a match was formed, so callers can attribute them to the
// tickets that participated. That third return value is the sole source of
// metrics: the returned Result leaves RuleEvaluationMetrics unset for Build to
// fill in, so there is only ever one snapshot per search.
//
// backfill, when non-nil, is a backfill ticket whose players are seated on the
// teams they already occupy before the search starts; the greedy loop then only
// fills what is left over. Backfill tickets in tickets are never placed by that
// loop, so a match holds at most the one backfill ticket named here.
func formOne(rs *ruleset.RuleSet, evals []rule.Evaluator, reqs map[string]teamReq, tickets []core.Ticket, backfill *core.Ticket) (Result, map[string]struct{}, []core.RuleMetric, bool) {
	mc := newMetricsCollector(evals)
	if len(tickets) == 0 {
		return Result{}, nil, mc.snapshot(), false
	}
	slots := expandTeams(rs)
	if len(slots) == 0 {
		return Result{}, nil, mc.snapshot(), false
	}
	// Only regular tickets are candidates for placement; the backfill ticket (if
	// any) is seated separately below, and a second one may not join it.
	regular := make([]core.Ticket, 0, len(tickets))
	for _, t := range tickets {
		if !t.Backfill {
			regular = append(regular, t)
		}
	}
	if len(regular) == 0 {
		return Result{}, nil, mc.snapshot(), false
	}

	used := map[string]struct{}{}
	if backfill != nil {
		if !seedBackfill(slots, *backfill) {
			return Result{}, nil, mc.snapshot(), false
		}
		used[backfill.ID] = struct{}{}
	}

	balancedAttr := ""
	if rs.Algorithm.Strategy == "balanced" {
		balancedAttr = rs.Algorithm.BalancedAttribute
	}

	// When the balanced strategy is active, sort tickets by the balanced
	// attribute descending so the greedy "place into lowest-sum team" loop
	// produces an even split. Otherwise apply batchingPreference pre-sorting and
	// any absoluteSort/distanceSort rules to order the batch.
	if balancedAttr != "" && len(regular) > 1 {
		slices.SortStableFunc(regular, func(a, b core.Ticket) int {
			// descending: the larger sum sorts first
			return cmp.Compare(partyAttrSum(b, balancedAttr), partyAttrSum(a, balancedAttr))
		})
	} else if len(regular) > 1 {
		regular = orderBatch(rs, regular)
	}

	for _, t := range regular {
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
			if rulesPass(evals, slots, reqs, mc) {
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

	// A backfill match has to admit somebody: seating the roster that is already
	// playing, and nobody else, is not a match worth returning.
	if backfill != nil && len(used) == 1 {
		return Result{}, nil, mc.snapshot(), false
	}
	if !allMinSatisfied(slots) {
		return Result{}, nil, mc.snapshot(), false
	}
	// The complete-match check, enforcing every rule rather than only the ones the
	// placement gate deemed ready.
	//
	// It cannot currently reject: a failed placement is reverted exactly, so these
	// slots are the ones the last accepted placement was validated against, and
	// reaching allMinSatisfied here means no rule was deferred at that moment. The
	// check is kept as the invariant's guard — it is what makes "every rule holds
	// over the finished match" a property of this function rather than of the
	// loop's bookkeeping — so a future placement path that skips validation cannot
	// emit an inadmissible match.
	if !rulesPass(evals, slots, nil, mc) {
		return Result{}, nil, mc.snapshot(), false
	}

	out := Result{Teams: make(map[string][]core.Player, len(slots)), Region: sharedRegion(slots)}
	for _, s := range slots {
		out.Teams[s.Name] = s.Players
	}
	out.TicketIDs = slices.Sorted(maps.Keys(used))
	return out, used, mc.snapshot(), true
}

// seedBackfill seats a backfill ticket's players on the teams they already
// occupy, reporting false if the roster does not fit the rule set's teams.
//
// Seating happens before any rule is evaluated, and the seated players are
// never re-examined on their own: the match they come from is a given, and it
// may well have been formed under expansion-loosened values that the rule set
// no longer offers. Only the final evaluation, over the existing roster plus
// whoever joined, decides whether the backfill is admissible.
//
// Each player is seated as a one-player party, since a FlexMatch backfill
// request carries per-player data only and the original parties of the
// in-progress match are not recoverable from it.
func seedBackfill(slots []teamSlot, t core.Ticket) bool {
	for _, p := range t.Players {
		i, err := resolveTeamSlot(slots, p.Team)
		if err != nil || len(slots[i].Players) >= slots[i].MaxPlayers {
			return false
		}
		slots[i].Players = append(slots[i].Players, p)
		slots[i].Parties = append(slots[i].Parties, []core.Player{p})
	}
	return true
}

// CheckBackfillRoster reports whether players can be seated as the existing
// roster of a backfill match against rs: every player must name a team the rule
// set declares, and no team may be asked to hold more than its maxPlayers. A
// team that is already at maxPlayers is fine — it simply takes on nobody new.
func CheckBackfillRoster(rs *ruleset.RuleSet, players []core.Player) error {
	slots := expandTeams(rs)
	counts := make([]int, len(slots))
	for _, p := range players {
		i, err := resolveTeamSlot(slots, p.Team)
		if err != nil {
			return fmt.Errorf("player %q: %w", p.ID, err)
		}
		counts[i]++
		if counts[i] > slots[i].MaxPlayers {
			return fmt.Errorf("team %q is given %d players, more than its maxPlayers of %d", slots[i].Name, counts[i], slots[i].MaxPlayers)
		}
	}
	return nil
}

// resolveTeamSlot returns the index of the slot that team names, matching the
// slot names a caller reads back from Result.Teams. A team left unexpanded keeps
// its declared name and so resolves by it.
//
// Falling through to a base name means quantity expansion split that team, since
// expandTeams only renames a team when it produces several slots for it. The
// name therefore does not say which instance the player sits on, and is reported
// as ambiguous rather than resolved to an arbitrary one.
func resolveTeamSlot(slots []teamSlot, team string) (int, error) {
	if team == "" {
		return 0, errors.New("team is required")
	}
	for i, s := range slots {
		if s.Name == team {
			return i, nil
		}
	}
	if slices.ContainsFunc(slots, func(s teamSlot) bool { return s.BaseName == team }) {
		return 0, fmt.Errorf("team %q is declared with quantity > 1: name one of the teams it expands to, such as %q", team, team+"_1")
	}
	return 0, fmt.Errorf("unknown team %q", team)
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
	// Only a region every player reported a latency for can host the match. Map
	// iteration order is random, so when several regions qualify pick the
	// lexicographically smallest one to keep the result deterministic.
	best := ""
	for r, c := range regions {
		if c == totalPlayers && (best == "" || r < best) {
			best = r
		}
	}
	return best
}

// teamReq records which teams a rule depends on. A rule is only meaningful once
// the teams it reads have reached their minimum size; until then the greedy
// builder must not let the rule reject a placement, or balance/size rules
// (e.g. count(teams[a]) = count(teams[b])) would fail against the inevitably
// imbalanced partial matches that occur while teams are still filling.
type teamReq struct {
	all   bool                // references teams[*] or a match-wide scope
	teams map[string]struct{} // referenced team base names
}

// buildTeamReqs computes, per rule name, the teams that rule must wait for
// before it can be enforced during incremental placement.
//
// Only size/count rules need to wait: a rule that measures team player counts
// (it contains a count(...) expression, e.g. count(teams[a].players) =
// count(teams[b].players)) inevitably fails against the imbalanced partial
// matches the greedy builder traverses while filling teams, yet becomes
// satisfiable once the teams are balanced. Such a rule waits for the teams it
// references (teams[<name>] / teams[*]) to reach minPlayers. Every other rule —
// skill distances, collection/membership constraints like a block list, and so
// on — is monotonic (a violation is not undone by adding players) and yields a
// zero teamReq, meaning "always ready", so its incremental behaviour is
// unchanged. Compound rules inherit the union of their referenced rules' waits.
func buildTeamReqs(rs *ruleset.RuleSet) map[string]teamReq {
	reqs := make(map[string]teamReq, len(rs.Rules))
	for i := range rs.Rules {
		r := &rs.Rules[i]
		if r.Type == ruleset.RuleCompound {
			continue
		}
		exprs := ruleExprStrings(r)
		if exprsContainCount(exprs) {
			reqs[r.Name] = scanTeamRefs(exprs)
		} else {
			reqs[r.Name] = teamReq{teams: map[string]struct{}{}}
		}
	}
	for i := range rs.Rules {
		r := &rs.Rules[i]
		if r.Type != ruleset.RuleCompound {
			continue
		}
		req := teamReq{teams: map[string]struct{}{}}
		if node, err := ruleset.ParseCompound(r.Statement); err == nil {
			for _, child := range node.RuleNames() {
				cr := reqs[child]
				if cr.all {
					req.all = true
				}
				for name := range cr.teams {
					req.teams[name] = struct{}{}
				}
			}
		}
		reqs[r.Name] = req
	}
	return reqs
}

// exprsContainCount reports whether any expression counts players, which is the
// signature of a team-size rule whose enforcement must wait for teams to fill.
func exprsContainCount(exprs []string) bool {
	return slices.ContainsFunc(exprs, func(s string) bool { return strings.Contains(s, "count(") })
}

// ruleExprStrings collects the property-expression strings of a rule: its
// measurements plus its referenceValue when that is a JSON string (rather than
// a numeric literal).
func ruleExprStrings(r *ruleset.Rule) []string {
	out := slices.Clone(r.Measurements)
	if rv := r.ReferenceValue; len(rv) > 0 {
		var s string
		if json.Unmarshal(rv, &s) == nil {
			out = append(out, s)
		}
	}
	return out
}

// scanTeamRefs extracts teams[<name>] and teams[*] references from expression
// strings.
func scanTeamRefs(exprs []string) teamReq {
	req := teamReq{teams: map[string]struct{}{}}
	for _, s := range exprs {
		for {
			_, rest, found := strings.Cut(s, "teams[")
			if !found {
				break
			}
			name, tail, closed := strings.Cut(rest, "]")
			if !closed {
				break
			}
			s = tail
			name = strings.TrimSpace(name)
			if name == "*" || name == "" {
				req.all = true
				continue
			}
			req.teams[name] = struct{}{}
		}
	}
	return req
}

// ruleReady reports whether the teams a rule depends on have all reached their
// minimum size in the current slots, so the rule can be enforced during
// incremental placement. A zero teamReq (no dependency) is always ready.
func ruleReady(req teamReq, slots []teamSlot) bool {
	if req.all {
		for _, s := range slots {
			if len(s.Players) < s.MinPlayers {
				return false
			}
		}
	}
	for name := range req.teams {
		for _, s := range slots {
			if s.BaseName == name && len(s.Players) < s.MinPlayers {
				return false
			}
		}
	}
	return true
}

// rulesPass evaluates every rule against the candidate built from slots,
// recording each pass/fail into mc, and reports whether the candidate is
// admissible (all rules passed). It never short-circuits, so each rule's
// failedCount is complete; the returned bool still matches "all rules passed",
// so match correctness is unchanged.
//
// reqs selects the mode. When non-nil this is the placement-time gate: rules
// whose referenced teams have not yet reached minPlayers are skipped and
// deferred to the final evaluation, which lets the greedy builder pass through
// the temporarily imbalanced states it must traverse while filling teams. When
// nil — the final, complete-match check, where every team is at least at its
// minimum and all rules are therefore ready — every rule is enforced.
func rulesPass(evals []rule.Evaluator, slots []teamSlot, reqs map[string]teamReq, mc *metricsCollector) bool {
	// Region is left empty so latency rules pick any satisfying region.
	cand := buildCandidate(slots, "")
	allOK := true
	for _, e := range evals {
		if reqs != nil && !ruleReady(reqs[e.Name()], slots) {
			continue
		}
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
		slices.SortStableFunc(idx, func(a, b int) int { return cmp.Compare(sums[a], sums[b]) })
		return idx
	}
	// default: prefer least-full team to spread players evenly
	slices.SortStableFunc(idx, func(a, b int) int {
		return cmp.Compare(len(slots[a].Players), len(slots[b].Players))
	})
	return idx
}
