package flows

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/luno/workflow"

	"github.com/kostiantyn-matsebora/barekube/console/internal/config"
	"github.com/kostiantyn-matsebora/barekube/console/internal/observe"
)

// Run is one workflow run of a Job.
type Run = workflow.Run[Job, St]

// payload is what a wait receives: the latest observation of the node. A
// cluster wait receives an empty one and reads its children from the registry.
type payload struct {
	observe.Observation
}

type stepFn func(ctx context.Context, r *Run) St
type eventFn func(ctx context.Context, r *Run, p payload) St
type timeoutFn func(ctx context.Context, r *Run) St

// EdgeKind says how a transition is drawn and which handler may take it.
type EdgeKind int

const (
	// Event is an observed event or an accepted action: a solid edge.
	Event EdgeKind = iota
	// Fail is a failed action: a dotted edge out of a step.
	Fail
	// Timeout is an expired wait: a dotted edge out of a wait.
	Timeout
)

// Edge is one declared transition. A handler may only return a declared one.
type Edge struct {
	From, To St
	Label    string
	Kind     EdgeKind
}

// Wait is a status resolved only by an observed event, with a deadline.
type Wait struct {
	Timer     func(t config.Timing, j *Job) time.Duration
	OnEvent   eventFn
	OnTimeout timeoutFn
	// Fresh requires the observation to be newer than the moment the run
	// entered the status, so a stale reading can never resolve a wait.
	Fresh bool
}

// Spec declares a workflow: its graph and the handlers behind each status.
// The same declaration builds the workflow and draws its diagram.
type Spec struct {
	Name  string
	Edges []Edge
	Steps map[St]stepFn
	Waits map[St]Wait
}

func (s Spec) outgoing(from St) []Edge {
	var out []Edge
	for _, e := range s.Edges {
		if e.From == from {
			out = append(out, e)
		}
	}
	return out
}

func (s Spec) dests(from St, timeout bool) []St {
	var out []St
	for _, e := range s.outgoing(from) {
		if timeout == (e.Kind == Timeout) && !slices.Contains(out, e.To) {
			out = append(out, e.To)
		}
	}
	return out
}

// Terminal reports whether no edge leaves the status.
func (s Spec) Terminal(st St) bool { return len(s.outgoing(st)) == 0 }

// Statuses lists every status in the graph, in first-mention order.
func (s Spec) Statuses() []St {
	var out []St
	add := func(st St) {
		if !slices.Contains(out, st) {
			out = append(out, st)
		}
	}
	for _, e := range s.Edges {
		add(e.From)
		add(e.To)
	}
	return out
}

// Start is the one status no edge leads to.
func (s Spec) Start() St {
	for _, st := range s.Statuses() {
		if !slices.ContainsFunc(s.Edges, func(e Edge) bool { return e.To == st }) {
			return st
		}
	}
	return 0
}

// WaitStatuses lists the statuses resolved by observations.
func (s Spec) WaitStatuses() []St {
	var out []St
	for _, st := range s.Statuses() {
		if _, ok := s.Waits[st]; ok {
			out = append(out, st)
		}
	}
	return out
}

// Validate checks the declaration is a runnable graph.
func (s Spec) Validate() error {
	if len(s.Edges) == 0 {
		return fmt.Errorf("%s: no edges", s.Name)
	}
	starts := 0
	for _, st := range s.Statuses() {
		if !slices.ContainsFunc(s.Edges, func(e Edge) bool { return e.To == st }) {
			starts++
		}
		_, isStep := s.Steps[st]
		w, isWait := s.Waits[st]
		switch {
		case s.Terminal(st):
			if isStep || isWait {
				return fmt.Errorf("%s: terminal %s has a handler", s.Name, st)
			}
		case isStep == isWait:
			return fmt.Errorf("%s: %s must be exactly one of step or wait", s.Name, st)
		case isWait:
			if len(s.dests(st, true)) == 0 || len(s.dests(st, false)) == 0 {
				return fmt.Errorf("%s: wait %s needs event and timeout edges", s.Name, st)
			}
			if w.Timer == nil || w.OnEvent == nil || w.OnTimeout == nil {
				return fmt.Errorf("%s: wait %s is missing a handler", s.Name, st)
			}
		case len(s.dests(st, true)) > 0:
			return fmt.Errorf("%s: step %s has a timeout edge", s.Name, st)
		}
	}
	if starts != 1 {
		return fmt.Errorf("%s: want exactly one start status, found %d", s.Name, starts)
	}
	return nil
}

var terminalClass = map[string]string{"completed": "done", "refused": "fail", "failed": "fail"}

// Mermaid draws the graph: boxes are steps, hexagons are waits, rounded
// shapes are terminal statuses, dotted edges are failures and timeouts.
func (s Spec) Mermaid() string {
	var b strings.Builder
	b.WriteString("flowchart TD\n")
	node := func(st St) string {
		switch {
		case st == s.Start():
			return fmt.Sprintf("%s([%s])", st, st)
		case s.Terminal(st):
			class := terminalClass[finalKind(st)]
			if st == StoodBy || st == PoweredOff {
				class = "rest"
			}
			return fmt.Sprintf("%s([%s]):::%s", st, st, class)
		}
		if _, ok := s.Waits[st]; ok {
			return fmt.Sprintf("%s{{%s}}:::wait", st, st)
		}
		return fmt.Sprintf("%s[%s]", st, st)
	}
	for _, e := range s.Edges {
		arrow := "-->"
		if e.Kind != Event {
			arrow = "-.->"
		}
		fmt.Fprintf(&b, "  %s %s|\"%s\"| %s\n", node(e.From), arrow, e.Label, node(e.To))
	}
	b.WriteString("  classDef wait fill:#fff7e0,stroke:#b8860b,stroke-width:1.5px,color:#111\n")
	b.WriteString("  classDef done fill:#e3efe9,stroke:#1f6f5a,stroke-width:2px,color:#111\n")
	b.WriteString("  classDef fail fill:#fde8e8,stroke:#b91c1c,stroke-width:2px,color:#111\n")
	b.WriteString("  classDef rest fill:#eef0f3,stroke:#6b7280,stroke-width:2px,color:#111\n")
	return b.String()
}
