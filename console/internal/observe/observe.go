// Package observe turns polling and heartbeats into facts about nodes and
// into events. Nothing here guesses: a fact a probe cannot read stays Unknown.
package observe

import (
	"context"
	"sync"
	"time"

	"k8s.io/utils/clock"

	"github.com/kostiantyn-matsebora/barekube/console/internal/events"
)

// Tri is a three-valued fact. Unknown means "could not be read", never "no".
type Tri int8

const (
	Unknown Tri = iota
	Yes
	No
)

func (t Tri) String() string {
	switch t {
	case Yes:
		return "yes"
	case No:
		return "no"
	}
	return "unknown"
}

// Observation is what one probe round learned about one node.
type Observation struct {
	Node      string    `json:"node"`
	At        time.Time `json:"at"`
	Reachable Tri       `json:"reachable"`
	K3sActive Tri       `json:"k3sActive"`
	Ready     Tri       `json:"ready"`
	Cordoned  Tri       `json:"cordoned"`
}

// NodeState is the derived, display-level state of a node.
type NodeState string

const (
	StateHealthy  NodeState = "healthy"
	StateCordoned NodeState = "cordoned"
	StateNotReady NodeState = "not-ready"
	StateStoodBy  NodeState = "stood-by"
	StateOff      NodeState = "off"
	StateUnknown  NodeState = "unknown"
)

// Derive maps an observation to a state. First match wins: off, stood-by,
// unknown, not-ready, cordoned, healthy.
func Derive(o Observation) NodeState {
	switch {
	case o.Reachable == No:
		return StateOff
	case o.Reachable == Unknown:
		return StateUnknown
	case o.K3sActive == No:
		return StateStoodBy
	case o.K3sActive == Unknown || o.Ready == Unknown || o.Cordoned == Unknown:
		return StateUnknown
	case o.Ready == No:
		return StateNotReady
	case o.Cordoned == Yes:
		return StateCordoned
	}
	return StateHealthy
}

// Prober reads one node. It leaves At unset, the observer stamps it.
type Prober interface {
	Probe(ctx context.Context, node string) Observation
}

// Observer polls every node, keeps the latest observation of each and raises
// an event for every fact that changed.
type Observer struct {
	nodes  []string
	prober Prober
	clk    clock.Clock
	log    *events.Log

	mu     sync.Mutex
	latest map[string]Observation
	subs   []func([]Observation)
	store  Persistence
}

// Persistence keeps the newest observation of each node across restarts.
// Saving is best effort: a failed write never blocks a probe.
type Persistence interface {
	SaveObservation(Observation) error
	LoadObservations() ([]Observation, error)
}

// Restore loads the observations saved before a restart. Only the time is
// kept. Every fact comes back Unknown, because a reading from before the
// restart says nothing about the node now. The page shows when the node was
// last seen, and the first probe replaces the rest.
func (o *Observer) Restore(p Persistence) error {
	saved, err := p.LoadObservations()
	if err != nil {
		return err
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	o.store = p
	known := map[string]bool{}
	for _, n := range o.nodes {
		known[n] = true
	}
	for _, s := range saved {
		if known[s.Node] {
			o.latest[s.Node] = Observation{Node: s.Node, At: s.At}
		}
	}
	return nil
}

// New builds an observer for the nodes, in display order.
func New(nodes []string, p Prober, clk clock.Clock, log *events.Log) *Observer {
	return &Observer{nodes: nodes, prober: p, clk: clk, log: log, latest: map[string]Observation{}}
}

// Subscribe registers fn, called after every round with that round's observations.
func (o *Observer) Subscribe(fn func([]Observation)) {
	o.mu.Lock()
	o.subs = append(o.subs, fn)
	o.mu.Unlock()
}

// Latest returns the newest observation of a node.
func (o *Observer) Latest(node string) (Observation, bool) {
	o.mu.Lock()
	defer o.mu.Unlock()
	obs, ok := o.latest[node]
	return obs, ok
}

// Snapshot returns the newest observation of every node that has one, in node order.
func (o *Observer) Snapshot() []Observation {
	o.mu.Lock()
	defer o.mu.Unlock()
	out := make([]Observation, 0, len(o.nodes))
	for _, n := range o.nodes {
		if obs, ok := o.latest[n]; ok {
			out = append(out, obs)
		}
	}
	return out
}

// Tick probes every node concurrently, records the results, raises events and notifies subscribers.
func (o *Observer) Tick(ctx context.Context) {
	results := make([]Observation, len(o.nodes))
	var wg sync.WaitGroup
	for i, n := range o.nodes {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results[i] = o.prober.Probe(ctx, n)
			results[i].Node = n
		}()
	}
	wg.Wait()
	now := o.clk.Now()
	for i := range results {
		results[i].At = now
		o.record(results[i], events.SourceObserver)
	}
	o.notify(results)
}

// Ingest merges a heartbeat: it knows reachability and k3s only, so Ready and
// Cordoned keep their last observed values.
func (o *Observer) Ingest(node string, k3sActive bool) {
	o.mu.Lock()
	prev := o.latest[node]
	o.mu.Unlock()
	obs := prev
	obs.Node = node
	obs.At = o.clk.Now()
	obs.Reachable = Yes
	obs.K3sActive = No
	if k3sActive {
		obs.K3sActive = Yes
	}
	o.record(obs, events.SourceHeartbeat)
	o.notify([]Observation{obs})
}

func (o *Observer) notify(batch []Observation) {
	o.mu.Lock()
	subs := append([]func([]Observation){}, o.subs...)
	o.mu.Unlock()
	for _, fn := range subs {
		fn(batch)
	}
}

func (o *Observer) record(obs Observation, source string) {
	o.mu.Lock()
	prev := o.latest[obs.Node]
	o.latest[obs.Node] = obs
	p := o.store
	o.mu.Unlock()
	if p != nil {
		_ = p.SaveObservation(obs)
	}
	emit := func(kind string) {
		o.log.Add(events.Event{Kind: kind, Source: source, Node: obs.Node})
	}
	change := func(old, cur Tri, yes, no string) {
		if cur == old || cur == Unknown {
			return
		}
		if cur == Yes {
			emit(yes)
		} else {
			emit(no)
		}
	}
	change(prev.Reachable, obs.Reachable, events.NodeReachable, events.NodeUnreachable)
	change(prev.K3sActive, obs.K3sActive, events.K3sActive, events.K3sInactive)
	change(prev.Ready, obs.Ready, events.NodeReady, events.NodeNotReady)
	change(prev.Cordoned, obs.Cordoned, events.NodeCordoned, events.NodeUncordoned)
}

// Run ticks until ctx ends: every fast while busy reports true, every slow otherwise.
func (o *Observer) Run(ctx context.Context, fast, slow time.Duration, busy func() bool) {
	for {
		o.Tick(ctx)
		d := slow
		if busy != nil && busy() {
			d = fast
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(d):
		}
	}
}
