package observe

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"k8s.io/utils/clock"
	clocktesting "k8s.io/utils/clock/testing"

	"github.com/kostiantyn-matsebora/barekube/console/internal/events"
)

type fakeProber struct {
	mu   sync.Mutex
	next map[string]Observation
	n    int
}

func (f *fakeProber) Probe(_ context.Context, node string) Observation {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.n++
	return f.next[node]
}

func (f *fakeProber) set(node string, o Observation) {
	f.mu.Lock()
	f.next[node] = o
	f.mu.Unlock()
}

func healthy() Observation {
	return Observation{Reachable: Yes, K3sActive: Yes, Ready: Yes, Cordoned: No}
}

func newObs(t *testing.T) (*Observer, *fakeProber, *events.Log, *clocktesting.FakeClock) {
	t.Helper()
	clk := clocktesting.NewFakeClock(time.Date(2026, 10, 31, 0, 0, 0, 0, time.UTC))
	log := events.NewLog(100, clk)
	p := &fakeProber{next: map[string]Observation{"a": healthy(), "b": healthy()}}
	return New([]string{"a", "b"}, p, clk, log), p, log, clk
}

func kinds(l *events.Log, node string) []string {
	var out []string
	for _, e := range l.List(events.Filter{Node: node}) {
		out = append(out, e.Kind)
	}
	return out
}

func TestTriString(t *testing.T) {
	if Yes.String() != "yes" || No.String() != "no" || Unknown.String() != "unknown" {
		t.Fatal("tri strings")
	}
}

func TestDerive(t *testing.T) {
	cases := []struct {
		name string
		o    Observation
		want NodeState
	}{
		{"off", Observation{Reachable: No, K3sActive: Yes, Ready: Yes, Cordoned: No}, StateOff},
		{"reach unknown", Observation{Reachable: Unknown}, StateUnknown},
		{"stood by", Observation{Reachable: Yes, K3sActive: No, Ready: Yes}, StateStoodBy},
		{"k3s unknown", Observation{Reachable: Yes, K3sActive: Unknown}, StateUnknown},
		{"ready unknown", Observation{Reachable: Yes, K3sActive: Yes, Ready: Unknown, Cordoned: No}, StateUnknown},
		{"cordon unknown", Observation{Reachable: Yes, K3sActive: Yes, Ready: Yes, Cordoned: Unknown}, StateUnknown},
		{"not ready", Observation{Reachable: Yes, K3sActive: Yes, Ready: No, Cordoned: No}, StateNotReady},
		{"cordoned", Observation{Reachable: Yes, K3sActive: Yes, Ready: Yes, Cordoned: Yes}, StateCordoned},
		{"healthy", healthy(), StateHealthy},
	}
	for _, c := range cases {
		if got := Derive(c.o); got != c.want {
			t.Errorf("%s: got %s want %s", c.name, got, c.want)
		}
	}
}

func TestTickRecordsStampsAndEmitsInitialFacts(t *testing.T) {
	o, _, log, clk := newObs(t)
	var got [][]Observation
	o.Subscribe(func(b []Observation) { got = append(got, b) })
	o.Tick(context.Background())
	obs, ok := o.Latest("a")
	if !ok || obs.Node != "a" || !obs.At.Equal(clk.Now()) {
		t.Fatalf("latest = %+v ok=%v", obs, ok)
	}
	if len(got) != 1 || len(got[0]) != 2 {
		t.Fatalf("subscriber batches = %+v", got)
	}
	want := []string{events.NodeReachable, events.K3sActive, events.NodeReady, events.NodeUncordoned}
	if k := kinds(log, "a"); len(k) != 4 {
		t.Fatalf("initial events %v want %v", k, want)
	}
	if _, ok := o.Latest("zzz"); ok {
		t.Fatal("unknown node has an observation")
	}
}

func TestOnlyChangesEmitEvents(t *testing.T) {
	o, p, log, _ := newObs(t)
	o.Tick(context.Background())
	before := len(log.List(events.Filter{}))
	o.Tick(context.Background())
	if n := len(log.List(events.Filter{})); n != before {
		t.Fatalf("unchanged tick emitted %d events", n-before)
	}
	p.set("a", Observation{Reachable: No, K3sActive: Unknown, Ready: Yes, Cordoned: No})
	o.Tick(context.Background())
	k := kinds(log, "a")
	if k[len(k)-1] != events.NodeUnreachable {
		t.Fatalf("events %v", k)
	}
	// Unknown facts never emit.
	for _, kind := range k {
		if kind == events.K3sInactive {
			t.Fatal("unknown k3s raised an event")
		}
	}
	p.set("a", Observation{Reachable: Yes, K3sActive: No, Ready: No, Cordoned: Yes})
	o.Tick(context.Background())
	k = kinds(log, "a")
	for _, want := range []string{events.NodeReachable, events.K3sInactive, events.NodeNotReady, events.NodeCordoned} {
		found := false
		for _, kind := range k {
			found = found || kind == want
		}
		if !found {
			t.Errorf("missing %s in %v", want, k)
		}
	}
}

func TestSnapshotKeepsNodeOrderAndSkipsUnobserved(t *testing.T) {
	o, _, _, _ := newObs(t)
	if len(o.Snapshot()) != 0 {
		t.Fatal("snapshot before any tick")
	}
	o.Tick(context.Background())
	s := o.Snapshot()
	if len(s) != 2 || s[0].Node != "a" || s[1].Node != "b" {
		t.Fatalf("snapshot %+v", s)
	}
}

func TestIngestKeepsClusterFacts(t *testing.T) {
	o, p, log, clk := newObs(t)
	p.set("a", Observation{Reachable: No, K3sActive: Unknown, Ready: Yes, Cordoned: Yes})
	o.Tick(context.Background())
	var batches [][]Observation
	o.Subscribe(func(b []Observation) { batches = append(batches, b) })
	clk.Step(time.Second)
	o.Ingest("a", false)
	obs, _ := o.Latest("a")
	if obs.Reachable != Yes || obs.K3sActive != No || obs.Ready != Yes || obs.Cordoned != Yes {
		t.Fatalf("ingest merged wrong: %+v", obs)
	}
	if len(batches) != 1 || len(batches[0]) != 1 {
		t.Fatalf("batches %+v", batches)
	}
	heartbeat := 0
	for _, e := range log.List(events.Filter{Node: "a"}) {
		if e.Source == events.SourceHeartbeat {
			heartbeat++
		}
	}
	if heartbeat == 0 {
		t.Fatal("heartbeat events carry no heartbeat source")
	}
	o.Ingest("a", true)
	if obs, _ := o.Latest("a"); obs.K3sActive != Yes {
		t.Fatal("k3s active not ingested")
	}
}

func TestRunTicksFastWhileBusyAndStopsOnCancel(t *testing.T) {
	o, p, _, _ := newObs(t)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		o.Run(ctx, time.Millisecond, time.Hour, func() bool { return true })
		close(done)
	}()
	deadline := time.After(2 * time.Second)
	for {
		p.mu.Lock()
		n := p.n
		p.mu.Unlock()
		if n >= 6 {
			break
		}
		select {
		case <-deadline:
			t.Fatal("observer never ticked fast")
		case <-time.After(time.Millisecond):
		}
	}
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not stop")
	}
}

func TestRunUsesSlowIntervalAtRest(t *testing.T) {
	o, p, _, _ := newObs(t)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { o.Run(ctx, time.Millisecond, time.Hour, nil); close(done) }()
	time.Sleep(50 * time.Millisecond)
	cancel()
	<-done
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.n != 2 {
		t.Fatalf("one round expected at rest (2 probes), got %d", p.n)
	}
}

type memPersist struct {
	saved   map[string]Observation
	loadErr error
	saveErr error
}

func (m *memPersist) SaveObservation(o Observation) error {
	if m.saveErr != nil {
		return m.saveErr
	}
	m.saved[o.Node] = o
	return nil
}
func (m *memPersist) LoadObservations() ([]Observation, error) {
	if m.loadErr != nil {
		return nil, m.loadErr
	}
	var out []Observation
	for _, o := range m.saved {
		out = append(out, o)
	}
	return out, nil
}

func TestRestoreKeepsOnlyTheTimeAndEveryFactComesBackUnknown(t *testing.T) {
	seen := time.Date(2026, 11, 1, 3, 0, 0, 0, time.UTC)
	p := &memPersist{saved: map[string]Observation{
		"a":     {Node: "a", At: seen, Reachable: Yes, K3sActive: Yes, Ready: Yes, Cordoned: No},
		"ghost": {Node: "ghost", At: seen, Reachable: Yes},
	}}
	o := New([]string{"a", "b"}, nil, clock.RealClock{}, events.NewLog(10, nil))
	if err := o.Restore(p); err != nil {
		t.Fatal(err)
	}
	got, ok := o.Latest("a")
	if !ok || !got.At.Equal(seen) || got.Reachable != Unknown || got.K3sActive != Unknown || got.Ready != Unknown || got.Cordoned != Unknown {
		t.Fatalf("a stale reading must come back unknown: %+v", got)
	}
	if Derive(got) != StateUnknown {
		t.Fatalf("derived %s", Derive(got))
	}
	if _, ok := o.Latest("ghost"); ok {
		t.Fatal("a node not in the config must not appear")
	}
	if _, ok := o.Latest("b"); ok {
		t.Fatal("a node never seen stays unobserved")
	}
	// New observations are written through.
	o.Ingest("a", true)
	if p.saved["a"].K3sActive != Yes || p.saved["a"].At.Equal(seen) {
		t.Fatalf("not written through: %+v", p.saved["a"])
	}
	// A failing write never breaks a probe.
	p.saveErr = errors.New("disk")
	o.Ingest("a", false)
	if l, _ := o.Latest("a"); l.K3sActive != No {
		t.Fatal("a failed save must not lose the observation")
	}
}

func TestRestoreReportsALoadError(t *testing.T) {
	o := New([]string{"a"}, nil, clock.RealClock{}, events.NewLog(10, nil))
	if err := o.Restore(&memPersist{loadErr: errors.New("disk")}); err == nil {
		t.Fatal("want the error")
	}
}
