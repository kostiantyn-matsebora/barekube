package flows

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/kostiantyn-matsebora/barekube/console/internal/config"
	"github.com/kostiantyn-matsebora/barekube/console/internal/observe"
)

// The cluster start brings the nodes of a cluster back from fully off. The
// owner powers them on; this takes over from "the nodes are booting": it waits
// for the masters and an etcd majority, then rejoins every node. It is about
// the nodes only. The network layer (Cilium, kube-vip, the API VIP) is the
// platform's and has its own workflow (platform_flow.go); the final report here
// only says when that layer looks unhealthy. Every step reads the cluster and
// acts on what it finds, so a run is safe to start again.

// needsRejoin is true for a node that is not yet back as a working member.
// Anything not observed as healthy counts: a rejoin of a healthy node is
// harmless, a skipped broken one is not.
func needsRejoin(o observe.Observation) bool {
	return !(o.Reachable == observe.Yes && o.K3sActive == observe.Yes && o.Ready == observe.Yes && o.Cordoned == observe.No)
}

// note writes a decision to the event log under the current run.
func (e *Engine) note(ctx context.Context, node, detail string) {
	e.ops.(loggedOps).record(ctx, detail, node, nil)
}

// noteChanged logs a finding the first time, and again only when it changes.
// The checks run every observation round, and a log line per round would bury
// the one that matters.
func (e *Engine) noteChanged(ctx context.Context, key, detail string) {
	e.heldMu.Lock()
	same := e.seen[key] == detail
	e.seen[key] = detail
	e.heldMu.Unlock()
	if !same {
		e.note(ctx, "", detail)
	}
}

// heldFor is true once cond has held on every call for at least d. One
// reading proves nothing: the first true reading starts the clock and a false
// one forgets it. The clock is kept in memory, not in the run, because a wait
// that returns skip saves nothing.
func (e *Engine) heldFor(key string, cond bool, d time.Duration) bool {
	e.heldMu.Lock()
	defer e.heldMu.Unlock()
	if !cond {
		delete(e.held, key)
		return false
	}
	since, ok := e.held[key]
	if !ok {
		e.held[key] = e.clk.Now()
		since = e.held[key]
	}
	if e.clk.Since(since) < d {
		return false
	}
	delete(e.held, key)
	return true
}

func (e *Engine) reachableMasters(j *Job) []string {
	var out []string
	for _, m := range j.Masters {
		if o, ok := e.obs.Latest(m); ok && o.Reachable == observe.Yes {
			out = append(out, m)
		}
	}
	return out
}

func (e *Engine) hStartStarted(_ context.Context, r *Run) St {
	j := r.Object
	j.Masters = names(e.cfg.Masters())
	j.Agents = names(e.cfg.Agents())
	return AwaitingNodes
}

func (e *Engine) onNodes(ctx context.Context, r *Run, _ payload) St {
	if len(e.reachableMasters(r.Object)) < len(r.Object.Masters) {
		return skip
	}
	e.note(ctx, "", "every master answers")
	return AwaitingQuorum
}

// onNodesTimeout goes on with a majority: one master that never boots must not
// keep the other two from bringing the cluster back.
func (e *Engine) onNodesTimeout(ctx context.Context, r *Run) St {
	j := r.Object
	up := e.reachableMasters(j)
	if len(up) >= len(j.Masters)/2+1 {
		var missing []string
		for _, m := range j.Masters {
			if !slices.Contains(up, m) {
				missing = append(missing, m)
			}
		}
		j.Reason = "continuing without " + strings.Join(missing, ", ") + ": not reachable in time"
		e.note(ctx, "", j.Reason)
		return AwaitingQuorum
	}
	j.Reason = fmt.Sprintf("only %d of %d masters reachable, a majority is needed", len(up), len(j.Masters))
	return Failed
}

func (e *Engine) onQuorum(ctx context.Context, r *Run, _ payload) St {
	if !e.ops.QuorumMajority(ctx) {
		return skip
	}
	e.note(ctx, "", "etcd majority answering")
	return RejoiningMasters
}

// rejoinChild starts a rejoin run of one node under this run. A node that
// cannot start one is recorded and counts as settled.
func (e *Engine) rejoinChild(ctx context.Context, r *Run, name string) {
	n, ok := e.cfg.Node(name)
	if !ok || !n.Supports(config.OpRejoin) {
		return
	}
	job := &Job{Node: name, Role: string(n.Role), Mode: ModeGraceful, Parent: r.RunID, By: "cluster start"}
	if _, err := e.start(ctx, e.wfs[WfRejoin], name, job); err != nil {
		e.ops.(loggedOps).record(ctx, "start rejoin child", name, err)
	}
}

func (e *Engine) rejoinNeeded(name string) bool {
	n, ok := e.cfg.Node(name)
	if !ok || !n.Supports(config.OpRejoin) {
		return false
	}
	o, seen := e.obs.Latest(name)
	return !seen || needsRejoin(o)
}

// hRejoiningMasters starts the next master that needs it. Masters rejoin one
// at a time, so quorum is never put at risk by two at once.
func (e *Engine) hRejoiningMasters(ctx context.Context, r *Run) St {
	j := r.Object
	for _, m := range j.Masters {
		if slices.Contains(j.Released, m) {
			continue
		}
		j.Released = append(j.Released, m)
		if !e.rejoinNeeded(m) {
			continue
		}
		j.Current = m
		e.rejoinChild(ctx, r, m)
		return AwaitingMasterRejoined
	}
	return RejoiningAgents
}

// recordOutcome notes a child that did not end Completed.
func (e *Engine) recordOutcome(r *Run, node string) {
	if info, ok := e.child(r, node); ok && info.Terminal && info.st != Completed {
		msg := node + ": " + info.Status
		if info.Reason != "" {
			msg += " (" + info.Reason + ")"
		}
		r.Object.Failures = append(r.Object.Failures, msg)
	}
}

func (e *Engine) onMasterRejoined(_ context.Context, r *Run, _ payload) St {
	if !e.childSettled(r, r.Object.Current) {
		return skip
	}
	e.recordOutcome(r, r.Object.Current)
	return RejoiningMasters
}

func (e *Engine) hRejoiningAgents(ctx context.Context, r *Run) St {
	started := 0
	for _, a := range r.Object.Agents {
		if e.rejoinNeeded(a) {
			e.rejoinChild(ctx, r, a)
			started++
		}
	}
	if started == 0 {
		return CheckingHealth
	}
	return AwaitingAgentsRejoined
}

func (e *Engine) onAgentsRejoined(_ context.Context, r *Run, _ payload) St {
	for _, a := range r.Object.Agents {
		if !e.childSettled(r, a) {
			return skip
		}
	}
	for _, a := range r.Object.Agents {
		e.recordOutcome(r, a)
	}
	return CheckingHealth
}

// hCheckingHealth reports, and fixes nothing. A faulty Longhorn volume or a
// node that did not rejoin fails the run. Degraded volumes (rebuilding) and a
// sealed Vault are notes on a completed run: neither is the console's to fix,
// and it never holds the unseal keys.
func (e *Engine) hCheckingHealth(ctx context.Context, r *Run) St {
	j := r.Object
	fatal := len(j.Failures) > 0
	notes := slices.Clone(j.Failures)

	if lh, err := e.ops.LonghornHealth(ctx); err != nil {
		notes = append(notes, "longhorn health could not be read: "+err.Error())
	} else if lh.Available {
		if lh.Faulty > 0 {
			fatal = true
			notes = append(notes, fmt.Sprintf("%d of %d longhorn volumes are faulty", lh.Faulty, lh.Total))
		}
		if lh.Degraded > 0 {
			notes = append(notes, fmt.Sprintf("%d of %d longhorn volumes are degraded, rebuilding", lh.Degraded, lh.Total))
		}
	}
	// The platform layer is not this run's to fix, but a cluster brought back
	// with a dead network layer is not really back. Only said when Cilium is
	// installed, and only when it can be read.
	if vip := e.cfg.Platform.VIP; vip != "" {
		if p, err := e.ops.ClusterProbe(ctx, vip); err == nil && p.CiliumPresent {
			if state, detail := PlatformState(vip, p); state != PlatformOK {
				notes = append(notes, "the platform layer is not healthy ("+detail+"): run Restore API VIP")
			}
		}
	}
	if ns := e.cfg.ClusterStart.VaultNamespace; ns != "" {
		if v, err := e.ops.VaultSealed(ctx, ns); err != nil {
			notes = append(notes, "vault state could not be read: "+err.Error())
		} else if len(v.Sealed) > 0 {
			notes = append(notes, "vault is sealed ("+strings.Join(v.Sealed, ", ")+"), it needs unsealing")
		}
	}
	if len(notes) > 0 {
		j.Reason = strings.Join(notes, "; ")
	}
	e.note(ctx, "", "health: "+firstNonEmpty(j.Reason, "all clear"))
	if fatal {
		return Failed
	}
	return Completed
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

func (e *Engine) clusterStartSpec() Spec {
	w := e.cfg.ClusterStart.Waits
	wait := func(d time.Duration) func(config.Timing, *Job) time.Duration {
		return func(config.Timing, *Job) time.Duration { return d }
	}
	return Spec{
		Name: WfClusterStart,
		Edges: []Edge{
			{Started, AwaitingNodes, "masters being awaited", Event},
			{AwaitingNodes, AwaitingQuorum, "every master answers", Event},
			{AwaitingNodes, AwaitingQuorum, "timeout, a majority answers", Timeout},
			{AwaitingNodes, Failed, "timeout, no majority", Timeout},
			{AwaitingQuorum, RejoiningMasters, "etcd majority answering", Event},
			{AwaitingQuorum, Failed, "timeout", Timeout},
			{RejoiningMasters, AwaitingMasterRejoined, "next master rejoin started", Event},
			{RejoiningMasters, RejoiningAgents, "no master left", Event},
			{AwaitingMasterRejoined, RejoiningMasters, "child settled", Event},
			{AwaitingMasterRejoined, RejoiningMasters, "timeout, continue", Timeout},
			{RejoiningAgents, AwaitingAgentsRejoined, "agent runs started", Event},
			{RejoiningAgents, CheckingHealth, "no agent needs it", Event},
			{AwaitingAgentsRejoined, CheckingHealth, "every agent child settled", Event},
			{AwaitingAgentsRejoined, CheckingHealth, "timeout, continue", Timeout},
			{CheckingHealth, Completed, "healthy, or notes only", Event},
			{CheckingHealth, Failed, "faulty volume, or a node did not rejoin", Fail},
		},
		Steps: map[St]stepFn{
			Started:          e.hStartStarted,
			RejoiningMasters: e.hRejoiningMasters, RejoiningAgents: e.hRejoiningAgents, CheckingHealth: e.hCheckingHealth,
		},
		Waits: map[St]Wait{
			AwaitingNodes: {Timer: wait(w.Nodes), OnEvent: e.onNodes, OnTimeout: e.onNodesTimeout},
			AwaitingQuorum: {Timer: wait(w.Quorum), OnEvent: e.onQuorum,
				OnTimeout: timeoutTo(Failed, "no etcd majority answered in time")},
			AwaitingMasterRejoined: {Timer: wait(w.Rejoin), OnEvent: e.onMasterRejoined,
				OnTimeout: func(_ context.Context, r *Run) St {
					r.Object.Failures = append(r.Object.Failures, r.Object.Current+": rejoin did not finish in time")
					return RejoiningMasters
				}},
			AwaitingAgentsRejoined: {Timer: wait(w.Rejoin), OnEvent: e.onAgentsRejoined,
				OnTimeout: func(_ context.Context, r *Run) St {
					r.Object.Failures = append(r.Object.Failures, "agents: rejoin did not finish in time")
					return CheckingHealth
				}},
		},
	}
}
