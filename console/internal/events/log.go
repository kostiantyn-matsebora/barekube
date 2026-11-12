// Package events is console's real event log: an append-only stream of typed
// events that every view (page log, run cards) reads from.
package events

import (
	"sync"
	"time"

	"k8s.io/utils/clock"
)

// Kinds. node.* and k3s.* come from the observer, the rest from runs.
const (
	NodeReachable    = "node.reachable"
	NodeUnreachable  = "node.unreachable"
	K3sActive        = "k3s.active"
	K3sInactive      = "k3s.inactive"
	NodeReady        = "node.ready"
	NodeNotReady     = "node.notready"
	NodeCordoned     = "node.cordoned"
	NodeUncordoned   = "node.uncordoned"
	RunTriggered     = "run.triggered"
	RunStatus        = "run.status"
	RunCompleted     = "run.completed"
	RunFailed        = "run.failed"
	RunRefused       = "run.refused"
	ActionOK         = "action.ok"
	ActionFailed     = "action.failed"
	TimeoutFired     = "timeout.fired"
	ChildCompleted   = "child.completed"
	ChildAwaiting    = "child.awaiting-clearance"
	Clearance        = "clearance"
	SourceObserver   = "observer"
	SourceHeartbeat  = "heartbeat"
	SourceRun        = "run"
	SourceAction     = "action"
	SourceTimeout    = "timeout"
	SourceChild      = "child"
	SourceUI         = "ui"
	SourceAutomation = "automation"
)

// Event is one record in the log. Time is the console's receive time.
type Event struct {
	ID       int64     `json:"id"`
	Time     time.Time `json:"time"`
	Kind     string    `json:"kind"`
	Source   string    `json:"source"`
	Node     string    `json:"node,omitempty"`
	RunID    string    `json:"runId,omitempty"`
	Workflow string    `json:"workflow,omitempty"`
	Detail   string    `json:"detail,omitempty"`
}

// Filter selects events. Empty fields match everything. After skips IDs up to and including it.
type Filter struct {
	Node, RunID, Workflow string
	After                 int64
}

func (f Filter) match(e Event) bool {
	return e.ID > f.After &&
		(f.Node == "" || e.Node == f.Node) &&
		(f.RunID == "" || e.RunID == f.RunID) &&
		(f.Workflow == "" || e.Workflow == f.Workflow)
}

// Log is a bounded, concurrency-safe ring of events.
// Sink persists events. Saving is best effort: a failed write never blocks or
// fails the action that produced the event.
type Sink interface {
	Save(Event) error
	// Load returns the newest max events, oldest first.
	Load(max int) ([]Event, error)
}

type Log struct {
	sink Sink
	mu   sync.Mutex
	clk  clock.Clock
	max  int
	next int64
	buf  []Event
}

// NewLog keeps the newest max events.
func NewLog(max int, clk clock.Clock) *Log {
	if max < 1 {
		max = 1
	}
	if clk == nil {
		clk = clock.RealClock{}
	}
	return &Log{clk: clk, max: max}
}

// Add stamps the event with an ID and the current time and appends it.
// NewPersistentLog is NewLog backed by a sink. It reloads the newest events,
// so ids keep counting up across restarts.
func NewPersistentLog(max int, clk clock.Clock, sink Sink) (*Log, error) {
	l := NewLog(max, clk)
	l.sink = sink
	old, err := sink.Load(l.max)
	if err != nil {
		return nil, err
	}
	l.buf = old
	if n := len(old); n > 0 {
		l.next = old[n-1].ID
	}
	return l, nil
}

func (l *Log) Add(e Event) Event {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.next++
	e.ID = l.next
	e.Time = l.clk.Now()
	l.buf = append(l.buf, e)
	if len(l.buf) > l.max {
		l.buf = append([]Event(nil), l.buf[len(l.buf)-l.max:]...)
	}
	if l.sink != nil {
		_ = l.sink.Save(e)
	}
	return e
}

// List returns matching events, oldest first.
func (l *Log) List(f Filter) []Event {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := []Event{}
	for _, e := range l.buf {
		if f.match(e) {
			out = append(out, e)
		}
	}
	return out
}
