package flexi_test

import (
	"testing"
	"time"

	"github.com/moepig/flexi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// findMetric returns the RuleMetric named name, or false if absent.
func findMetric(metrics []flexi.RuleMetric, name string) (flexi.RuleMetric, bool) {
	for _, m := range metrics {
		if m.RuleName == name {
			return m, true
		}
	}
	return flexi.RuleMetric{}, false
}

func metricNames(metrics []flexi.RuleMetric) []string {
	out := make([]string, 0, len(metrics))
	for _, m := range metrics {
		out = append(out, m.RuleName)
	}
	return out
}

// Two comparison rules where the first fails and the second passes for a
// high-skill candidate, so no match forms. Rule order matters: a
// short-circuiting evaluator would never reach Pass.
const failFirstRS = `{
  "name": "fail-first",
  "ruleLanguageVersion": "1.0",
  "playerAttributes": [{"name": "skill", "type": "number"}],
  "teams": [{"name": "all", "minPlayers": 2, "maxPlayers": 2}],
  "rules": [
    {"name": "Fail", "type": "comparison",
     "measurements": ["avg(players.attributes[skill])"], "referenceValue": 10, "operation": "<="},
    {"name": "Pass", "type": "comparison",
     "measurements": ["avg(players.attributes[skill])"], "referenceValue": 0, "operation": ">="}
  ]
}`

// Purpose: Verify a formed Match carries the rule's evaluation metrics with the
// rule set's name and a positive pass count.
// Method:  Form a single match from four skill-balanced solos against skillRS.
// Expect:  One RuleMetric named "FairSkill" with PassedCount>=1 and FailedCount==0.
func TestMetrics_BasicMatchAttachesMetrics(t *testing.T) {
	mm, err := flexi.New([]byte(skillRS))
	require.NoError(t, err)
	for _, tk := range []flexi.Ticket{solo("a", 50), solo("b", 52), solo("c", 49), solo("d", 51)} {
		require.NoError(t, mm.Enqueue(tk))
	}
	matches, err := mm.Tick()
	require.NoError(t, err)
	require.Len(t, matches, 1)

	m, ok := findMetric(matches[0].RuleEvaluationMetrics, "FairSkill")
	require.True(t, ok, "FairSkill metric present: %v", metricNames(matches[0].RuleEvaluationMetrics))
	assert.GreaterOrEqual(t, m.PassedCount, 1)
	assert.Equal(t, 0, m.FailedCount)
}

// Purpose: Verify every rule is evaluated (no short-circuit) so a later rule records a pass even when an earlier rule fails.
// Method:  Enqueue two high-skill solos against failFirstRS (Fail then Pass) and Tick.
// Expect:  No match; metrics show Fail.FailedCount>0 and Pass.PassedCount>0 (Pass evaluated despite Fail failing first).
func TestMetrics_NoShortCircuitCountsAllRules(t *testing.T) {
	mm, err := flexi.New([]byte(failFirstRS))
	require.NoError(t, err)
	require.NoError(t, mm.Enqueue(solo("a", 100)))
	require.NoError(t, mm.Enqueue(solo("b", 100)))

	matches, err := mm.Tick()
	require.NoError(t, err)
	require.Empty(t, matches)

	metrics, ok := mm.RuleMetrics("a")
	require.True(t, ok)

	fail, ok := findMetric(metrics, "Fail")
	require.True(t, ok)
	assert.Greater(t, fail.FailedCount, 0)
	assert.Equal(t, 0, fail.PassedCount)

	pass, ok := findMetric(metrics, "Pass")
	require.True(t, ok, "second rule must still appear: %v", metricNames(metrics))
	assert.Greater(t, pass.PassedCount, 0, "Pass evaluated even though Fail failed first")
}

// Purpose: Verify timed-out tickets retain queryable cumulative metrics.
// Method:  Form a proposal (FairSkill evaluated → metrics recorded), accept a,b,c and Reject d so a,b,c
//
//	return to SEARCHING, then let requestTimeoutSeconds elapse to genuinely time them out.
//
// Expect:  Ticket "a" is TIMED_OUT and RuleMetrics still returns its FairSkill metric.
func TestMetrics_RetainedAfterTimeout(t *testing.T) {
	clock := flexi.NewFakeClock(time.Unix(1_700_000_000, 0))
	mm, err := flexi.New([]byte(acceptReqTimeoutRS), flexi.WithClock(clock))
	require.NoError(t, err)
	enqueueQuartet(t, mm)

	_, err = mm.Tick()
	require.NoError(t, err)
	require.Len(t, mm.PendingAcceptances(), 1)

	for _, id := range []string{"a", "b", "c"} {
		require.NoError(t, mm.Accept(id, id))
	}
	require.NoError(t, mm.Reject("d", "d"))

	clock.Advance(121 * time.Second)
	_, err = mm.Tick()
	require.NoError(t, err)

	s, err := mm.Status("a")
	require.NoError(t, err)
	require.Equal(t, flexi.StatusTimedOut, s)

	metrics, ok := mm.RuleMetrics("a")
	require.True(t, ok, "timed-out ticket should retain metrics")
	_, ok = findMetric(metrics, "FairSkill")
	assert.True(t, ok)
}

// Purpose: Verify cancelled tickets retain metrics if they participated in a search, but unticked-then-cancelled tickets report none.
// Method:  (a) Cancel a ticket that was never ticked; (b) Tick a non-matching pair, then cancel one of them.
// Expect:  (a) RuleMetrics returns false; (b) RuleMetrics returns true.
func TestMetrics_CancelledTicket(t *testing.T) {
	t.Run("never evaluated", func(t *testing.T) {
		mm, err := flexi.New([]byte(skillRS))
		require.NoError(t, err)
		require.NoError(t, mm.Enqueue(solo("a", 50)))
		require.NoError(t, mm.Cancel("a"))

		_, ok := mm.RuleMetrics("a")
		assert.False(t, ok, "a was cancelled before any Tick")
	})

	t.Run("evaluated then cancelled", func(t *testing.T) {
		mm, err := flexi.New([]byte(failFirstRS))
		require.NoError(t, err)
		require.NoError(t, mm.Enqueue(solo("a", 100)))
		require.NoError(t, mm.Enqueue(solo("b", 100)))

		_, err = mm.Tick() // no match, but a/b participate in a search
		require.NoError(t, err)
		require.NoError(t, mm.Cancel("a"))

		s, err := mm.Status("a")
		require.NoError(t, err)
		require.Equal(t, flexi.StatusCancelled, s)

		metrics, ok := mm.RuleMetrics("a")
		require.True(t, ok, "cancelled ticket retains accumulated metrics")
		assert.NotEmpty(t, metrics)
	})
}

// Purpose: Verify metrics report only standalone top-level rules. A rule
// referenced by a compound is evaluated only inside that compound (never on its
// own, matching AWS FlexMatch), so it is not listed as a separate metric.
// Method:  Form a match against a rule set whose rules are RuleA, RuleB, and a
// compound Both that ANDs them.
// Expect:  Only the compound "Both" appears; its children RuleA/RuleB do not.
func TestMetrics_CompoundReportsTopLevelOnly(t *testing.T) {
	body := `{
	  "name": "compound",
	  "ruleLanguageVersion": "1.0",
	  "playerAttributes": [{"name": "skill", "type": "number"}],
	  "teams": [{"name": "all", "minPlayers": 2, "maxPlayers": 2}],
	  "rules": [
	    {"name": "RuleA", "type": "comparison",
	     "measurements": ["avg(players.attributes[skill])"], "referenceValue": 0, "operation": ">="},
	    {"name": "RuleB", "type": "comparison",
	     "measurements": ["avg(players.attributes[skill])"], "referenceValue": 1000, "operation": "<="},
	    {"name": "Both", "type": "compound",
	     "statement": "and(RuleA, RuleB)"}
	  ]
	}`
	mm, err := flexi.New([]byte(body))
	require.NoError(t, err)
	require.NoError(t, mm.Enqueue(solo("a", 50)))
	require.NoError(t, mm.Enqueue(solo("b", 50)))

	matches, err := mm.Tick()
	require.NoError(t, err)
	require.Len(t, matches, 1)

	assert.ElementsMatch(t, []string{"Both"},
		metricNames(matches[0].RuleEvaluationMetrics))
}

// Purpose: Verify a rule set with no rules yields no metrics, preserving the
// backward-compatible zero value for existing callers.
// Method:  Form a match against a rules-less rule set.
// Expect:  Match.RuleEvaluationMetrics is empty.
func TestMetrics_NoRulesIsEmpty(t *testing.T) {
	body := `{
	  "name": "norules",
	  "ruleLanguageVersion": "1.0",
	  "teams": [{"name": "all", "minPlayers": 2, "maxPlayers": 2}]
	}`
	mm, err := flexi.New([]byte(body))
	require.NoError(t, err)
	require.NoError(t, mm.Enqueue(solo("a", 0)))
	require.NoError(t, mm.Enqueue(solo("b", 0)))

	matches, err := mm.Tick()
	require.NoError(t, err)
	require.Len(t, matches, 1)
	assert.Empty(t, matches[0].RuleEvaluationMetrics)
}

// Purpose: Verify per-ticket metrics accumulate across multiple Ticks.
// Method:  Two tickets fail a tight batchDistance on the first Tick, then match after a 31s expansion loosens it.
// Expect:  The "Tight" metric carries both a failure (from Tick 1) and passes (from Tick 2), proving cross-tick accumulation.
func TestMetrics_AccumulatesAcrossTicks(t *testing.T) {
	body := `{
	  "name": "expand",
	  "ruleLanguageVersion": "1.0",
	  "playerAttributes": [{"name":"skill","type":"number"}],
	  "teams": [{"name": "all", "minPlayers": 2, "maxPlayers": 2}],
	  "rules": [
	    {"name": "Tight", "type": "batchDistance",
	     "batchAttribute": "skill", "maxDistance": 5}
	  ],
	  "expansions": [
	    {"target": "rules[Tight].maxDistance",
	     "steps": [{"waitTimeSeconds": 30, "value": 100}]}
	  ]
	}`
	clock := flexi.NewFakeClock(time.Unix(1_700_000_000, 0))
	mm, err := flexi.New([]byte(body), flexi.WithClock(clock))
	require.NoError(t, err)
	require.NoError(t, mm.Enqueue(solo("a", 10)))
	require.NoError(t, mm.Enqueue(solo("b", 80)))

	matches, err := mm.Tick() // gap 70 > 5: no match, Tight fails
	require.NoError(t, err)
	require.Empty(t, matches)

	clock.Advance(31 * time.Second)
	matches, err = mm.Tick() // expansion loosens to 100: match forms
	require.NoError(t, err)
	require.Len(t, matches, 1)

	metrics, ok := mm.RuleMetrics("a")
	require.True(t, ok)
	tight, ok := findMetric(metrics, "Tight")
	require.True(t, ok)
	assert.Greater(t, tight.FailedCount, 0, "failure recorded on the first tick")
	assert.Greater(t, tight.PassedCount, 0, "passes recorded on the second tick")
}

// Purpose: Verify the acceptance path exposes metrics on the Proposal and that
// the eventual Match (resolved from that proposal) carries the same metrics.
// Method:  Form a proposal under acceptRS, read its metrics, then have every
//
//	player accept and Tick to resolve it into a Match.
//
// Expect:  The Proposal carries a FairSkill metric (PassedCount>=1) and the
//
//	resolved Match's metrics are identical to the proposal's.
func TestMetrics_ProposalAndResolvedMatch(t *testing.T) {
	mm, err := flexi.New([]byte(acceptRS))
	require.NoError(t, err)
	enqueueQuartet(t, mm)

	_, err = mm.Tick()
	require.NoError(t, err)
	proposals := mm.PendingAcceptances()
	require.Len(t, proposals, 1)
	prop := proposals[0]

	pm, ok := findMetric(prop.RuleEvaluationMetrics, "FairSkill")
	require.True(t, ok, "proposal exposes metrics: %v", metricNames(prop.RuleEvaluationMetrics))
	assert.GreaterOrEqual(t, pm.PassedCount, 1)

	// solo tickets use the ticket id as their single player's id.
	for _, id := range prop.TicketIDs {
		require.NoError(t, mm.Accept(id, id))
	}
	matches, err := mm.Tick()
	require.NoError(t, err)
	require.Len(t, matches, 1)

	assert.Equal(t, prop.RuleEvaluationMetrics, matches[0].RuleEvaluationMetrics,
		"resolved match reuses the proposal's metrics")
}

// Purpose: Verify that when one Tick forms several matches, each Match carries
// its own populated metrics (no cross-contamination between results).
// Method:  Enqueue eight skill-balanced solos against skillRS so Build forms
//
//	two independent 2v2 matches in a single Tick.
//
// Expect:  Two matches, each with a FairSkill metric whose PassedCount>=1.
func TestMetrics_MultipleMatchesEachHaveMetrics(t *testing.T) {
	mm, err := flexi.New([]byte(skillRS))
	require.NoError(t, err)
	for _, id := range []string{"a", "b", "c", "d", "e", "f", "g", "h"} {
		require.NoError(t, mm.Enqueue(solo(id, 50)))
	}

	matches, err := mm.Tick()
	require.NoError(t, err)
	require.Len(t, matches, 2)

	for i, m := range matches {
		fs, ok := findMetric(m.RuleEvaluationMetrics, "FairSkill")
		require.Truef(t, ok, "match %d carries FairSkill: %v", i, metricNames(m.RuleEvaluationMetrics))
		assert.GreaterOrEqualf(t, fs.PassedCount, 1, "match %d", i)
	}
}

// Purpose: Verify exact pass/fail counts, the ruleset-order of the returned
// slice, and that metrics are attributed to every ticket present during the
// search (not only matched ones).
// Method:  Enqueue two high-skill solos against failFirstRS. The search places
//
//	ticket "a" once (Fail fails, Pass passes) then bails out, so the
//	tallies are fully determined; "b" never gets placed.
//
// Expect:  Both "a" and "b" report exactly [Fail{0,1}, Pass{1,0}] in that order.
func TestMetrics_ExactCountsOrderAndAttribution(t *testing.T) {
	mm, err := flexi.New([]byte(failFirstRS))
	require.NoError(t, err)
	require.NoError(t, mm.Enqueue(solo("a", 100)))
	require.NoError(t, mm.Enqueue(solo("b", 100)))

	matches, err := mm.Tick()
	require.NoError(t, err)
	require.Empty(t, matches)

	want := []flexi.RuleMetric{
		{RuleName: "Fail", PassedCount: 0, FailedCount: 1},
		{RuleName: "Pass", PassedCount: 1, FailedCount: 0},
	}
	for _, id := range []string{"a", "b"} {
		metrics, ok := mm.RuleMetrics(id)
		require.Truef(t, ok, "ticket %s", id)
		assert.Equalf(t, want, metrics, "ticket %s (exact counts + ruleset order)", id)
	}
}

// Purpose: Verify RuleMetrics returns a defensive copy so callers cannot mutate
// the matchmaker's stored metrics.
// Method:  Read a ticket's metrics, mutate the returned slice, then read again.
// Expect:  The second read is unaffected by the mutation.
func TestMetrics_ReturnedSliceIsCopy(t *testing.T) {
	mm, err := flexi.New([]byte(failFirstRS))
	require.NoError(t, err)
	require.NoError(t, mm.Enqueue(solo("a", 100)))
	require.NoError(t, mm.Enqueue(solo("b", 100)))

	_, err = mm.Tick()
	require.NoError(t, err)

	first, ok := mm.RuleMetrics("a")
	require.True(t, ok)
	require.NotEmpty(t, first)
	first[0].RuleName = "MUTATED"
	first[0].PassedCount = 9999
	first[0].FailedCount = 9999

	second, ok := mm.RuleMetrics("a")
	require.True(t, ok)
	assert.Equal(t, []flexi.RuleMetric{
		{RuleName: "Fail", PassedCount: 0, FailedCount: 1},
		{RuleName: "Pass", PassedCount: 1, FailedCount: 0},
	}, second, "internal metrics unaffected by caller mutation")
}
