package flexi

import (
	"fmt"
	"testing"
)

func BenchmarkTickMatches(b *testing.B) {
	for _, count := range []int{500, 1000, 2000} {
		for _, acceptance := range []bool{false, true} {
			b.Run(fmt.Sprintf("tickets=%d/acceptance=%t", count, acceptance), func(b *testing.B) {
				body := fmt.Sprintf(`{"ruleLanguageVersion":"1.0","teams":[{"name":"all","minPlayers":2,"maxPlayers":2}],"rules":[{"name":"always","type":"comparison","measurements":["count(players)"],"operation":">=","referenceValue":0}],"acceptanceRequired":%t}`, acceptance)
				b.ReportAllocs()
				for range b.N {
					b.StopTimer()
					m, err := New([]byte(body))
					if err != nil {
						b.Fatal(err)
					}
					for i := range count {
						id := fmt.Sprintf("p%d", i)
						if err := m.Enqueue(Ticket{ID: id, Players: []Player{{ID: id}}}); err != nil {
							b.Fatal(err)
						}
					}
					b.StartTimer()
					_, err = m.Tick()
					b.StopTimer()
					if err != nil {
						b.Fatal(err)
					}
				}
			})
		}
	}
}

func BenchmarkTickUnmatched(b *testing.B) {
	const body = `{"ruleLanguageVersion":"1.0","teams":[{"name":"all","minPlayers":4,"maxPlayers":4}],"rules":[{"name":"size","type":"comparison","measurements":["count(players)"],"operation":"=","referenceValue":4}]}`
	m, err := New([]byte(body))
	if err != nil {
		b.Fatal(err)
	}
	if err := m.Enqueue(Ticket{ID: "waiting", Players: []Player{{ID: "p"}}}); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		if _, err := m.Tick(); err != nil {
			b.Fatal(err)
		}
	}
}
