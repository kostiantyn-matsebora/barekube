package flows

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/kostiantyn-matsebora/barekube/console/internal/config"
	"github.com/kostiantyn-matsebora/barekube/console/internal/events"
	"github.com/kostiantyn-matsebora/barekube/console/internal/observe"
)

// powerDown plays the node: once its k3s is told to stop it reports inactive,
// and once powered off it reports unreachable.
func (h *harness) follow() {
	h.ops.mu.Lock()
	calls := append([]string(nil), h.ops.calls...)
	h.ops.mu.Unlock()
	for _, c := range calls {
		var op, n string
		_, _ = sscan(c, &op, &n)
		switch op {
		case "stop", "kill":
			if h.facts[n].Reachable == observe.Yes {
				h.set(n, func(o *observe.Observation) { o.K3sActive = observe.No })
			}
		case "poweroff", "poweroff-force":
			h.down(n)
		}
	}
}

func sscan(c string, op, n *string) (int, error) {
	parts := strings.Fields(c)
	if len(parts) == 2 {
		*op, *n = parts[0], parts[1]
		return 2, nil
	}
	return 0, nil
}

// driveCluster ticks and plays the nodes until the cluster run reaches want.
func (h *harness) driveCluster(want St) RunInfo {
	h.t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if r := h.info(clusterKey); r.st == want {
			return r
		}
		h.follow()
		h.tick()
		time.Sleep(3 * time.Millisecond)
	}
	h.t.Fatalf("cluster: wanted %s, run is %+v", want, h.info(clusterKey))
	return RunInfo{}
}

func TestClusterShutdownHappyPath(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.ops.quorum = false // the cluster shutdown bypasses the quorum gate
	h.ops.risk = []string{"vol-1"}
	if _, err := h.eng.StartCluster(context.Background(), "ha"); err != nil {
		t.Fatal(err)
	}
	r := h.driveCluster(Completed)
	if r.Final != "completed" {
		t.Fatalf("%+v", r)
	}
	// Agents stand by with k3s killed (force), masters power off, leader m2 last.
	for _, a := range []string{"a1", "a2"} {
		if !h.ops.called("kill", a) || h.ops.called("poweroff", a) {
			t.Errorf("agent %s: %v", a, h.ops.calls)
		}
	}
	order := h.ops.order("poweroff")
	if len(order) != 3 || order[2] != "m2" {
		t.Fatalf("masters must power off with the leader last: %v", order)
	}
	if h.ops.called("risk", "m1") {
		t.Fatal("cluster children must skip the replica gate")
	}
	if h.ops.count("poweroff-force") != 0 {
		t.Fatal("graceful poweroffs completed, none should be forced")
	}
	for _, k := range []string{events.ChildAwaiting, events.Clearance, events.ChildCompleted} {
		if !h.hasEvent(k, "m1") {
			t.Errorf("no %s event for m1: %v", k, h.kinds("m1"))
		}
	}
}

func TestClusterReleasesMastersOneAtATime(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.eng.StartCluster(context.Background(), "ha")
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) && h.info(clusterKey).st != Completed {
		h.follow()
		h.tick()
		// A master that was powered off but is not yet observed down must be
		// the only one: the next one is not released before it is gone.
		pending := 0
		for _, m := range h.ops.order("poweroff") {
			if h.facts[m].Reachable != observe.No {
				pending++
			}
		}
		if pending > 1 {
			t.Fatalf("%d masters powered off at once: %v", pending, h.ops.calls)
		}
		time.Sleep(3 * time.Millisecond)
	}
	if h.info(clusterKey).st != Completed {
		t.Fatalf("%+v", h.info(clusterKey))
	}
}

func TestClusterLeaderUnknownKeepsConfiguredOrder(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.ops.leaderErr = errBoom
	h.eng.StartCluster(context.Background(), "ha")
	h.driveCluster(Completed)
	order := h.ops.order("poweroff")
	if strings.Join(order, ",") != "m1,m2,m3" {
		t.Fatalf("order %v", order)
	}
}

func TestClusterLeaderLastDisabled(t *testing.T) {
	t.Parallel()
	h := newHarness(t, func(c *config.Config) { c.ClusterShutdown.Masters.LeaderLast = false })
	h.eng.StartCluster(context.Background(), "ha")
	h.driveCluster(Completed)
	if strings.Join(h.ops.order("poweroff"), ",") != "m1,m2,m3" {
		t.Fatalf("order %v", h.ops.order("poweroff"))
	}
}

func TestClusterCordonAllCanBeDisabled(t *testing.T) {
	t.Parallel()
	count := func(off bool) int {
		h := newHarness(t, func(c *config.Config) {
			if off {
				f := false
				c.ClusterShutdown.CordonAll = &f
			}
		})
		h.eng.StartCluster(context.Background(), "ha")
		h.driveCluster(Completed)
		return len(h.ops.order("cordon"))
	}
	// Each node's child cordons it again, so enabling adds one call per node.
	if on, off := count(false), count(true); on-off != 5 {
		t.Fatalf("cordon calls: %d enabled, %d disabled", on, off)
	}
}

func TestClusterFailsWhenAMasterDoesNotShutDown(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.ops.failOn("poweroff m3")
	h.ops.failOn("poweroff-force m3")
	h.eng.StartCluster(context.Background(), "ha")
	r := h.driveCluster(Failed)
	if !strings.Contains(r.Reason, "m3") {
		t.Fatalf("%+v", r)
	}
	if !h.hasEvent(events.RunFailed, "") {
		t.Fatal("no run.failed event")
	}
}

func TestClusterContinuesPastAFailedAgent(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.ops.failOn("kill a1")
	h.eng.StartCluster(context.Background(), "ha")
	h.driveCluster(Completed)
	if !h.ops.called("poweroff", "m1") {
		t.Fatal("masters must still be powered off")
	}
}

func TestClusterOnlyOneAtATime(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	if _, err := h.eng.StartCluster(context.Background(), "ha"); err != nil {
		t.Fatal(err)
	}
	if _, err := h.eng.StartCluster(context.Background(), "ha"); err != ErrClusterActive {
		t.Fatalf("second cluster run: %v", err)
	}
	if _, err := h.eng.StartNode(context.Background(), WfReboot, "a1", ModeGraceful, "ui"); err != ErrClusterRunning {
		t.Fatalf("node run during shutdown: %v", err)
	}
}

func TestClusterAgentWaitTimeoutContinues(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.eng.StartCluster(context.Background(), "ha")
	h.waitFor(clusterKey, AwaitingAgentsStoodBy) // nodes never answer: no follow()
	r := h.expire(clusterKey, 4*time.Minute, AwaitingMastersDrained)
	_ = r
}

func TestClusterMastersDrainedTimeoutContinues(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.eng.StartCluster(context.Background(), "ha")
	// Agents settle, masters never finish draining their k3s.
	for h.info(clusterKey).st != AwaitingMastersDrained {
		for _, a := range []string{"a1", "a2"} {
			h.set(a, func(o *observe.Observation) { o.K3sActive = observe.No })
		}
		h.tick()
		time.Sleep(3 * time.Millisecond)
	}
	h.expire(clusterKey, 6*time.Minute, AwaitingMasterDown)
}

func TestClusterMasterDownTimeout(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.eng.StartCluster(context.Background(), "ha")
	for h.info(clusterKey).st != AwaitingMasterDown {
		h.follow()
		// masters stop k3s but never actually go down after poweroff
		for _, c := range h.ops.order("poweroff") {
			h.set(c, func(o *observe.Observation) { o.Reachable = observe.Yes })
		}
		h.tick()
		time.Sleep(3 * time.Millisecond)
	}
	h.expire(clusterKey, 5*time.Minute, Failed)
}

func TestOrderMastersAndNames(t *testing.T) {
	t.Parallel()
	m := []string{"a", "b", "c"}
	if got := orderMasters(m, "a", true); strings.Join(got, "") != "bca" {
		t.Fatal(got)
	}
	if got := orderMasters(m, "x", true); strings.Join(got, "") != "abc" {
		t.Fatal(got)
	}
	if got := orderMasters(m, "a", false); strings.Join(got, "") != "abc" {
		t.Fatal(got)
	}
	if got := names([]config.Node{{Name: "q"}}); got[0] != "q" {
		t.Fatal(got)
	}
}

func TestClusterSnapshotsBeforeTakingAnythingDown(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.eng.StartCluster(context.Background(), "ha")
	h.driveCluster(Completed)
	h.ops.mu.Lock()
	defer h.ops.mu.Unlock()
	if len(h.ops.calls) == 0 || h.ops.calls[0] != "snapshot " && !strings.HasPrefix(h.ops.calls[0], "snapshot") {
		t.Fatalf("snapshot must be the first action: %v", h.ops.calls)
	}
	n := 0
	for _, c := range h.ops.calls {
		if strings.HasPrefix(c, "snapshot") {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("%d snapshots", n)
	}
}

func TestClusterGoesOnWhenTheSnapshotFails(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.ops.snapshotErr = errBoom
	h.eng.StartCluster(context.Background(), "ha")
	r := h.driveCluster(Completed)
	if r.Reason != "" {
		t.Fatalf("a failed snapshot must not mark the run: %+v", r)
	}
	if !h.hasEvent(events.ActionFailed, "") {
		t.Fatal("the failed snapshot left no action.failed event")
	}
}

func TestClusterSnapshotCanBeDisabled(t *testing.T) {
	t.Parallel()
	off := false
	h := newHarness(t, func(c *config.Config) { c.ClusterShutdown.Snapshot = &off })
	h.eng.StartCluster(context.Background(), "ha")
	h.driveCluster(Completed)
	if h.ops.count("snapshot") != 0 {
		t.Fatal("snapshot ran while disabled")
	}
}

// The rehearsal profile: every node force stands by, nothing powers off.
func TestClusterRehearsalProfileStandsEveryNodeBy(t *testing.T) {
	t.Parallel()
	h := newHarness(t, func(c *config.Config) {
		c.ClusterShutdown.Masters = config.Policy{Action: "standby", Mode: "force"}
	})
	h.ops.quorum = false
	h.eng.StartCluster(context.Background(), "ha")
	r := h.driveCluster(Completed)
	if r.Final != "completed" {
		t.Fatalf("%+v", r)
	}
	for _, n := range []string{"m1", "m2", "m3", "a1", "a2"} {
		if !h.ops.called("kill", n) {
			t.Errorf("%s: k3s was not killed: %v", n, h.ops.calls)
		}
	}
	if h.ops.count("poweroff") != 0 {
		t.Fatal("the rehearsal must never power a machine off")
	}
	if h.ops.count("snapshot") != 1 {
		t.Fatal("the snapshot still comes first")
	}
	for _, n := range []string{"m1", "m2", "m3"} {
		if info, ok := h.eng.reg.latestRun(n); !ok || info.Workflow != WfStandBy || info.st != StoodBy {
			t.Errorf("%s child: %+v", n, info)
		}
	}
}

func TestRepeatedAutomationCallsAreHarmless(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	ctx := context.Background()
	h.set("a1", func(o *observe.Observation) { o.K3sActive = observe.No })
	h.tick()
	for _, c := range []struct{ wf, node string }{{WfStandBy, "a1"}, {WfPowerOff, "m1"}} {
		if c.node == "m1" {
			h.down("m1")
			h.tick()
		}
		if _, err := h.eng.StartNode(ctx, c.wf, c.node, ModeForce, "automation"); !errors.Is(err, ErrNothingToDo) {
			t.Errorf("%s %s on a parked node: %v", c.wf, c.node, err)
		}
	}
	if len(h.eng.Runs()) != 0 || h.ops.count("kill") != 0 {
		t.Fatal("a refused call must not start a run or touch a node")
	}
	// Reboot and rejoin are never refused on this ground.
	if _, err := h.eng.StartNode(ctx, WfReboot, "a2", ModeGraceful, "ui"); err != nil {
		t.Fatal(err)
	}
	// Unobserved or unknown is not parked: not knowing proves nothing.
	h.obs.mu.Lock()
	h.obs.hidden["a2"] = true
	h.obs.mu.Unlock()
	h.eng.reg.mu.Lock()
	delete(h.eng.reg.active, "a2")
	h.eng.reg.mu.Unlock()
	if _, err := h.eng.StartNode(ctx, WfStandBy, "a2", ModeForce, "ui"); err != nil {
		t.Fatalf("an unobserved node must be allowed: %v", err)
	}
}

func TestClusterShutdownRefusedWhenEveryNodeIsParked(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	for _, n := range []string{"m1", "m2", "m3", "a1", "a2"} {
		h.set(n, func(o *observe.Observation) { o.K3sActive = observe.No })
	}
	h.tick()
	if _, err := h.eng.StartCluster(context.Background(), "automation"); !errors.Is(err, ErrNothingToDo) {
		t.Fatalf("%v", err)
	}
	// One node still serving is enough to run.
	h.set("a2", func(o *observe.Observation) { *o = up() })
	h.tick()
	if _, err := h.eng.StartCluster(context.Background(), "automation"); err != nil {
		t.Fatal(err)
	}
}
