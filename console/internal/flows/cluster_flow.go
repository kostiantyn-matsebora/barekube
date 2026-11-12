package flows

import (
	"context"
	"slices"
	"time"

	"github.com/kostiantyn-matsebora/barekube/console/internal/config"
)

// orderMasters puts the etcd leader last, so quorum lasts as long as it can.
func orderMasters(masters []string, leader string, leaderLast bool) []string {
	out := slices.Clone(masters)
	if !leaderLast || !slices.Contains(out, leader) {
		return out
	}
	out = slices.DeleteFunc(out, func(m string) bool { return m == leader })
	return append(out, leader)
}

func names(ns []config.Node) []string {
	out := make([]string, 0, len(ns))
	for _, n := range ns {
		out = append(out, n.Name)
	}
	return out
}

// child is the latest run of a node, if this cluster run started it.
func (e *Engine) child(parent *Run, node string) (RunInfo, bool) {
	info, ok := e.reg.latestRun(node)
	if !ok || info.Parent != parent.RunID {
		return RunInfo{}, false
	}
	return info, true
}

// childSettled is true for a child that ended, or that never started.
func (e *Engine) childSettled(parent *Run, node string) bool {
	info, ok := e.child(parent, node)
	return !ok || info.Terminal
}

func (e *Engine) childOK(parent *Run, node string) bool {
	info, ok := e.child(parent, node)
	return ok && info.Terminal && (info.st == PoweredOff || info.st == StoodBy)
}

func (e *Engine) startChildren(ctx context.Context, r *Run, nodes []string, pol config.Policy) {
	wf := WfStandBy
	if pol.Action == "poweroff" {
		wf = WfPowerOff
	}
	for _, name := range nodes {
		n, ok := e.cfg.Node(name)
		if !ok {
			continue
		}
		child := wf
		if wf == WfPowerOff && !n.Supports(config.OpPowerOff) {
			child = WfStandBy // this node cannot be powered back on remotely
		}
		job := &Job{Node: name, Role: string(n.Role), Mode: Mode(pol.Mode), Cluster: true, Parent: r.RunID, ForceAfter: pol.ForceAfter, By: "cluster shutdown"}
		// A node that cannot start its child (busy, unsupported) is recorded
		// by the action log and treated as settled: the shutdown goes on.
		if _, err := e.start(ctx, e.wfs[child], name, job); err != nil {
			e.ops.(loggedOps).record(ctx, "start "+child+" child", name, err)
		}
	}
}

func (e *Engine) hClusterStarted(ctx context.Context, r *Run) St {
	j := r.Object
	j.Agents = names(e.cfg.Agents())
	j.Masters = names(e.cfg.Masters())
	if leader, err := e.ops.EtcdLeader(ctx); err == nil {
		j.Leader = leader
	}
	return SnapshottingEtcd
}

// hSnapshotting saves an etcd snapshot before anything is taken down. It is
// best effort: a failed snapshot is logged and the shutdown goes on, because a
// dying battery must not wait on a backup.
func (e *Engine) hSnapshotting(ctx context.Context, r *Run) St {
	if e.cfg.ClusterShutdown.SnapshotEnabled() {
		_ = e.ops.EtcdSnapshot(ctx)
	}
	return CordoningAll
}

func (e *Engine) hCordoningAll(ctx context.Context, r *Run) St {
	if e.cfg.ClusterShutdown.CordonAllEnabled() {
		for _, n := range e.cfg.Nodes {
			_ = e.ops.Cordon(ctx, n.Name)
		}
	}
	return StandingByAgents
}

func (e *Engine) hStandingByAgents(ctx context.Context, r *Run) St {
	e.startChildren(ctx, r, r.Object.Agents, e.cfg.ClusterShutdown.Agents)
	return AwaitingAgentsStoodBy
}

func (e *Engine) hPoweringOffMasters(ctx context.Context, r *Run) St {
	e.startChildren(ctx, r, r.Object.Masters, e.cfg.ClusterShutdown.Masters)
	return AwaitingMastersDrained
}

// nextMaster is the first master not yet released, leader last when asked.
func (e *Engine) nextMaster(j *Job) (string, bool) {
	order := orderMasters(j.Masters, j.Leader, e.cfg.ClusterShutdown.Masters.LeaderLast)
	for _, m := range order {
		if !slices.Contains(j.Released, m) {
			return m, true
		}
	}
	return "", false
}

func (e *Engine) finish(r *Run) St {
	for _, m := range r.Object.Masters {
		if !e.childOK(r, m) {
			r.Object.Reason = "master " + m + " did not shut down cleanly"
			return Failed
		}
	}
	return Completed
}

func (e *Engine) hReleasing(ctx context.Context, r *Run) St {
	j := r.Object
	next, ok := e.nextMaster(j)
	if !ok {
		return e.finish(r)
	}
	j.Released = append(j.Released, next)
	j.Current = next
	e.reg.clear(next)
	e.log.Add(clusterEvent(ctx, r, next))
	return AwaitingMasterDown
}

func (e *Engine) clusterSpec() Spec {
	waits := e.cfg.ClusterShutdown.Waits
	return Spec{
		Name: WfCluster,
		Edges: []Edge{
			{Started, SnapshottingEtcd, "etcd leader read", Event},
			{SnapshottingEtcd, CordoningAll, "snapshot saved, or skipped, best effort", Event},
			{CordoningAll, StandingByAgents, "every node cordoned, best effort", Event},
			{StandingByAgents, AwaitingAgentsStoodBy, "agent runs started", Event},
			{AwaitingAgentsStoodBy, PoweringOffMasters, "every agent child settled", Event},
			{AwaitingAgentsStoodBy, PoweringOffMasters, "timeout, continue", Timeout},
			{PoweringOffMasters, AwaitingMastersDrained, "master runs started", Event},
			{AwaitingMastersDrained, ReleasingNextMaster, "every master waiting or settled", Event},
			{AwaitingMastersDrained, ReleasingNextMaster, "timeout, continue", Timeout},
			{ReleasingNextMaster, AwaitingMasterDown, "clearance sent, leader last", Event},
			{ReleasingNextMaster, Completed, "no masters left, all down", Event},
			{ReleasingNextMaster, Failed, "no masters left, one failed", Fail},
			{AwaitingMasterDown, ReleasingNextMaster, "child settled, masters remain", Event},
			{AwaitingMasterDown, Completed, "child settled, all down", Event},
			{AwaitingMasterDown, Failed, "child settled, one failed", Event},
			{AwaitingMasterDown, ReleasingNextMaster, "timeout, masters remain", Timeout},
			{AwaitingMasterDown, Failed, "timeout, none remain", Timeout},
		},
		Steps: map[St]stepFn{
			Started: e.hClusterStarted, SnapshottingEtcd: e.hSnapshotting, CordoningAll: e.hCordoningAll, StandingByAgents: e.hStandingByAgents,
			PoweringOffMasters: e.hPoweringOffMasters, ReleasingNextMaster: e.hReleasing,
		},
		Waits: map[St]Wait{
			AwaitingAgentsStoodBy: {Timer: func(config.Timing, *Job) time.Duration { return waits.Agents },
				OnEvent: func(_ context.Context, r *Run, _ payload) St {
					for _, a := range r.Object.Agents {
						if !e.childSettled(r, a) {
							return skip
						}
					}
					return PoweringOffMasters
				},
				OnTimeout: timeoutTo(PoweringOffMasters, "agents were not all settled in time, continuing")},
			AwaitingMastersDrained: {Timer: func(config.Timing, *Job) time.Duration { return waits.MastersDrained },
				OnEvent: func(_ context.Context, r *Run, _ payload) St {
					for _, m := range r.Object.Masters {
						info, ok := e.child(r, m)
						if ok && !info.Terminal && info.st != AwaitingClearance {
							return skip
						}
					}
					return ReleasingNextMaster
				},
				OnTimeout: timeoutTo(ReleasingNextMaster, "masters were not all drained in time, continuing")},
			AwaitingMasterDown: {Timer: func(config.Timing, *Job) time.Duration { return waits.MasterDown },
				OnEvent: func(_ context.Context, r *Run, _ payload) St {
					if !e.childSettled(r, r.Object.Current) {
						return skip
					}
					if _, more := e.nextMaster(r.Object); more {
						return ReleasingNextMaster
					}
					return e.finish(r)
				},
				OnTimeout: func(_ context.Context, r *Run) St {
					if _, more := e.nextMaster(r.Object); more {
						r.Object.Reason = "master " + r.Object.Current + " was too slow, continuing"
						return ReleasingNextMaster
					}
					return refuse(r.Object, Failed, "master %s did not go down in time", r.Object.Current)
				}},
		},
	}
}
