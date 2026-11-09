package flows

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/kostiantyn-matsebora/barekube/console/internal/config"
	"github.com/kostiantyn-matsebora/barekube/console/internal/nodeagent"
)

// The platform layer is what runs on the nodes and makes the cluster usable:
// the network (Cilium) and the API virtual IP (kube-vip). It is not a node and
// not the cluster's power state, so it has its own view and its own workflow.
//
// vip-restore repairs the one failure that was found live on 2026-11-08: Cilium
// reaches the API through the VIP, kube-vip needs Cilium to win the lease that
// announces the VIP, so after a full power-off neither can start. It acts only
// when that exact condition holds (see verdictOf), by holding the VIP by hand
// on one master until kube-vip takes over. It runs on its own, started from the
// platform card; the cluster start never runs it.

// Platform states, as PlatformState reports them.
const (
	PlatformAbsent   = "absent"   // Cilium is not installed: nothing to show
	PlatformOK       = "ok"       // Cilium ready everywhere, the VIP answers
	PlatformPartial  = "partial"  // some Cilium agents are not ready yet
	PlatformVipDown  = "vip-down" // Cilium is up, the VIP does not answer
	PlatformDown     = "down"     // no Cilium agent is ready
	PlatformDeadlock = "deadlock" // down, and waiting on the dead VIP: the known deadlock
)

// PlatformState is the one rule for how healthy the platform layer is, shared
// by the card, the cluster start's report and the restore's own verdict.
func PlatformState(vip string, p nodeagent.ClusterProbeResponse) (state, detail string) {
	ready := fmt.Sprintf("%d/%d cilium agents ready", p.CiliumReadyCount, p.CiliumDesired)
	switch {
	case !p.CiliumPresent:
		return PlatformAbsent, "Cilium is not installed"
	case p.CiliumReadyCount == 0 && vip != "" && p.CiliumAPIHost == vip && !p.VIPAnswers:
		return PlatformDeadlock, ready + ", and the API VIP they depend on does not answer"
	case p.CiliumReadyCount == 0:
		return PlatformDown, ready
	case p.CiliumDesired > 0 && p.CiliumReadyCount < p.CiliumDesired:
		return PlatformPartial, ready
	case vip != "" && !p.VIPAnswers:
		return PlatformVipDown, ready + ", the API VIP does not answer"
	}
	// The k3s add-ons are optional: one that is not installed, or scaled to
	// zero on purpose, says nothing. One that is installed and not fully ready
	// makes the layer partial.
	var lagging []string
	for _, c := range p.Packaged {
		if c.Present && c.Desired > 0 && c.Ready < c.Desired {
			lagging = append(lagging, fmt.Sprintf("%s %d/%d", c.Name, c.Ready, c.Desired))
		}
	}
	if len(lagging) > 0 {
		return PlatformPartial, ready + ", not ready: " + strings.Join(lagging, ", ")
	}
	return PlatformOK, ready
}

func (e *Engine) hVipStarted(_ context.Context, r *Run) St {
	j := r.Object
	if e.cfg.Platform.VIP == "" {
		return refuse(j, Refused, "no API VIP is configured (platform.vip)")
	}
	j.Masters = names(e.cfg.Masters())
	return CheckingVip
}

// verdict says what the probe means for the VIP deadlock.
type verdict int

const (
	notNeeded  verdict = iota // no deadlock: nothing to do
	waiting                   // not the known deadlock, and not recovered: look again
	deadlocked                // the deadlock, if it holds past the grace period
)

// verdictOf is the whole rule, in one place. The deadlock is exactly: Cilium
// reaches the API through the VIP, no Cilium agent is ready anywhere, and the
// VIP does not answer. Anything else is either healthy or something this step
// has no business fixing.
func verdictOf(vip string, ciliumPresent bool, apiHost string, readyCount int, vipAnswers bool) (verdict, string) {
	switch {
	case vip == "":
		return notNeeded, "no VIP configured"
	case vipAnswers:
		return notNeeded, "the VIP answers"
	case !ciliumPresent:
		return notNeeded, "Cilium is not installed: no network layer to restore"
	case apiHost != vip:
		return notNeeded, fmt.Sprintf("Cilium reaches the API at %q, not through the VIP", apiHost)
	case readyCount > 0:
		return waiting, "VIP does not answer but Cilium is up: kube-vip may still be starting"
	}
	return deadlocked, "VIP does not answer and no Cilium agent is ready"
}

func (e *Engine) onCheckingVip(ctx context.Context, r *Run, _ payload) St {
	j := r.Object
	vip := e.cfg.Platform.VIP
	p, err := e.ops.ClusterProbe(ctx, vip)
	if err != nil {
		// Not being able to ask is not an answer. Ask again next round.
		return skip
	}
	// What the cluster looks like, as found: this is the evidence behind
	// every decision below, so it is shown even when nothing needs doing.
	e.noteChanged(ctx, r.RunID+"|probe", fmt.Sprintf(
		"probe: VIP %s answers=%v; cilium present=%v ready=%d/%d api-host=%q; kube-vip present=%v lease-holder=%q",
		vip, p.VIPAnswers, p.CiliumPresent, p.CiliumReadyCount, p.CiliumDesired, p.CiliumAPIHost, p.KubeVipPresent, p.KubeVipLeader))
	v, why := verdictOf(vip, p.CiliumPresent, p.CiliumAPIHost, p.CiliumReadyCount, p.VIPAnswers)
	key := r.RunID + "|deadlock"
	switch v {
	case notNeeded:
		e.heldFor(key, false, 0)
		j.Reason = "nothing to restore: " + why
		e.note(ctx, "", "vip restore: not needed, "+why)
		return Completed
	case waiting:
		e.heldFor(key, false, 0)
		e.noteChanged(ctx, r.RunID+"|verdict", "vip deadlock: waiting, "+why)
		return skip
	}
	if !e.heldFor(key, true, e.cfg.Platform.DeadlockGrace) {
		return skip
	}
	j.LeaseSeen = p.KubeVipRenewed
	e.note(ctx, "", "vip deadlock detected: "+why)
	return BreakingDeadlock
}

// hBreakingDeadlock adds the VIP on exactly one master. A master whose call
// fails has not started the unit, so the next one is tried: never two at once.
func (e *Engine) hBreakingDeadlock(ctx context.Context, r *Run) St {
	j := r.Object
	for _, m := range e.reachableMasters(j) {
		if err := e.ops.VipBootstrap(ctx, m, true); err == nil {
			j.Bootstrap = m
			return AwaitingVip
		}
	}
	return refuse(j, Failed, "no master accepted the VIP bootstrap address")
}

func (e *Engine) onAwaitingVip(ctx context.Context, r *Run, _ payload) St {
	j := r.Object
	p, err := e.ops.ClusterProbe(ctx, e.cfg.Platform.VIP)
	if err != nil {
		return skip
	}
	// A lease left from before the shutdown still names a holder. Only a
	// renewal that differs from the one seen before proves kube-vip ran.
	fresh := p.KubeVipRenewed != "" && p.KubeVipRenewed != j.LeaseSeen
	if !(p.VIPAnswers && p.CiliumReadyCount > 0 && fresh) {
		return skip
	}
	j.VipLeader = p.KubeVipLeader
	e.note(ctx, j.Bootstrap, fmt.Sprintf("cilium up, kube-vip lease held by %q", p.KubeVipLeader))
	return ReleasingVip
}

func sameNode(a, b string) bool {
	short := func(s string) string { return strings.ToLower(strings.SplitN(s, ".", 2)[0]) }
	return a != "" && b != "" && short(a) == short(b)
}

// hReleasingVip removes the bootstrap address, unless kube-vip's leader is the
// very master holding it: both own the same address there, and removing it
// would take it from kube-vip.
func (e *Engine) hReleasingVip(ctx context.Context, r *Run) St {
	j := r.Object
	if sameNode(j.VipLeader, j.Bootstrap) {
		j.Reason = "kube-vip leads from " + j.Bootstrap + ", the bootstrap address stays as kube-vip's own"
		e.note(ctx, j.Bootstrap, "bootstrap address kept: kube-vip leads from the same master")
		return Completed
	}
	if err := e.ops.VipBootstrap(ctx, j.Bootstrap, false); err != nil {
		return refuse(j, Failed, "removing the VIP bootstrap address on %s failed: %v", j.Bootstrap, err)
	}
	return AwaitingVipStable
}

func (e *Engine) onVipStable(ctx context.Context, r *Run, _ payload) St {
	p, err := e.ops.ClusterProbe(ctx, e.cfg.Platform.VIP)
	answers := err == nil && p.VIPAnswers
	if !e.heldFor(r.RunID+"|stable", answers, e.cfg.Timing.Settle) {
		return skip
	}
	e.note(ctx, "", "the VIP answers without the bootstrap address")
	return Completed
}

// onVipStableTimeout puts the address back: a VIP that dies when the manual
// address goes is worse than a manual address left on.
func (e *Engine) onVipStableTimeout(ctx context.Context, r *Run) St {
	j := r.Object
	_ = e.ops.VipBootstrap(ctx, j.Bootstrap, true)
	j.Reason = fmt.Sprintf("the VIP stopped answering without the bootstrap address; it is back on %s, kube-vip is not holding it", j.Bootstrap)
	return Failed
}

func (e *Engine) onVipTimeout(_ context.Context, r *Run) St {
	j := r.Object
	j.Reason = fmt.Sprintf("Cilium and kube-vip did not recover in time; the bootstrap address stays on %s (remove with: systemctl stop barekube-vip-bootstrap)", j.Bootstrap)
	return Failed
}

func (e *Engine) vipRestoreSpec() Spec {
	w := e.cfg.Platform.Waits
	wait := func(d time.Duration) func(config.Timing, *Job) time.Duration {
		return func(config.Timing, *Job) time.Duration { return d }
	}
	return Spec{
		Name: WfVipRestore,
		Edges: []Edge{
			{Started, CheckingVip, "a VIP is configured", Event},
			{Started, Refused, "no VIP configured", Fail},
			{CheckingVip, Completed, "nothing to restore", Event},
			{CheckingVip, BreakingDeadlock, "deadlock held past the grace period", Event},
			{CheckingVip, Failed, "timeout, no verdict", Timeout},
			{BreakingDeadlock, AwaitingVip, "bootstrap address added on one master", Event},
			{BreakingDeadlock, Failed, "no master accepted it", Fail},
			{AwaitingVip, ReleasingVip, "cilium up, kube-vip lease renewed", Event},
			{AwaitingVip, Failed, "timeout", Timeout},
			{ReleasingVip, AwaitingVipStable, "bootstrap address removed", Event},
			{ReleasingVip, Completed, "kube-vip leads from the same master, kept", Event},
			{ReleasingVip, Failed, "removal failed", Fail},
			{AwaitingVipStable, Completed, "VIP answers without the address", Event},
			{AwaitingVipStable, Failed, "timeout, address put back", Timeout},
		},
		Steps: map[St]stepFn{Started: e.hVipStarted, BreakingDeadlock: e.hBreakingDeadlock, ReleasingVip: e.hReleasingVip},
		Waits: map[St]Wait{
			CheckingVip: {Timer: wait(w.Probe), OnEvent: e.onCheckingVip,
				OnTimeout: timeoutTo(Failed, "the API VIP check reached no verdict in time")},
			AwaitingVip:       {Timer: wait(w.Vip), OnEvent: e.onAwaitingVip, OnTimeout: e.onVipTimeout},
			AwaitingVipStable: {Timer: wait(w.Stable), OnEvent: e.onVipStable, OnTimeout: e.onVipStableTimeout},
		},
	}
}
