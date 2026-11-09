package flows

import (
	"context"
	"fmt"
	"time"

	"github.com/kostiantyn-matsebora/barekube/console/internal/config"
	"github.com/kostiantyn-matsebora/barekube/console/internal/observe"
)

// bestEffort is true when a failed preparation must not stop the run: force
// mode, and every child of a cluster shutdown.
func bestEffort(j *Job) bool { return j.Mode == ModeForce || j.Cluster }

func fixed(f func(config.Timing) time.Duration) func(config.Timing, *Job) time.Duration {
	return func(t config.Timing, _ *Job) time.Duration { return f(t) }
}

func when(cond func(observe.Observation) bool, next St) eventFn {
	return func(_ context.Context, _ *Run, p payload) St {
		if cond(p.Observation) {
			return next
		}
		return skip
	}
}

func unreachable(o observe.Observation) bool { return o.Reachable == observe.No }
func k3sDown(o observe.Observation) bool     { return o.K3sActive == observe.No }
func k3sUp(o observe.Observation) bool       { return o.K3sActive == observe.Yes }
func uncordoned(o observe.Observation) bool  { return o.Cordoned == observe.No }
func backAndReady(o observe.Observation) bool {
	return o.Reachable == observe.Yes && o.Ready == observe.Yes
}

func timeoutTo(next St, format string, a ...any) timeoutFn {
	return func(_ context.Context, r *Run) St {
		r.Object.Reason = fmt.Sprintf(format, a...)
		return next
	}
}

func refuse(j *Job, st St, format string, a ...any) St {
	j.Reason = fmt.Sprintf(format, a...)
	return st
}

// hStarted gates a master behind etcd quorum, in every mode and for every
// operation that takes a master down. A cluster shutdown bypasses it: losing
// quorum is the point.
func (e *Engine) hStarted(ctx context.Context, r *Run) St {
	j := r.Object
	if j.Role == string(config.RoleMaster) && !j.Cluster && !e.ops.QuorumOK(ctx) {
		return refuse(j, Refused, "etcd quorum not healthy across all masters, refused in every mode")
	}
	return Cordoning
}

// hCordoning cordons and stops Longhorn scheduling. Graceful also refuses a
// node whose volumes would lose their last healthy replica. Force and cluster
// children carry on whatever fails.
func (e *Engine) hCordoning(ctx context.Context, r *Run) St {
	j := r.Object
	best := bestEffort(j)
	if err := e.ops.Cordon(ctx, j.Node); err != nil && !best {
		return refuse(j, RollingBack, "cordon failed: %v", err)
	}
	if err := e.ops.LonghornScheduling(ctx, j.Node, false); err != nil && !best {
		return refuse(j, RollingBack, "disabling longhorn scheduling failed: %v", err)
	}
	if j.Mode == ModeGraceful && !j.Cluster {
		risk, err := e.ops.ZeroReplicaRisk(ctx, j.Node)
		if err != nil {
			return refuse(j, RollingBack, "replica risk check failed: %v", err)
		}
		if len(risk) > 0 {
			return refuse(j, RollingBack, "volumes %v would lose their last healthy replica", risk)
		}
	}
	return Draining
}

func (e *Engine) hDraining(next St) stepFn {
	return func(ctx context.Context, r *Run) St {
		j := r.Object
		limit := e.cfg.Timing.Drain
		if j.Mode == ModeForce {
			limit = e.cfg.Timing.ForceDrain
		}
		if err := e.ops.Drain(ctx, j.Node, limit); err != nil && !bestEffort(j) {
			return refuse(j, RollingBack, "drain failed: %v", err)
		}
		return next
	}
}

func (e *Engine) hRebootRequested(ctx context.Context, r *Run) St {
	if err := e.ops.Reboot(ctx, r.Object.Node); err != nil {
		return refuse(r.Object, RollingBack, "reboot call failed: %v", err)
	}
	return AwaitingNodeDown
}

func (e *Engine) hStoppingK3s(ctx context.Context, r *Run) St {
	j := r.Object
	stop := e.ops.StopK3s
	if j.Mode == ModeForce {
		stop = e.ops.KillK3s
	}
	if err := stop(ctx, j.Node); err != nil {
		return refuse(j, RollingBack, "stopping k3s failed: %v", err)
	}
	return AwaitingK3sInactive
}

// hRollingBack puts the node back as it was. The run still ends Failed.
func (e *Engine) hRollingBack(ctx context.Context, r *Run) St {
	_ = e.ops.LonghornScheduling(ctx, r.Object.Node, true)
	_ = e.ops.Uncordon(ctx, r.Object.Node)
	return Failed
}

func (e *Engine) hUncordoning(ctx context.Context, r *Run) St {
	j := r.Object
	_ = e.ops.LonghornScheduling(ctx, j.Node, true)
	if err := e.ops.Uncordon(ctx, j.Node); err != nil {
		return refuse(j, Failed, "node is back but uncordon failed: %v", err)
	}
	return AwaitingUncordoned
}

func (e *Engine) awaitUncordoned() Wait {
	return Wait{Fresh: true, Timer: fixed(func(t config.Timing) time.Duration { return t.Uncordoned }),
		OnEvent:   when(uncordoned, Completed),
		OnTimeout: timeoutTo(Failed, "node did not report schedulable in time")}
}

func (e *Engine) rebootSpec() Spec {
	return Spec{
		Name: WfReboot,
		Edges: []Edge{
			{Started, Cordoning, "quorum ok, or agent", Event},
			{Started, Refused, "master, quorum unhealthy", Fail},
			{Cordoning, Draining, "cordoned, scheduling off", Event},
			{Cordoning, RollingBack, "failed, graceful", Fail},
			{Draining, RebootRequested, "drained, force: attempted", Event},
			{Draining, RollingBack, "failed, graceful", Fail},
			{RebootRequested, AwaitingNodeDown, "reboot accepted", Event},
			{RebootRequested, RollingBack, "call failed", Fail},
			{AwaitingNodeDown, AwaitingNodeUp, "node.unreachable", Event},
			{AwaitingNodeDown, RollingBack, "timeout", Timeout},
			{AwaitingNodeUp, Uncordoning, "node.reachable and node.ready", Event},
			{AwaitingNodeUp, Failed, "timeout", Timeout},
			{Uncordoning, AwaitingUncordoned, "uncordon accepted", Event},
			{Uncordoning, Failed, "call failed", Fail},
			{AwaitingUncordoned, Completed, "node.uncordoned", Event},
			{AwaitingUncordoned, Failed, "timeout", Timeout},
			{RollingBack, Failed, "scheduling on, uncordoned", Event},
		},
		Steps: map[St]stepFn{
			Started: e.hStarted, Cordoning: e.hCordoning, Draining: e.hDraining(RebootRequested),
			RebootRequested: e.hRebootRequested, Uncordoning: e.hUncordoning, RollingBack: e.hRollingBack,
		},
		Waits: map[St]Wait{
			AwaitingNodeDown: {Fresh: true, Timer: fixed(func(t config.Timing) time.Duration { return t.NodeDown }),
				OnEvent:   when(unreachable, AwaitingNodeUp),
				OnTimeout: timeoutTo(RollingBack, "node never went unreachable after the reboot call")},
			AwaitingNodeUp: {Fresh: true, Timer: fixed(func(t config.Timing) time.Duration { return t.NodeUp }),
				OnEvent:   when(backAndReady, Uncordoning),
				OnTimeout: timeoutTo(Failed, "node did not come back Ready in time")},
			AwaitingUncordoned: e.awaitUncordoned(),
		},
	}
}

func (e *Engine) standBySpec() Spec {
	return Spec{
		Name: WfStandBy,
		Edges: []Edge{
			{Started, Cordoning, "quorum ok, or agent", Event},
			{Started, Refused, "master, quorum unhealthy", Fail},
			{Cordoning, Draining, "cordoned, scheduling off", Event},
			{Cordoning, RollingBack, "failed, graceful", Fail},
			{Draining, StoppingK3s, "drained, force: attempted", Event},
			{Draining, RollingBack, "failed, graceful", Fail},
			{StoppingK3s, AwaitingK3sInactive, "stop, force: kill accepted", Event},
			{StoppingK3s, RollingBack, "call failed", Fail},
			{AwaitingK3sInactive, StoodBy, "k3s.inactive, held", Event},
			{AwaitingK3sInactive, Failed, "timeout", Timeout},
			{RollingBack, Failed, "scheduling on, uncordoned", Event},
		},
		Steps: map[St]stepFn{
			Started: e.hStarted, Cordoning: e.hCordoning, Draining: e.hDraining(StoppingK3s),
			StoppingK3s: e.hStoppingK3s, RollingBack: e.hRollingBack,
		},
		Waits: map[St]Wait{
			AwaitingK3sInactive: {Fresh: true, Timer: fixed(func(t config.Timing) time.Duration { return t.K3sInactive }),
				OnEvent:   e.settled(k3sDown, StoodBy),
				OnTimeout: timeoutTo(Failed, "k3s did not stop in time")},
		},
	}
}

func (e *Engine) powerOffSpec() Spec {
	return Spec{
		Name: WfPowerOff,
		Edges: []Edge{
			{Started, Cordoning, "quorum ok, or cluster", Event},
			{Started, Refused, "master, quorum unhealthy", Fail},
			{Cordoning, Draining, "cordoned, scheduling off", Event},
			{Cordoning, RollingBack, "failed, graceful per-node", Fail},
			{Draining, StoppingK3s, "drained, or attempted", Event},
			{Draining, RollingBack, "failed, graceful per-node", Fail},
			{StoppingK3s, AwaitingK3sInactive, "stop or kill accepted", Event},
			{StoppingK3s, RollingBack, "call failed", Fail},
			{AwaitingK3sInactive, PowerOffRequested, "k3s.inactive held, per-node", Event},
			{AwaitingK3sInactive, AwaitingClearance, "k3s.inactive held, cluster", Event},
			{AwaitingK3sInactive, Failed, "timeout", Timeout},
			{AwaitingClearance, PowerOffRequested, "clearance from parent", Event},
			{AwaitingClearance, Failed, "timeout", Timeout},
			{PowerOffRequested, AwaitingNodeDown, "poweroff accepted", Event},
			{PowerOffRequested, Failed, "call failed", Fail},
			{AwaitingNodeDown, PoweredOff, "node.unreachable", Event},
			{AwaitingNodeDown, ForcingOff, "timeout, graceful", Timeout},
			{AwaitingNodeDown, Failed, "timeout, force", Timeout},
			{ForcingOff, AwaitingForcedDown, "poweroff --force accepted", Event},
			{ForcingOff, Failed, "call failed", Fail},
			{AwaitingForcedDown, PoweredOff, "node.unreachable", Event},
			{AwaitingForcedDown, Failed, "timeout", Timeout},
			{RollingBack, Failed, "scheduling on, uncordoned", Event},
		},
		Steps: map[St]stepFn{
			Started: e.hStarted, Cordoning: e.hCordoning, Draining: e.hDraining(StoppingK3s),
			StoppingK3s: e.hStoppingK3s, RollingBack: e.hRollingBack,
			PowerOffRequested: e.hPowerOffRequested, ForcingOff: e.hForcingOff,
		},
		Waits: map[St]Wait{
			AwaitingK3sInactive: {Fresh: true, Timer: fixed(func(t config.Timing) time.Duration { return t.K3sInactive }),
				OnEvent: e.settledTo(k3sDown, func(r *Run) St {
					if r.Object.Cluster {
						return AwaitingClearance
					}
					return PowerOffRequested
				}),
				OnTimeout: timeoutTo(Failed, "k3s did not stop in time")},
			AwaitingClearance: {Timer: fixed(func(t config.Timing) time.Duration { return t.Clearance }),
				OnEvent: func(_ context.Context, r *Run, _ payload) St {
					if e.reg.isCleared(r.Object.Node) {
						return PowerOffRequested
					}
					return skip
				},
				OnTimeout: timeoutTo(Failed, "the cluster shutdown never released this node")},
			AwaitingNodeDown: {Fresh: true,
				Timer: func(t config.Timing, j *Job) time.Duration {
					switch {
					case j.Mode == ModeForce:
						return t.ForcedOff
					case j.ForceAfter > 0:
						return j.ForceAfter
					}
					return t.GracefulOff
				},
				OnEvent: when(unreachable, PoweredOff),
				OnTimeout: func(_ context.Context, r *Run) St {
					if r.Object.Mode == ModeForce {
						return refuse(r.Object, Failed, "node still reachable after the forced poweroff")
					}
					r.Object.Reason = "graceful poweroff too slow, forcing it"
					return ForcingOff
				}},
			AwaitingForcedDown: {Fresh: true, Timer: fixed(func(t config.Timing) time.Duration { return t.ForcedOff }),
				OnEvent:   when(unreachable, PoweredOff),
				OnTimeout: timeoutTo(Failed, "node still reachable after poweroff --force")},
		},
	}
}

func (e *Engine) hPowerOffRequested(ctx context.Context, r *Run) St {
	if err := e.ops.PowerOff(ctx, r.Object.Node, r.Object.Mode == ModeForce); err != nil {
		return refuse(r.Object, Failed, "poweroff call failed: %v", err)
	}
	return AwaitingNodeDown
}

func (e *Engine) hForcingOff(ctx context.Context, r *Run) St {
	if err := e.ops.PowerOff(ctx, r.Object.Node, true); err != nil {
		return refuse(r.Object, Failed, "forced poweroff call failed: %v", err)
	}
	return AwaitingForcedDown
}

// hRejoinStarted reads the observed state. It never pings on its own.
func (e *Engine) hRejoinStarted(_ context.Context, r *Run) St {
	obs, ok := e.obs.Latest(r.Object.Node)
	switch {
	case !ok || obs.Reachable == observe.Unknown:
		return refuse(r.Object, Refused, "node state not observed yet")
	case obs.Reachable == observe.No:
		return refuse(r.Object, Refused, "node-agent unreachable, has the node been powered on?")
	}
	return StartingK3s
}

func (e *Engine) hStartingK3s(ctx context.Context, r *Run) St {
	if err := e.ops.StartK3s(ctx, r.Object.Node); err != nil {
		return refuse(r.Object, Failed, "starting k3s failed: %v", err)
	}
	return AwaitingK3sActive
}

func (e *Engine) rejoinSpec() Spec {
	return Spec{
		Name: WfRejoin,
		Edges: []Edge{
			{Started, StartingK3s, "node.reachable observed", Event},
			{Started, Refused, "node.unreachable observed", Fail},
			{StartingK3s, AwaitingK3sActive, "start accepted", Event},
			{StartingK3s, Failed, "call failed", Fail},
			{AwaitingK3sActive, AwaitingNodeReady, "k3s.active", Event},
			{AwaitingK3sActive, Failed, "timeout", Timeout},
			{AwaitingNodeReady, Uncordoning, "node.ready", Event},
			{AwaitingNodeReady, Failed, "timeout", Timeout},
			{Uncordoning, AwaitingUncordoned, "uncordon accepted", Event},
			{Uncordoning, Failed, "call failed", Fail},
			{AwaitingUncordoned, Completed, "node.uncordoned", Event},
			{AwaitingUncordoned, Failed, "timeout", Timeout},
		},
		Steps: map[St]stepFn{Started: e.hRejoinStarted, StartingK3s: e.hStartingK3s, Uncordoning: e.hUncordoning},
		Waits: map[St]Wait{
			AwaitingK3sActive: {Fresh: true, Timer: fixed(func(t config.Timing) time.Duration { return t.K3sActive }),
				OnEvent:   when(k3sUp, AwaitingNodeReady),
				OnTimeout: timeoutTo(Failed, "k3s did not start in time")},
			AwaitingNodeReady: {Fresh: true, Timer: fixed(func(t config.Timing) time.Duration { return t.NodeUp }),
				OnEvent:   when(backAndReady, Uncordoning),
				OnTimeout: timeoutTo(Failed, "node did not report Ready in time")},
			AwaitingUncordoned: e.awaitUncordoned(),
		},
	}
}
