package flexi

import (
	"errors"
	"fmt"
	"testing"

	"github.com/moepig/flexi/internal/ruleset"
)

func TestExpandedTeamNameConflicts(t *testing.T) {
	for _, teams := range []string{
		`[{"name":"red","minPlayers":1,"maxPlayers":1,"quantity":2},{"name":"red_1","minPlayers":1,"maxPlayers":1}]`,
		`[{"name":"red","minPlayers":1,"maxPlayers":1,"quantity":2},{"name":"red_1","minPlayers":1,"maxPlayers":1,"quantity":2}]`,
	} {
		_, err := New([]byte(fmt.Sprintf(`{"ruleLanguageVersion":"1.0","teams":%s}`, teams)))
		if !errors.Is(err, ErrInvalidRuleSet) {
			t.Fatalf("teams %s: %v", teams, err)
		}
	}
}

func TestExistingAcceptedRuleFormsRemainUsable(t *testing.T) {
	for _, body := range []string{
		`{"ruleLanguageVersion":"1.0","teams":[{"name":"all","minPlayers":1,"maxPlayers":1,"quantity":-1}]}`,
		`{"ruleLanguageVersion":"1.0","playerAttributes":[{"name":"roles","type":"string_list"}],"teams":[{"name":"all","minPlayers":1,"maxPlayers":1}],"rules":[{"name":"roles","type":"collection","measurements":["players.attributes[roles]"],"operation":"reference_intersection_count","referenceValue":"medic","minCount":1}]}`,
		`{"ruleLanguageVersion":"1.0","playerAttributes":[{"name":"roles","type":"string_list"}],"teams":[{"name":"all","minPlayers":1,"maxPlayers":1}],"rules":[{"name":"roles","type":"collection","measurements":["players.attributes[roles]"],"operation":"contains","referenceValue":"medic","minCount":-1}]}`,
	} {
		m, err := New([]byte(body))
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		if err := m.Enqueue(Ticket{ID: "t", Players: []Player{{ID: "p", Attributes: Attributes{"roles": StringList("medic")}}}}); err != nil {
			t.Fatal(err)
		}
		matches, err := m.Tick()
		if err != nil || len(matches) != 1 {
			t.Fatalf("matches=%d err=%v", len(matches), err)
		}
	}
}

func TestCountExpressionsUseParsedTeamScope(t *testing.T) {
	cases := []struct {
		name, teams, measurement string
		players                  int
	}{
		{"single", `[{"name":"all","minPlayers":2,"maxPlayers":2}]`, "count(teams[all].players)", 2},
		{"space", `[{"name":"all","minPlayers":2,"maxPlayers":2}]`, "count (teams[all].players)", 2},
		{"newline", `[{"name":"all","minPlayers":2,"maxPlayers":2}]`, "count (\nplayers)", 2},
		{"global", `[{"name":"all","minPlayers":2,"maxPlayers":2}]`, "count(players)", 2},
		{"all", `[{"name":"red","minPlayers":2,"maxPlayers":2,"quantity":2}]`, "count(teams[*].players)", 4},
		{"expanded", `[{"name":"red","minPlayers":2,"maxPlayers":2,"quantity":2}]`, "count(teams[red_1].players)", 4},
		{"multiple", `[{"name":"red","minPlayers":2,"maxPlayers":2,"quantity":2}]`, "count(teams[red_1,red_2].players)", 4},
		{"base", `[{"name":"red","minPlayers":2,"maxPlayers":2,"quantity":2}]`, "count(teams[red].players)", 4},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			want := 2
			if tc.name == "base" {
				want = 4
			}
			body := fmt.Sprintf(`{"ruleLanguageVersion":"1.0","teams":%s,"rules":[{"name":"size","type":"comparison","measurements":[%q],"operation":"=","referenceValue":%d}]}`, tc.teams, tc.measurement, want)
			m, err := New([]byte(body))
			if err != nil {
				t.Fatal(err)
			}
			for i := range tc.players {
				id := fmt.Sprintf("p%d", i)
				if err := m.Enqueue(Ticket{ID: id, Players: []Player{{ID: id}}}); err != nil {
					t.Fatal(err)
				}
			}
			matches, err := m.Tick()
			if err != nil || len(matches) != 1 {
				t.Fatalf("matches=%d err=%v", len(matches), err)
			}
		})
	}
}

func TestCountReferenceValueWaitsForPlayers(t *testing.T) {
	const body = `{"ruleLanguageVersion":"1.0","teams":[{"name":"all","minPlayers":2,"maxPlayers":2}],"rules":[{"name":"size","type":"comparison","measurements":["2"],"operation":"=","referenceValue":"count(players)"}]}`
	m, err := New([]byte(body))
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"a", "b"} {
		if err := m.Enqueue(Ticket{ID: id, Players: []Player{{ID: id}}}); err != nil {
			t.Fatal(err)
		}
	}
	matches, err := m.Tick()
	if err != nil || len(matches) != 1 {
		t.Fatalf("matches=%d err=%v", len(matches), err)
	}
}

func TestCollectionLowerBoundIsCheckedOnCompleteCandidate(t *testing.T) {
	for _, tc := range []struct {
		name              string
		min, max, players int
		match             bool
	}{
		{"two medics", 2, 2, 2, true},
		{"one medic", 2, 2, 1, false},
		{"minimum below bound", 1, 3, 2, true},
		{"maximum exceeded", 2, 2, 2, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			minCount, maxCount := 2, 1
			if tc.name == "maximum exceeded" {
				minCount = 1
			}
			if tc.name != "maximum exceeded" {
				maxCount = 3
			}
			body := fmt.Sprintf(`{"ruleLanguageVersion":"1.0","playerAttributes":[{"name":"roles","type":"string_list"}],"teams":[{"name":"all","minPlayers":%d,"maxPlayers":%d}],"rules":[{"name":"medics","type":"collection","measurements":["players.attributes[roles]"],"operation":"contains","referenceValue":"medic","minCount":%d,"maxCount":%d}]}`, tc.min, tc.max, minCount, maxCount)
			m, err := New([]byte(body))
			if err != nil {
				t.Fatal(err)
			}
			for i := range tc.players {
				id := fmt.Sprintf("p%d", i)
				if err := m.Enqueue(Ticket{ID: id, Players: []Player{{ID: id, Attributes: Attributes{"roles": StringList("medic")}}}}); err != nil {
					t.Fatal(err)
				}
			}
			matches, err := m.Tick()
			if err != nil {
				t.Fatal(err)
			}
			if (len(matches) == 1) != tc.match {
				t.Fatalf("matches=%d", len(matches))
			}
		})
	}
}

func TestCompoundContainingDeferredCollection(t *testing.T) {
	const body = `{"ruleLanguageVersion":"1.0","playerAttributes":[{"name":"roles","type":"string_list"}],"teams":[{"name":"all","minPlayers":2,"maxPlayers":2}],"rules":[{"name":"medics","type":"collection","measurements":["players.attributes[roles]"],"operation":"contains","referenceValue":"medic","minCount":2},{"name":"impossible","type":"comparison","measurements":["0"],"operation":"=","referenceValue":1},{"name":"combined","type":"compound","statement":"or(medics,impossible)"}]}`
	m, err := New([]byte(body))
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"a", "b"} {
		if err := m.Enqueue(Ticket{ID: id, Players: []Player{{ID: id, Attributes: Attributes{"roles": StringList("medic")}}}}); err != nil {
			t.Fatal(err)
		}
	}
	matches, err := m.Tick()
	if err != nil || len(matches) != 1 {
		t.Fatalf("matches=%d err=%v", len(matches), err)
	}
	if len(matches[0].RuleEvaluationMetrics) != 1 || matches[0].RuleEvaluationMetrics[0].RuleName != "combined" {
		t.Fatalf("metrics=%+v", matches[0].RuleEvaluationMetrics)
	}
}

func TestInvalidRuleSetAndRuntimeEvaluationErrors(t *testing.T) {
	for _, rule := range []string{
		`{"name":"distance","type":"distance","measurements":["players.attributes[skill]"],"maxDistance":1}`,
		`{"name":"bad","type":"comparison","measurements":["typo(players)"],"operation":"=","referenceValue":1}`,
		`{"name":"bad","type":"comparison","measurements":["count(teams[missing].players)"],"operation":"=","referenceValue":1}`,
	} {
		_, err := New([]byte(fmt.Sprintf(`{"ruleLanguageVersion":"1.0","teams":[{"name":"all","minPlayers":1,"maxPlayers":1}],"rules":[%s]}`, rule)))
		if !errors.Is(err, ErrInvalidRuleSet) {
			t.Fatalf("rule %s: %v", rule, err)
		}
	}
	for _, body := range []string{
		`{"ruleLanguageVersion":"1.0","playerAttributes":[{"name":"skill","type":"number","default":"bad"}],"teams":[{"name":"all","minPlayers":1,"maxPlayers":1}]}`,
		`{"ruleLanguageVersion":"1.0","playerAttributes":[{"name":"role","type":"string"}],"teams":[{"name":"all","minPlayers":1,"maxPlayers":1}],"rules":[{"name":"bad","type":"comparison","measurements":["avg(players.attributes[role])"],"operation":"=","referenceValue":1}]}`,
		`{"ruleLanguageVersion":"1.0","teams":[{"name":"all","minPlayers":1,"maxPlayers":1}],"expansions":[{"target":"teams[missing].minPlayers","steps":[{"waitTimeSeconds":10,"value":1}]}]}`,
		`{"ruleLanguageVersion":"1.0","teams":[{"name":"all","minPlayers":1,"maxPlayers":1}],"expansions":[{"target":"teams[all].minPlayers","steps":[{"waitTimeSeconds":10,"value":2}]}]}`,
	} {
		_, err := New([]byte(body))
		if !errors.Is(err, ErrInvalidRuleSet) {
			t.Fatalf("body %s: %v", body, err)
		}
	}
	m, err := New([]byte(`{"ruleLanguageVersion":"1.0","teams":[{"name":"all","minPlayers":1,"maxPlayers":1}],"rules":[{"name":"bad","type":"comparison","measurements":["avg(players.attributes[role])"],"operation":"=","referenceValue":1}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Enqueue(Ticket{ID: "t", Players: []Player{{ID: "p", Attributes: Attributes{"role": String("medic")}}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Tick(); err == nil {
		t.Fatal("runtime evaluation error was suppressed")
	}
	if status, _ := m.Status("t"); status != StatusQueued {
		t.Fatalf("status=%s", status)
	}
}

func TestCompoundForwardReferenceAndCycle(t *testing.T) {
	base := `{"ruleLanguageVersion":"1.0","teams":[{"name":"all","minPlayers":1,"maxPlayers":1}],"rules":%s}`
	forward := `[{"name":"outer","type":"compound","statement":"and(inner,inner)"},{"name":"inner","type":"comparison","measurements":["count(players)"],"operation":"=","referenceValue":1}]`
	if _, err := New([]byte(fmt.Sprintf(base, forward))); err != nil {
		t.Fatal(err)
	}
	cycle := `[{"name":"a","type":"compound","statement":"and(b,b)"},{"name":"b","type":"compound","statement":"and(a,a)"}]`
	if _, err := New([]byte(fmt.Sprintf(base, cycle))); !errors.Is(err, ruleset.ErrInvalidRuleSet) {
		t.Fatalf("cycle: %v", err)
	}
}
