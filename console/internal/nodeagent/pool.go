package nodeagent

import (
	"context"
	"fmt"
	"time"
)

// MasterPool routes kubectl-ish calls to whichever configured master
// answers first, in order — any master can manage the whole cluster, not
// just itself, so which one answers doesn't matter for correctness.
type MasterPool struct {
	Masters []Client // in try-order
}

// each tries every configured master in order, but ONLY fails over to the
// next one when the current master was genuinely unreachable
// (IsUnreachable, client.go) — a master that was reached and gave an
// authoritative answer (even an error, e.g. a real drain failure) is NOT
// retried on another master: that real-world failure would just repeat
// identically there, multiplying however long the operation's own
// timeout is for zero benefit. Confirmed live, 2026-10-11 (see
// UnreachableError's doc comment for the exact incident).
func (p *MasterPool) each(ctx context.Context, fn func(Client) error) error {
	if len(p.Masters) == 0 {
		return fmt.Errorf("no masters configured to route through")
	}
	var lastErr error
	for _, m := range p.Masters {
		err := fn(m)
		if err == nil {
			return nil
		}
		if !IsUnreachable(err) {
			return err
		}
		lastErr = err
	}
	return fmt.Errorf("all masters unreachable: %w", lastErr)
}

func (p *MasterPool) Cordon(ctx context.Context, node string) error {
	return p.each(ctx, func(c Client) error { return c.Cordon(ctx, node) })
}

func (p *MasterPool) Uncordon(ctx context.Context, node string) error {
	return p.each(ctx, func(c Client) error { return c.Uncordon(ctx, node) })
}

func (p *MasterPool) Cordoned(ctx context.Context, node string) (bool, error) {
	var cordoned bool
	err := p.each(ctx, func(c Client) error {
		v, err := c.Cordoned(ctx, node)
		cordoned = v
		return err
	})
	return cordoned, err
}

func (p *MasterPool) Drain(ctx context.Context, node string, ignoreDaemonSets, deleteEmptyDirData bool, timeout time.Duration) error {
	return p.each(ctx, func(c Client) error { return c.Drain(ctx, node, ignoreDaemonSets, deleteEmptyDirData, timeout) })
}

func (p *MasterPool) NodeReady(ctx context.Context, node string) (bool, error) {
	var ready bool
	err := p.each(ctx, func(c Client) error {
		r, err := c.NodeReady(ctx, node)
		ready = r
		return err
	})
	return ready, err
}

func (p *MasterPool) SetLonghornScheduling(ctx context.Context, node string, allow bool) error {
	return p.each(ctx, func(c Client) error { return c.SetLonghornScheduling(ctx, node, allow) })
}

func (p *MasterPool) ZeroReplicaRisk(ctx context.Context, node string) ([]string, error) {
	var volumes []string
	err := p.each(ctx, func(c Client) error {
		v, err := c.ZeroReplicaRisk(ctx, node)
		volumes = v
		return err
	})
	return volumes, err
}

func (p *MasterPool) EtcdSnapshot(ctx context.Context) error {
	return p.each(ctx, func(c Client) error { return c.EtcdSnapshot(ctx) })
}

func (p *MasterPool) LonghornAvailable(ctx context.Context) (bool, error) {
	var available bool
	err := p.each(ctx, func(c Client) error {
		a, err := c.LonghornAvailable(ctx)
		available = a
		return err
	})
	return available, err
}

func (p *MasterPool) ClusterProbe(ctx context.Context, vip string) (ClusterProbeResponse, error) {
	var out ClusterProbeResponse
	err := p.each(ctx, func(c Client) error {
		v, err := c.ClusterProbe(ctx, vip)
		out = v
		return err
	})
	return out, err
}

func (p *MasterPool) LonghornHealth(ctx context.Context) (LonghornHealthResponse, error) {
	var out LonghornHealthResponse
	err := p.each(ctx, func(c Client) error {
		v, err := c.LonghornHealth(ctx)
		out = v
		return err
	})
	return out, err
}

func (p *MasterPool) VaultSealed(ctx context.Context, namespace string) (VaultSealedResponse, error) {
	var out VaultSealedResponse
	err := p.each(ctx, func(c Client) error {
		v, err := c.VaultSealed(ctx, namespace)
		out = v
		return err
	})
	return out, err
}
