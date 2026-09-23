package flexi

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/moepig/flexi/internal/ruleset"
)

const reviewAcceptRuleSet = `{"ruleLanguageVersion":"1.0","teams":[{"name":"all","minPlayers":2,"maxPlayers":2}],"acceptanceRequired":true,"acceptanceTimeoutSeconds":60}`

func TestTicketOwnershipAndPlayerIDs(t *testing.T) {
	m, err := New([]byte(reviewAcceptRuleSet))
	if err != nil {
		t.Fatal(err)
	}
	for _, players := range [][]Player{{{ID: ""}}, {{ID: "same"}, {ID: "same"}}} {
		if err := m.Enqueue(Ticket{ID: "invalid", Players: players}); !errors.Is(err, ErrInvalidTicket) {
			t.Fatalf("Enqueue: %v", err)
		}
		if err := m.EnqueueBackfill(Ticket{ID: "invalid", Players: players}); !errors.Is(err, ErrInvalidTicket) {
			t.Fatalf("EnqueueBackfill: %v", err)
		}
	}
	first := Ticket{ID: "first", Players: []Player{{ID: "p1", Attributes: Attributes{"roles": StringList("medic"), "scores": StringNumberMap(map[string]float64{"a": 1})}, Latencies: map[string]int{"a": 1}}}}
	if err := m.Enqueue(first); err != nil {
		t.Fatal(err)
	}
	first.Players[0].Attributes["roles"].SL[0] = "changed"
	first.Players[0].Attributes["scores"].SDM["a"] = 99
	first.Players[0].Latencies["a"] = 99
	first.Players[0].ID = "changed"
	if err := m.Enqueue(Ticket{ID: "second", Players: []Player{{ID: "p2"}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Tick(); err != nil {
		t.Fatal(err)
	}
	pending := m.PendingAcceptances()
	if len(pending) != 1 {
		t.Fatalf("pending: %d", len(pending))
	}
	p := pending[0].Teams["all"][0]
	if p.ID != "p1" || p.Attributes["roles"].SL[0] != "medic" || p.Attributes["scores"].SDM["a"] != 1 || p.Latencies["a"] != 1 {
		t.Fatalf("input changed proposal: %+v", p)
	}
	pending[0].Teams["all"][0].Attributes["roles"].SL[0] = "changed again"
	pending[0].Teams["all"][0].Attributes["scores"].SDM["a"] = 100
	pending[0].Teams["all"][0].Latencies["a"] = 100
	p = m.PendingAcceptances()[0].Teams["all"][0]
	if p.Attributes["roles"].SL[0] != "medic" || p.Attributes["scores"].SDM["a"] != 1 || p.Latencies["a"] != 1 {
		t.Fatalf("snapshot changed proposal: %+v", p)
	}
}

func TestDefaultAttributeOwnershipAcrossMatches(t *testing.T) {
	const body = `{"ruleLanguageVersion":"1.0","playerAttributes":[{"name":"modes","type":"string_list","default":["TDM"]},{"name":"ping","type":"string_number_map","default":{"a":10}}],"teams":[{"name":"all","minPlayers":1,"maxPlayers":1}]}`
	m, err := New([]byte(body))
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"one", "two"} {
		if err := m.Enqueue(Ticket{ID: id, Players: []Player{{ID: id}}}); err != nil {
			t.Fatal(err)
		}
		matches, err := m.Tick()
		if err != nil || len(matches) != 1 {
			t.Fatalf("matches=%d err=%v", len(matches), err)
		}
		player := matches[0].Teams["all"][0]
		if player.Attributes["modes"].SL[0] != "TDM" || player.Attributes["ping"].SDM["a"] != 10 {
			t.Fatalf("default leaked: %+v", player.Attributes)
		}
		player.Attributes["modes"].SL[0] = "CTF"
		player.Attributes["ping"].SDM["a"] = 99
	}
}

func TestExternalInputMutationDuringProposalReads(t *testing.T) {
	m, err := New([]byte(reviewAcceptRuleSet))
	if err != nil {
		t.Fatal(err)
	}
	input := Ticket{ID: "a", Players: []Player{{ID: "a", Attributes: Attributes{"roles": StringList("medic")}, Latencies: map[string]int{"a": 1}}}}
	if err := m.Enqueue(input); err != nil {
		t.Fatal(err)
	}
	if err := m.Enqueue(Ticket{ID: "b", Players: []Player{{ID: "b"}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Tick(); err != nil {
		t.Fatal(err)
	}
	started := make(chan struct{})
	done := make(chan struct{})
	go func() {
		close(started)
		for i := range 1000 {
			input.Players[0].Attributes["roles"].SL[0] = fmt.Sprintf("role%d", i)
			input.Players[0].Latencies["a"] = i
		}
		close(done)
	}()
	<-started
	for range 1000 {
		p := m.PendingAcceptances()[0].Teams["all"][0]
		if p.Attributes["roles"].SL[0] != "medic" || p.Latencies["a"] != 1 {
			t.Fatalf("proposal changed: %+v", p)
		}
	}
	<-done
}

func TestInvalidBackfillDoesNotReplaceQueuedRequest(t *testing.T) {
	m, err := New([]byte(reviewAcceptRuleSet))
	if err != nil {
		t.Fatal(err)
	}
	good := Ticket{ID: "old", GameSessionID: "session", Players: []Player{{ID: "p", Team: "all"}}}
	if err := m.EnqueueBackfill(good); err != nil {
		t.Fatal(err)
	}
	bad := Ticket{ID: "new", GameSessionID: "session", Players: []Player{{ID: "p", Team: "all"}, {ID: "p", Team: "all"}}}
	if err := m.EnqueueBackfill(bad); !errors.Is(err, ErrInvalidTicket) {
		t.Fatalf("replacement: %v", err)
	}
	if status, _ := m.Status("old"); status != StatusQueued {
		t.Fatalf("old status=%s", status)
	}
}

func TestAcceptanceDeadlineBoundary(t *testing.T) {
	clock := NewFakeClock(time.Unix(0, 0))
	m, err := New([]byte(reviewAcceptRuleSet), WithClock(clock))
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"a", "b"} {
		if err := m.Enqueue(Ticket{ID: id, Players: []Player{{ID: id}}}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := m.Tick(); err != nil {
		t.Fatal(err)
	}
	if err := m.Accept("a", "a"); err != nil {
		t.Fatal(err)
	}
	clock.Advance(60 * time.Second)
	if err := m.Accept("b", "b"); !errors.Is(err, ErrUnknownProposal) {
		t.Fatalf("accept at deadline: %v", err)
	}
	if _, err := m.Tick(); err != nil {
		t.Fatal(err)
	}
	if status, _ := m.Status("a"); status != StatusSearching {
		t.Fatalf("accepted ticket status=%s", status)
	}
	if status, _ := m.Status("b"); status != StatusCancelled {
		t.Fatalf("unaccepted ticket status=%s", status)
	}
}

func TestExpansionDefinitionReuseAndReversal(t *testing.T) {
	const body = `{"ruleLanguageVersion":"1.0","teams":[{"name":"all","minPlayers":2,"maxPlayers":2}],"expansions":[{"target":"teams[all].maxPlayers","steps":[{"waitTimeSeconds":10,"value":3}]}]}`
	clock := NewFakeClock(time.Unix(0, 0))
	m, err := New([]byte(body), WithClock(clock))
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Enqueue(Ticket{ID: "old", Players: []Player{{ID: "old"}}}); err != nil {
		t.Fatal(err)
	}
	clock.Advance(11 * time.Second)
	if _, err := m.Tick(); err != nil {
		t.Fatal(err)
	}
	active := m.expandedRS
	if active == nil || active == m.rs {
		t.Fatal("expansion was not applied")
	}
	if _, err := m.Tick(); err != nil {
		t.Fatal(err)
	}
	if m.expandedRS != active {
		t.Fatal("same step was rebuilt")
	}
	if err := m.Enqueue(Ticket{ID: "new", Players: []Player{{ID: "new"}}}); err != nil {
		t.Fatal(err)
	}
	matches, err := m.Tick()
	if err != nil || len(matches) != 1 {
		t.Fatalf("matches=%d err=%v", len(matches), err)
	}
	if m.expandedRS != m.rs {
		t.Fatal("newest ticket did not restore base definition")
	}
}

func TestAcceptedProposalSurvivesDeadlineAndTickError(t *testing.T) {
	clock := NewFakeClock(time.Unix(0, 0))
	m, err := New([]byte(reviewAcceptRuleSet), WithClock(clock))
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"a", "b"} {
		if err := m.Enqueue(Ticket{ID: id, Players: []Player{{ID: id}}}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := m.Tick(); err != nil {
		t.Fatal(err)
	}
	clock.Advance(59 * time.Second)
	for _, id := range []string{"a", "b"} {
		if err := m.Accept(id, id); err != nil {
			t.Fatal(err)
		}
	}
	clock.Advance(2 * time.Second)
	if err := m.Accept("a", "a"); !errors.Is(err, ErrUnknownProposal) {
		t.Fatalf("late accept: %v", err)
	}
	if err := m.Enqueue(Ticket{ID: "waiting", Players: []Player{{ID: "waiting"}}}); err != nil {
		t.Fatal(err)
	}
	m.rs.Expansions = []ruleset.Expansion{{Target: "teams[missing].minPlayers", Steps: []ruleset.ExpansionStep{{WaitTimeSeconds: 0, Value: []byte("1")}}}}
	if _, err := m.Tick(); err == nil {
		t.Fatal("expected expansion error")
	}
	if len(m.PendingAcceptances()) != 1 {
		t.Fatal("accepted proposal was lost")
	}
	m.rs.Expansions = nil
	matches, err := m.Tick()
	if err != nil || len(matches) != 1 {
		t.Fatalf("matches=%d err=%v", len(matches), err)
	}
}
