package app

import (
	"context"
	"time"

	"github.com/kostiantyn-matsebora/barekube/console/internal/config"
	"github.com/kostiantyn-matsebora/barekube/console/internal/nodeagent"
	"github.com/kostiantyn-matsebora/barekube/console/internal/observe"
)

// Prober reads one node: its own agent for reachability and k3s, the
// Kubernetes API for Ready and cordoned.
type Prober struct {
	agents map[string]nodeagent.Client
	pool   *nodeagent.MasterPool
	// Timeout bounds one probe call so a dead node cannot stall a round.
	Timeout time.Duration
}

// NewProber builds the prober for the configured nodes.
func NewProber(cfg *config.Config, agents map[string]nodeagent.Client) *Prober {
	p := &Prober{agents: agents, pool: &nodeagent.MasterPool{}, Timeout: 4 * time.Second}
	for _, m := range cfg.Masters() {
		if a, ok := agents[m.Name]; ok {
			p.pool.Masters = append(p.pool.Masters, a)
		}
	}
	return p
}

func tri(v bool, err error) observe.Tri {
	switch {
	case err != nil:
		return observe.Unknown
	case v:
		return observe.Yes
	}
	return observe.No
}

// Probe never guesses. An unreachable node reports only that, because a
// stopped agent says nothing about k3s, and the API can lag a dead node.
func (p *Prober) Probe(ctx context.Context, node string) observe.Observation {
	ctx, cancel := context.WithTimeout(ctx, p.Timeout)
	defer cancel()
	a, ok := p.agents[node]
	if !ok {
		return observe.Observation{Node: node}
	}
	if !a.Reachable(ctx) {
		return observe.Observation{Node: node, Reachable: observe.No}
	}
	o := observe.Observation{Node: node, Reachable: observe.Yes}
	o.K3sActive = tri(a.K3sActive(ctx))
	o.Ready = tri(p.pool.NodeReady(ctx, node))
	o.Cordoned = tri(p.pool.Cordoned(ctx, node))
	return o
}
