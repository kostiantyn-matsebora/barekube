// Package app wires the node-agents to the workflow engine: it is the one
// place that knows how a workflow step becomes a node-agent call and how a
// node is probed.
package app

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/kostiantyn-matsebora/barekube/console/internal/config"
	"github.com/kostiantyn-matsebora/barekube/console/internal/nodeagent"
)

// Ops implements flows.Ops over the node-agents. Calls that act on the node
// itself go to its own agent. Calls that need the Kubernetes API go through
// whichever master answers.
type Ops struct {
	agents  map[string]nodeagent.Client
	masters []config.Node
	pool    *nodeagent.MasterPool
}

// NewOps builds the adapter. Masters are tried in configuration order.
func NewOps(cfg *config.Config, agents map[string]nodeagent.Client) *Ops {
	o := &Ops{agents: agents, masters: cfg.Masters(), pool: &nodeagent.MasterPool{}}
	for _, m := range o.masters {
		if a, ok := agents[m.Name]; ok {
			o.pool.Masters = append(o.pool.Masters, a)
		}
	}
	return o
}

func (o *Ops) agent(node string) (nodeagent.Client, error) {
	a, ok := o.agents[node]
	if !ok {
		return nil, fmt.Errorf("no node-agent client for %q", node)
	}
	return a, nil
}

// QuorumOK is true only when every master's apiserver reports ready.
func (o *Ops) QuorumOK(ctx context.Context) bool {
	if len(o.masters) == 0 {
		return false
	}
	for _, m := range o.masters {
		a, ok := o.agents[m.Name]
		if !ok || !a.EtcdReadyz(ctx) {
			return false
		}
	}
	return true
}

// QuorumMajority is true when most masters' apiservers report ready. A cold
// start only needs a majority: etcd elects a leader with two of three. Each
// master is asked under its own short deadline, since a master still powered
// off would otherwise hold the call for as long as a TCP connect takes to fail.
func (o *Ops) QuorumMajority(ctx context.Context) bool {
	if len(o.masters) == 0 {
		return false
	}
	ready := 0
	for _, m := range o.masters {
		a, ok := o.agents[m.Name]
		if !ok {
			continue
		}
		c, cancel := context.WithTimeout(ctx, 5*time.Second)
		if a.EtcdReadyz(c) {
			ready++
		}
		cancel()
	}
	return ready >= len(o.masters)/2+1
}

func (o *Ops) ClusterProbe(ctx context.Context, vip string) (nodeagent.ClusterProbeResponse, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	return o.pool.ClusterProbe(ctx, vip)
}

func (o *Ops) VipBootstrap(ctx context.Context, node string, on bool) error {
	a, err := o.agent(node)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	return a.VipBootstrap(ctx, on)
}

func (o *Ops) LonghornHealth(ctx context.Context) (nodeagent.LonghornHealthResponse, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	return o.pool.LonghornHealth(ctx)
}

func (o *Ops) VaultSealed(ctx context.Context, namespace string) (nodeagent.VaultSealedResponse, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	return o.pool.VaultSealed(ctx, namespace)
}

func (o *Ops) Cordon(ctx context.Context, node string) error { return o.pool.Cordon(ctx, node) }

func (o *Ops) Uncordon(ctx context.Context, node string) error { return o.pool.Uncordon(ctx, node) }

// LonghornScheduling is skipped where Longhorn is not installed.
func (o *Ops) LonghornScheduling(ctx context.Context, node string, allow bool) error {
	if ok, err := o.pool.LonghornAvailable(ctx); err == nil && !ok {
		return nil
	}
	return o.pool.SetLonghornScheduling(ctx, node, allow)
}

func (o *Ops) ZeroReplicaRisk(ctx context.Context, node string) ([]string, error) {
	if ok, err := o.pool.LonghornAvailable(ctx); err == nil && !ok {
		return nil, nil
	}
	return o.pool.ZeroReplicaRisk(ctx, node)
}

func (o *Ops) Drain(ctx context.Context, node string, timeout time.Duration) error {
	return o.pool.Drain(ctx, node, true, true, timeout)
}

func (o *Ops) Reboot(ctx context.Context, node string) error {
	a, err := o.agent(node)
	if err != nil {
		return err
	}
	return a.Reboot(ctx)
}

func (o *Ops) StopK3s(ctx context.Context, node string) error {
	a, err := o.agent(node)
	if err != nil {
		return err
	}
	return a.StopK3s(ctx)
}

func (o *Ops) KillK3s(ctx context.Context, node string) error {
	a, err := o.agent(node)
	if err != nil {
		return err
	}
	return a.KillK3s(ctx)
}

func (o *Ops) StartK3s(ctx context.Context, node string) error {
	a, err := o.agent(node)
	if err != nil {
		return err
	}
	return a.StartK3s(ctx)
}

func (o *Ops) PowerOff(ctx context.Context, node string, force bool) error {
	a, err := o.agent(node)
	if err != nil {
		return err
	}
	return a.PowerOff(ctx, force)
}

// EtcdSnapshot saves a snapshot on the first master that answers. A snapshot
// of any member holds the whole cluster state. The call is bounded so a hung
// master cannot hold the shutdown.
func (o *Ops) EtcdSnapshot(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	return o.pool.EtcdSnapshot(ctx)
}

// EtcdLeader asks each master whether it leads. An unreachable master is
// skipped, no answer at all is an error.
func (o *Ops) EtcdLeader(ctx context.Context) (string, error) {
	for _, m := range o.masters {
		a, ok := o.agents[m.Name]
		if !ok {
			continue
		}
		if leader, err := a.EtcdIsLeader(ctx); err == nil && leader {
			return m.Name, nil
		}
	}
	return "", errors.New("no master reported itself etcd leader")
}
