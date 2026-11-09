package flows

import (
	"strings"
	"testing"
	"time"

	"github.com/kostiantyn-matsebora/barekube/console/internal/events"
	"github.com/kostiantyn-matsebora/barekube/console/internal/observe"
)

// rebootThrough drives a reboot to Completed the way a real node behaves.
func rebootThrough(t *testing.T, h *harness, node string) {
	t.Helper()
	h.waitFor(node, AwaitingNodeDown)
	h.stays(node, AwaitingNodeDown)
	h.down(node)
	h.waitFor(node, AwaitingNodeUp)
	h.set(node, func(o *observe.Observation) { *o = up(); o.Cordoned = observe.Yes })
	h.waitFor(node, AwaitingUncordoned)
	h.set(node, func(o *observe.Observation) { o.Cordoned = observe.No })
	h.waitFor(node, Completed)
}

func TestRebootGracefulAgentHappyPath(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.ops.quorum = false // an agent never needs quorum
	h.start(WfReboot, "a1", ModeGraceful)
	rebootThrough(t, h, "a1")
	for _, want := range []string{"cordon", "lh-off", "risk", "drain", "reboot", "lh-on", "uncordon"} {
		if !h.ops.called(want, "a1") {
			t.Errorf("%s was not called", want)
		}
	}
	if h.ops.count("poweroff") != 0 {
		t.Error("a reboot must not power anything off")
	}
	r := h.info("a1")
	if !r.Terminal || r.Final != "completed" || r.Reason != "" {
		t.Fatalf("final run %+v", r)
	}
	for _, k := range []string{events.RunTriggered, events.RunStatus, events.ActionOK, events.RunCompleted, events.NodeUnreachable} {
		if !h.hasEvent(k, "a1") && k != events.NodeUnreachable {
			t.Errorf("no %s event: %v", k, h.kinds("a1"))
		}
	}
}

func TestRebootForceMasterSkipsReplicaGate(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.ops.risk = []string{"vol-1"} // would stop a graceful run
	h.start(WfReboot, "m1", ModeForce)
	rebootThrough(t, h, "m1")
	if h.ops.called("risk", "m1") {
		t.Error("force must not consult the replica gate")
	}
}

func TestRebootNeverMovesOnAnAssumption(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.start(WfReboot, "a1", ModeGraceful)
	h.waitFor("a1", AwaitingNodeDown)
	// Unknown is not unreachable: a probe that could not answer proves nothing.
	h.set("a1", func(o *observe.Observation) { o.Reachable = observe.Unknown })
	h.stays("a1", AwaitingNodeDown)
	h.down("a1")
	h.waitFor("a1", AwaitingNodeUp)
	// Reachable but not Ready is not back.
	h.set("a1", func(o *observe.Observation) { *o = up(); o.Ready = observe.No; o.Cordoned = observe.Yes })
	h.stays("a1", AwaitingNodeUp)
	h.set("a1", func(o *observe.Observation) { o.Ready = observe.Unknown })
	h.stays("a1", AwaitingNodeUp)
}

func TestStaleObservationDoesNotResolveAWait(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.start(WfReboot, "a1", ModeGraceful)
	h.waitFor("a1", AwaitingNodeDown)
	h.clk.Step(time.Minute)
	stale := observe.Observation{Node: "a1", At: h.clk.Now().Add(-time.Hour), Reachable: observe.No}
	for range 5 {
		h.eng.OnObservations([]observe.Observation{stale})
		time.Sleep(2 * time.Millisecond)
	}
	if got := h.info("a1").st; got != AwaitingNodeDown {
		t.Fatalf("a stale observation moved the run to %s", got)
	}
}

func TestRebootRefusedWhenMasterQuorumUnhealthy(t *testing.T) {
	t.Parallel()
	for _, mode := range []Mode{ModeGraceful, ModeForce} {
		h := newHarness(t)
		h.ops.quorum = false
		h.start(WfReboot, "m1", mode)
		r := h.waitFor("m1", Refused)
		if r.Final != "refused" || !strings.Contains(r.Reason, "quorum") {
			t.Fatalf("%s: %+v", mode, r)
		}
		if h.ops.called("cordon", "m1") {
			t.Fatalf("%s: a refused run touched the node", mode)
		}
		if !h.hasEvent(events.RunRefused, "m1") {
			t.Fatalf("%s: no run.refused event", mode)
		}
	}
}

func TestRebootGracefulPrepareFailuresRollBack(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		setup  func(*fakeOps)
		reason string
	}{
		{"cordon", func(o *fakeOps) { o.failOn("cordon") }, "cordon failed"},
		{"longhorn scheduling", func(o *fakeOps) { o.failOn("lh-off") }, "longhorn scheduling"},
		{"replica risk error", func(o *fakeOps) { o.riskErr = errBoom }, "replica risk check failed"},
		{"replica risk volumes", func(o *fakeOps) { o.risk = []string{"vol-7"} }, "vol-7"},
		{"drain", func(o *fakeOps) { o.failOn("drain") }, "drain failed"},
		{"reboot call", func(o *fakeOps) { o.failOn("reboot") }, "reboot call failed"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			h := newHarness(t)
			c.setup(h.ops)
			h.start(WfReboot, "a1", ModeGraceful)
			r := h.waitFor("a1", Failed)
			if !strings.Contains(r.Reason, c.reason) {
				t.Fatalf("reason %q lacks %q", r.Reason, c.reason)
			}
			// The node is put back as it was.
			if !h.ops.called("lh-on", "a1") || !h.ops.called("uncordon", "a1") {
				t.Fatalf("no rollback: %v", h.ops.calls)
			}
			if h.ops.called("reboot", "a1") && c.name != "reboot call" {
				t.Fatal("rebooted despite a failed preparation")
			}
		})
	}
}

func TestRebootForceCarriesOnWhateverFails(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.ops.failOn("cordon")
	h.ops.failOn("lh-off")
	h.ops.failOn("drain")
	h.start(WfReboot, "a1", ModeForce)
	h.waitFor("a1", AwaitingNodeDown)
	if !h.ops.called("reboot", "a1") {
		t.Fatal("force did not reboot after failed preparation")
	}
	for _, e := range h.log.List(events.Filter{Node: "a1"}) {
		if e.Kind == events.ActionFailed && strings.HasPrefix(e.Detail, "cordon") {
			return
		}
	}
	t.Fatal("the failed cordon left no action.failed event")
}

func TestRebootTimeouts(t *testing.T) {
	t.Parallel()
	t.Run("node never goes down", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t)
		h.start(WfReboot, "a1", ModeGraceful)
		h.waitFor("a1", AwaitingNodeDown)
		r := h.expire("a1", 3*time.Minute, Failed)
		if !strings.Contains(r.Reason, "never went unreachable") || !h.ops.called("uncordon", "a1") {
			t.Fatalf("%+v calls=%v", r, h.ops.calls)
		}
		if !h.hasEvent(events.TimeoutFired, "a1") {
			t.Fatal("no timeout.fired event")
		}
	})
	t.Run("node never comes back", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t)
		h.start(WfReboot, "a1", ModeGraceful)
		h.waitFor("a1", AwaitingNodeDown)
		h.down("a1")
		h.waitFor("a1", AwaitingNodeUp)
		r := h.expire("a1", 6*time.Minute, Failed)
		if !strings.Contains(r.Reason, "Ready") {
			t.Fatalf("%+v", r)
		}
	})
	t.Run("node stays cordoned", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t)
		h.start(WfReboot, "a1", ModeGraceful)
		h.waitFor("a1", AwaitingNodeDown)
		h.down("a1")
		h.waitFor("a1", AwaitingNodeUp)
		h.set("a1", func(o *observe.Observation) { *o = up(); o.Cordoned = observe.Yes })
		h.waitFor("a1", AwaitingUncordoned)
		r := h.expire("a1", time.Minute, Failed)
		if !strings.Contains(r.Reason, "schedulable") {
			t.Fatalf("%+v", r)
		}
	})
}

func TestRebootUncordonFailureEndsFailed(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.start(WfReboot, "a1", ModeGraceful)
	h.waitFor("a1", AwaitingNodeDown)
	h.down("a1")
	h.waitFor("a1", AwaitingNodeUp)
	h.ops.mu.Lock()
	h.ops.fail["uncordon"] = errBoom
	h.ops.mu.Unlock()
	h.set("a1", func(o *observe.Observation) { *o = up(); o.Cordoned = observe.Yes })
	r := h.waitFor("a1", Failed)
	if !strings.Contains(r.Reason, "uncordon failed") {
		t.Fatalf("%+v", r)
	}
}
