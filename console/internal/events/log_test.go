package events

import (
	"errors"
	"sync"
	"testing"
	"time"

	clocktesting "k8s.io/utils/clock/testing"
)

func TestAddStampsIDAndTime(t *testing.T) {
	clk := clocktesting.NewFakeClock(time.Date(2026, 10, 31, 1, 2, 3, 0, time.UTC))
	l := NewLog(10, clk)
	a := l.Add(Event{Kind: NodeReachable, Node: "m1"})
	clk.Step(time.Second)
	b := l.Add(Event{Kind: NodeUnreachable, Node: "m1"})
	if a.ID != 1 || b.ID != 2 {
		t.Fatalf("ids %d %d", a.ID, b.ID)
	}
	if !b.Time.After(a.Time) {
		t.Fatal("time not stamped from the clock")
	}
}

func TestRingKeepsNewest(t *testing.T) {
	l := NewLog(3, clocktesting.NewFakeClock(time.Now()))
	for range 5 {
		l.Add(Event{Kind: RunStatus})
	}
	got := l.List(Filter{})
	if len(got) != 3 || got[0].ID != 3 || got[2].ID != 5 {
		t.Fatalf("ring = %+v", got)
	}
}

func TestMinimumCapacity(t *testing.T) {
	l := NewLog(0, clocktesting.NewFakeClock(time.Now()))
	l.Add(Event{})
	l.Add(Event{})
	if n := len(l.List(Filter{})); n != 1 {
		t.Fatalf("want 1, got %d", n)
	}
}

func TestFilter(t *testing.T) {
	l := NewLog(10, clocktesting.NewFakeClock(time.Now()))
	l.Add(Event{Node: "a", RunID: "r1", Workflow: "reboot"})
	l.Add(Event{Node: "b", RunID: "r2", Workflow: "standby"})
	l.Add(Event{Node: "a", RunID: "r3", Workflow: "standby"})
	cases := []struct {
		name string
		f    Filter
		want int
	}{
		{"all", Filter{}, 3},
		{"node", Filter{Node: "a"}, 2},
		{"run", Filter{RunID: "r2"}, 1},
		{"workflow", Filter{Workflow: "standby"}, 2},
		{"after", Filter{After: 2}, 1},
		{"combined", Filter{Node: "a", Workflow: "standby"}, 1},
		{"none", Filter{Node: "zzz"}, 0},
	}
	for _, c := range cases {
		if got := len(l.List(c.f)); got != c.want {
			t.Errorf("%s: got %d want %d", c.name, got, c.want)
		}
	}
}

func TestConcurrentAdds(t *testing.T) {
	l := NewLog(1000, clocktesting.NewFakeClock(time.Now()))
	var wg sync.WaitGroup
	for range 50 {
		wg.Add(1)
		go func() { defer wg.Done(); l.Add(Event{}) }()
	}
	wg.Wait()
	if n := len(l.List(Filter{})); n != 50 {
		t.Fatalf("got %d", n)
	}
}

func TestNewLogDefaultsToTheRealClock(t *testing.T) {
	l := NewLog(2, nil)
	if e := l.Add(Event{Kind: ActionOK}); e.Time.IsZero() {
		t.Fatal("event not stamped")
	}
}

type memSink struct {
	saved   []Event
	loadErr error
}

func (m *memSink) Save(e Event) error { m.saved = append(m.saved, e); return nil }
func (m *memSink) Load(max int) ([]Event, error) {
	if m.loadErr != nil {
		return nil, m.loadErr
	}
	if len(m.saved) > max {
		return m.saved[len(m.saved)-max:], nil
	}
	return m.saved, nil
}

func TestPersistentLogWritesThroughAndKeepsCountingAcrossRestart(t *testing.T) {
	sink := &memSink{}
	l, err := NewPersistentLog(10, nil, sink)
	if err != nil {
		t.Fatal(err)
	}
	l.Add(Event{Kind: "a"})
	l.Add(Event{Kind: "b"})
	if len(sink.saved) != 2 || sink.saved[1].ID != 2 {
		t.Fatalf("not written through: %+v", sink.saved)
	}
	// A restart reloads the history and the ids continue.
	l2, err := NewPersistentLog(10, nil, sink)
	if err != nil {
		t.Fatal(err)
	}
	if got := l2.List(Filter{}); len(got) != 2 {
		t.Fatalf("history lost: %v", got)
	}
	if e := l2.Add(Event{Kind: "c"}); e.ID != 3 {
		t.Fatalf("ids must continue, got %d", e.ID)
	}
}

func TestPersistentLogFailsWhenTheHistoryCannotBeLoaded(t *testing.T) {
	if _, err := NewPersistentLog(10, nil, &memSink{loadErr: errors.New("disk")}); err == nil {
		t.Fatal("want the load error")
	}
}
