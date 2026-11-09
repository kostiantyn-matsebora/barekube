package flows

import (
	"errors"
	"fmt"
	"sync"
	"time"
)

// RunInfo is the console's view of one run, for the UI and for parents that
// wait on children. Terminal runs stay listed.
type RunInfo struct {
	RunID     string    `json:"runId"`
	Workflow  string    `json:"workflow"`
	Node      string    `json:"node,omitempty"`
	Mode      Mode      `json:"mode,omitempty"`
	Parent    string    `json:"parent,omitempty"`
	By        string    `json:"by,omitempty"`
	Status    string    `json:"status"`
	Terminal  bool      `json:"terminal"`
	Final     string    `json:"final,omitempty"`
	Reason    string    `json:"reason,omitempty"`
	StartedAt time.Time `json:"startedAt"`
	UpdatedAt time.Time `json:"updatedAt"`

	st  St
	key string
}

// Errors returned when a run cannot start.
var (
	ErrUnknownNode     = errors.New("unknown node")
	ErrUnsupported     = errors.New("operation not supported by this node")
	ErrBusy            = errors.New("another workflow is already running")
	ErrClusterRunning  = errors.New("a cluster shutdown or start is in progress")
	ErrClusterActive   = errors.New("a cluster shutdown or start is already running")
	ErrUnknownWorkflow = errors.New("unknown workflow")
	// ErrNothingToDo is returned when the observed state already is the goal.
	// It makes a repeated automation call harmless.
	ErrNothingToDo = errors.New("nothing to do")
)

const clusterKey = "\x00cluster"
const maxHistory = 100

type registry struct {
	mu      sync.Mutex
	runs    []*RunInfo // oldest first
	active  map[string]*RunInfo
	latest  map[string]*RunInfo
	cleared map[string]bool
}

func newRegistry() *registry {
	return &registry{active: map[string]*RunInfo{}, latest: map[string]*RunInfo{}, cleared: map[string]bool{}}
}

// begin reserves the key for a new run. A child run (parent set) may start
// during a cluster shutdown, a plain node run may not.
func (g *registry) begin(info RunInfo) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	// One root workflow at a time. A child run, started by a running parent,
	// is not a root and is never refused on this ground.
	if info.Parent == "" {
		for _, r := range g.active {
			if r.Parent != "" {
				continue
			}
			if r.key == clusterKey {
				if info.key == clusterKey {
					return ErrClusterActive
				}
				return ErrClusterRunning
			}
			return fmt.Errorf("%w: %s on %s is running", ErrBusy, r.Workflow, r.Node)
		}
	}
	if _, busy := g.active[info.key]; busy {
		if info.key == clusterKey {
			return ErrClusterActive
		}
		return ErrBusy
	}
	cp := info
	g.runs = append(g.runs, &cp)
	g.active[info.key] = &cp
	g.latest[info.key] = &cp
	delete(g.cleared, info.key)
	g.trim()
	return nil
}

func (g *registry) trim() {
	for len(g.runs) > maxHistory && g.runs[0].Terminal {
		g.runs = g.runs[1:]
	}
}

// abort drops a reservation whose run never started.
func (g *registry) abort(key string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if a, ok := g.active[key]; ok {
		g.runs = removeRun(g.runs, a)
		delete(g.active, key)
		delete(g.latest, key)
	}
}

func removeRun(rs []*RunInfo, r *RunInfo) []*RunInfo {
	out := rs[:0]
	for _, x := range rs {
		if x != r {
			out = append(out, x)
		}
	}
	return out
}

// update records a status change.
func (g *registry) update(key, runID string, st St, terminal bool, reason string, now time.Time) {
	g.mu.Lock()
	defer g.mu.Unlock()
	r, ok := g.latest[key]
	if !ok {
		return
	}
	r.RunID, r.st, r.Status, r.UpdatedAt = runID, st, st.String(), now
	if reason != "" {
		r.Reason = reason
	}
	if terminal {
		r.Terminal, r.Final = true, finalKind(st)
		if g.active[key] == r {
			delete(g.active, key)
		}
		g.trim()
	}
}

func (g *registry) activeRun(key string) (RunInfo, bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if r, ok := g.active[key]; ok {
		return *r, true
	}
	return RunInfo{}, false
}

func (g *registry) latestRun(key string) (RunInfo, bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if r, ok := g.latest[key]; ok {
		return *r, true
	}
	return RunInfo{}, false
}

func (g *registry) anyActive() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return len(g.active) > 0
}

// list returns every known run, newest first.
func (g *registry) list() []RunInfo {
	g.mu.Lock()
	defer g.mu.Unlock()
	out := make([]RunInfo, 0, len(g.runs))
	for i := len(g.runs) - 1; i >= 0; i-- {
		out = append(out, *g.runs[i])
	}
	return out
}

func (g *registry) clear(node string) {
	g.mu.Lock()
	g.cleared[node] = true
	g.mu.Unlock()
}

func (g *registry) isCleared(node string) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.cleared[node]
}
