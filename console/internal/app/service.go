package app

import (
	"context"
	"errors"
	"sync"
	"time"

	"k8s.io/utils/clock"

	"github.com/kostiantyn-matsebora/barekube/console/internal/config"
	"github.com/kostiantyn-matsebora/barekube/console/internal/events"
	"github.com/kostiantyn-matsebora/barekube/console/internal/flows"
	"github.com/kostiantyn-matsebora/barekube/console/internal/nodeagent"
	"github.com/kostiantyn-matsebora/barekube/console/internal/observe"
	"github.com/kostiantyn-matsebora/barekube/console/internal/store"
)

// ErrUnknownNode is returned for a heartbeat from a node not in the config.
var ErrUnknownNode = errors.New("unknown node")

// Service is everything the web layer needs: the node view, the runs, the
// event log, and the actions that start a workflow.
type Service struct {
	cfg      *config.Config
	Log      *events.Log
	Observer *observe.Observer
	Engine   *flows.Engine
	clk      clock.Clock

	ops *Ops

	mu         sync.Mutex
	heartbeats map[string]time.Time

	platMu sync.Mutex
	plat   platformCache
}

// NewService wires the observer and the engine over the node-agents.
func NewService(cfg *config.Config, agents map[string]nodeagent.Client, clk clock.Clock) (*Service, error) {
	return NewServiceWithStore(cfg, agents, clk, nil)
}

// NewServiceWithStore is NewService with state kept in db. A nil db keeps it
// in memory, which is for tests only.
func NewServiceWithStore(cfg *config.Config, agents map[string]nodeagent.Client, clk clock.Clock, db *store.DB) (*Service, error) {
	if clk == nil {
		clk = clock.RealClock{}
	}
	log := events.NewLog(2000, clk)
	if db != nil {
		var err error
		if log, err = events.NewPersistentLog(2000, clk, db.Events()); err != nil {
			return nil, err
		}
	}
	var names []string
	for _, n := range cfg.Nodes {
		names = append(names, n.Name)
	}
	obs := observe.New(names, NewProber(cfg, agents), clk, log)
	if db != nil {
		if err := obs.Restore(db.Observations()); err != nil {
			return nil, err
		}
	}
	ops := NewOps(cfg, agents)
	eng, err := flows.New(cfg, ops, obs, log, flows.Options{Clock: clk, Store: db})
	if err != nil {
		return nil, err
	}
	obs.Subscribe(eng.OnObservations)
	return &Service{cfg: cfg, Log: log, Observer: obs, Engine: eng, clk: clk, ops: ops, heartbeats: map[string]time.Time{}}, nil
}

// Start runs the workflows and the observer until ctx ends.
func (s *Service) Start(ctx context.Context) {
	s.Engine.Run(ctx)
	go s.Observer.Run(ctx, s.cfg.Timing.ObserveFast, s.cfg.Timing.ObserveSlow, s.Engine.Busy)
}

// Heartbeat records a node's push and feeds it to the observer.
func (s *Service) Heartbeat(node string, k3sActive bool) error {
	if _, ok := s.cfg.Node(node); !ok {
		return ErrUnknownNode
	}
	s.mu.Lock()
	s.heartbeats[node] = s.clk.Now()
	s.mu.Unlock()
	s.Observer.Ingest(node, k3sActive)
	return nil
}

// NodeView is one row of the node table.
type NodeView struct {
	Name       string         `json:"name"`
	Role       string         `json:"role"`
	State      string         `json:"state"`
	Reachable  string         `json:"reachable"`
	K3s        string         `json:"k3s"`
	Ready      string         `json:"ready"`
	Cordoned   string         `json:"cordoned"`
	ObservedAt *time.Time     `json:"observedAt,omitempty"`
	Heartbeat  *time.Time     `json:"heartbeat,omitempty"`
	Operations []string       `json:"operations"`
	Run        *flows.RunInfo `json:"run,omitempty"`
}

var allOps = []config.Operation{config.OpReboot, config.OpStandBy, config.OpPowerOff, config.OpRejoin}

// Nodes returns the table: derived state from observations, never a stored
// label, plus the active (or latest) run of each node.
func (s *Service) Nodes() []NodeView {
	runs := map[string]flows.RunInfo{}
	for _, r := range s.Engine.Runs() {
		if r.Node == "" {
			continue
		}
		if _, seen := runs[r.Node]; !seen {
			runs[r.Node] = r
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]NodeView, 0, len(s.cfg.Nodes))
	for _, n := range s.cfg.Nodes {
		v := NodeView{Name: n.Name, Role: string(n.Role), State: string(observe.StateUnknown),
			Reachable: observe.Unknown.String(), K3s: observe.Unknown.String(), Ready: observe.Unknown.String(), Cordoned: observe.Unknown.String(), Operations: []string{}}
		if o, ok := s.Observer.Latest(n.Name); ok {
			at := o.At
			v.State, v.ObservedAt = string(observe.Derive(o)), &at
			v.Reachable, v.K3s, v.Ready, v.Cordoned = o.Reachable.String(), o.K3sActive.String(), o.Ready.String(), o.Cordoned.String()
		}
		if hb, ok := s.heartbeats[n.Name]; ok {
			t := hb
			v.Heartbeat = &t
		}
		for _, op := range allOps {
			if n.Supports(op) {
				v.Operations = append(v.Operations, string(op))
			}
		}
		if r, ok := runs[n.Name]; ok {
			rr := r
			v.Run = &rr
		}
		out = append(out, v)
	}
	return out
}

// Runs lists every run, newest first.
func (s *Service) Runs() []flows.RunInfo { return s.Engine.Runs() }

// Workflows returns each workflow's diagram.
func (s *Service) Workflows() []flows.WorkflowInfo { return s.Engine.Workflows() }

// Events lists the matching events, oldest first.
func (s *Service) Events(f events.Filter) []events.Event { return s.Log.List(f) }

// StartNode starts a node workflow.
func (s *Service) StartNode(ctx context.Context, wf, node string, mode flows.Mode, by string) (flows.RunInfo, error) {
	return s.Engine.StartNode(ctx, wf, node, mode, by)
}

// StartCluster starts the cluster shutdown.
func (s *Service) StartCluster(ctx context.Context, by string) (flows.RunInfo, error) {
	return s.Engine.StartCluster(ctx, by)
}

// StartClusterStart starts the cold start of a cluster that is fully off.
func (s *Service) StartClusterStart(ctx context.Context, by string) (flows.RunInfo, error) {
	return s.Engine.StartClusterStart(ctx, by)
}

// StartVipRestore starts the platform-level repair of the API VIP.
func (s *Service) StartVipRestore(ctx context.Context, by string) (flows.RunInfo, error) {
	return s.Engine.StartVipRestore(ctx, by)
}
