package flows

import (
	"context"
	"time"

	"github.com/kostiantyn-matsebora/barekube/console/internal/events"
	"github.com/kostiantyn-matsebora/barekube/console/internal/nodeagent"
)

// Ops is everything a workflow step may do to the cluster. Every method is one
// node-agent call, so a test replaces the whole cluster with one fake.
type Ops interface {
	QuorumOK(ctx context.Context) bool
	Cordon(ctx context.Context, node string) error
	Uncordon(ctx context.Context, node string) error
	LonghornScheduling(ctx context.Context, node string, allow bool) error
	ZeroReplicaRisk(ctx context.Context, node string) ([]string, error)
	Drain(ctx context.Context, node string, timeout time.Duration) error
	Reboot(ctx context.Context, node string) error
	StopK3s(ctx context.Context, node string) error
	KillK3s(ctx context.Context, node string) error
	StartK3s(ctx context.Context, node string) error
	PowerOff(ctx context.Context, node string, force bool) error
	EtcdLeader(ctx context.Context) (string, error)
	EtcdSnapshot(ctx context.Context) error

	// Cluster start. The reads (QuorumMajority, ClusterProbe, LonghornHealth,
	// VaultSealed) run on every observation round and are not logged one by
	// one; the one write, VipBootstrap, is.
	QuorumMajority(ctx context.Context) bool
	ClusterProbe(ctx context.Context, vip string) (nodeagent.ClusterProbeResponse, error)
	VipBootstrap(ctx context.Context, node string, on bool) error
	LonghornHealth(ctx context.Context) (nodeagent.LonghornHealthResponse, error)
	VaultSealed(ctx context.Context, namespace string) (nodeagent.VaultSealedResponse, error)
}

type runRef struct{ RunID, Workflow, Node string }

type refKey struct{}

func withRef(ctx context.Context, r runRef) context.Context {
	return context.WithValue(ctx, refKey{}, r)
}

func refOf(ctx context.Context) runRef {
	r, _ := ctx.Value(refKey{}).(runRef)
	return r
}

// loggedOps records an action.ok or action.failed event for every call.
type loggedOps struct {
	Ops
	log *events.Log
}

func (l loggedOps) record(ctx context.Context, action, node string, err error) {
	r := refOf(ctx)
	if node == "" {
		node = r.Node
	}
	e := events.Event{Kind: events.ActionOK, Source: events.SourceAction, Node: node, RunID: r.RunID, Workflow: r.Workflow, Detail: action}
	if err != nil {
		e.Kind = events.ActionFailed
		e.Detail = action + ": " + err.Error()
	}
	l.log.Add(e)
}

func (l loggedOps) QuorumOK(ctx context.Context) bool {
	ok := l.Ops.QuorumOK(ctx)
	var err error
	if !ok {
		err = errQuorum
	}
	l.record(ctx, "etcd quorum check", "", err)
	return ok
}

func (l loggedOps) Cordon(ctx context.Context, n string) error {
	err := l.Ops.Cordon(ctx, n)
	l.record(ctx, "cordon", n, err)
	return err
}

func (l loggedOps) Uncordon(ctx context.Context, n string) error {
	err := l.Ops.Uncordon(ctx, n)
	l.record(ctx, "uncordon", n, err)
	return err
}

func (l loggedOps) LonghornScheduling(ctx context.Context, n string, allow bool) error {
	err := l.Ops.LonghornScheduling(ctx, n, allow)
	action := "longhorn scheduling off"
	if allow {
		action = "longhorn scheduling on"
	}
	l.record(ctx, action, n, err)
	return err
}

func (l loggedOps) ZeroReplicaRisk(ctx context.Context, n string) ([]string, error) {
	v, err := l.Ops.ZeroReplicaRisk(ctx, n)
	l.record(ctx, "zero-replica-risk check", n, err)
	return v, err
}

func (l loggedOps) Drain(ctx context.Context, n string, t time.Duration) error {
	err := l.Ops.Drain(ctx, n, t)
	l.record(ctx, "drain", n, err)
	return err
}

func (l loggedOps) Reboot(ctx context.Context, n string) error {
	err := l.Ops.Reboot(ctx, n)
	l.record(ctx, "reboot", n, err)
	return err
}

func (l loggedOps) StopK3s(ctx context.Context, n string) error {
	err := l.Ops.StopK3s(ctx, n)
	l.record(ctx, "stop k3s", n, err)
	return err
}

func (l loggedOps) KillK3s(ctx context.Context, n string) error {
	err := l.Ops.KillK3s(ctx, n)
	l.record(ctx, "kill k3s", n, err)
	return err
}

func (l loggedOps) StartK3s(ctx context.Context, n string) error {
	err := l.Ops.StartK3s(ctx, n)
	l.record(ctx, "start k3s", n, err)
	return err
}

func (l loggedOps) PowerOff(ctx context.Context, n string, force bool) error {
	err := l.Ops.PowerOff(ctx, n, force)
	action := "poweroff"
	if force {
		action = "poweroff --force"
	}
	l.record(ctx, action, n, err)
	return err
}

func (l loggedOps) EtcdLeader(ctx context.Context) (string, error) {
	v, err := l.Ops.EtcdLeader(ctx)
	l.record(ctx, "etcd leader lookup", "", err)
	return v, err
}

func (l loggedOps) EtcdSnapshot(ctx context.Context) error {
	err := l.Ops.EtcdSnapshot(ctx)
	l.record(ctx, "etcd snapshot", "", err)
	return err
}

func (l loggedOps) VipBootstrap(ctx context.Context, n string, on bool) error {
	err := l.Ops.VipBootstrap(ctx, n, on)
	action := "vip bootstrap address removed"
	if on {
		action = "vip bootstrap address added"
	}
	l.record(ctx, action, n, err)
	return err
}
