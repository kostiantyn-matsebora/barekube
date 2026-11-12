package flows

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/kostiantyn-matsebora/barekube/console/internal/config"
	"github.com/kostiantyn-matsebora/barekube/console/internal/observe"
)

func k3sStops(h *harness, node string) {
	h.set(node, func(o *observe.Observation) { o.K3sActive = observe.No })
}

func TestStandByGracefulStopsK3s(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.start(WfStandBy, "m1", ModeGraceful)
	h.waitFor("m1", AwaitingK3sInactive)
	h.stays("m1", AwaitingK3sInactive) // k3s still active: not stood by
	if !h.ops.called("stop", "m1") || h.ops.called("kill", "m1") {
		t.Fatalf("graceful must stop, not kill: %v", h.ops.calls)
	}
	k3sStops(h, "m1")
	r := h.waitFor("m1", StoodBy)
	if !r.Terminal || r.Final != "completed" {
		t.Fatalf("%+v", r)
	}
}

func TestStandByForceKillsK3sAndSkipsReplicaGate(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.ops.risk = []string{"vol-1"}
	h.start(WfStandBy, "a2", ModeForce)
	h.waitFor("a2", AwaitingK3sInactive)
	if !h.ops.called("kill", "a2") || h.ops.called("stop", "a2") || h.ops.called("risk", "a2") {
		t.Fatalf("calls %v", h.ops.calls)
	}
	k3sStops(h, "a2")
	h.waitFor("a2", StoodBy)
}

func TestStandByRefusedWithoutQuorum(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.ops.quorum = false
	h.start(WfStandBy, "m3", ModeForce)
	r := h.waitFor("m3", Refused)
	if !strings.Contains(r.Reason, "quorum") || h.ops.called("kill", "m3") {
		t.Fatalf("%+v %v", r, h.ops.calls)
	}
	// An agent is not behind the gate.
	h.start(WfStandBy, "a1", ModeForce)
	h.waitFor("a1", AwaitingK3sInactive)
}

func TestStandByFailuresRollBack(t *testing.T) {
	t.Parallel()
	cases := map[string]struct{ fail, reason string }{
		"cordon": {"cordon", "cordon failed"},
		"drain":  {"drain", "drain failed"},
		"stop":   {"stop", "stopping k3s failed"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			h := newHarness(t)
			h.ops.failOn(c.fail)
			h.start(WfStandBy, "a1", ModeGraceful)
			r := h.waitFor("a1", Failed)
			if !strings.Contains(r.Reason, c.reason) || !h.ops.called("uncordon", "a1") {
				t.Fatalf("%+v %v", r, h.ops.calls)
			}
		})
	}
}

func TestStandByTimesOutWhenK3sKeepsRunning(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.start(WfStandBy, "a1", ModeGraceful)
	h.waitFor("a1", AwaitingK3sInactive)
	r := h.expire("a1", 2*time.Minute, Failed)
	if !strings.Contains(r.Reason, "k3s did not stop") {
		t.Fatalf("%+v", r)
	}
}

// settleHarness runs with the real settle window and drives time by hand.
func settleHarness(t *testing.T) *harness {
	return newHarness(t, func(c *config.Config) { c.Timing.Settle = 10 * time.Second })
}

// observe delivers one observation round at the current fake time.
func (h *harness) observeAt() {
	var batch []observe.Observation
	for _, n := range h.eng.cfg.Nodes {
		o := h.facts[n.Name]
		o.Node, o.At = n.Name, h.clk.Now()
		batch = append(batch, o)
		h.obs.mu.Lock()
		h.obs.m[n.Name] = o
		h.obs.mu.Unlock()
	}
	h.eng.OnObservations(batch)
	time.Sleep(20 * time.Millisecond)
}

func (h *harness) step(d time.Duration) { h.clk.Step(d) }

// The agent-2 incident: the kill is answered with one "inactive", systemd
// restarts k3s five seconds later, and the run had already said StoodBy.
func TestStandByIsNotCompletedByATransientInactive(t *testing.T) {
	t.Parallel()
	h := settleHarness(t)
	h.start(WfStandBy, "a2", ModeForce)
	h.waitFor("a2", AwaitingK3sInactive)

	k3sStops(h, "a2") // the kill took effect
	h.observeAt()
	h.step(5 * time.Second)
	h.set("a2", func(o *observe.Observation) { o.K3sActive = observe.Yes }) // systemd restarted it
	h.observeAt()
	h.step(30 * time.Second)
	h.observeAt()
	if got := h.info("a2"); got.st != AwaitingK3sInactive || got.Terminal {
		t.Fatalf("a restarted k3s must not complete the run: %+v", got)
	}
}

func TestStandByCompletesOnlyOnceK3sStaysDown(t *testing.T) {
	t.Parallel()
	h := settleHarness(t)
	h.start(WfStandBy, "a2", ModeForce)
	h.waitFor("a2", AwaitingK3sInactive)

	k3sStops(h, "a2")
	h.observeAt()
	h.step(9 * time.Second)
	h.observeAt()
	if h.info("a2").st != AwaitingK3sInactive {
		t.Fatal("9s of 10s is not settled")
	}
	h.step(2 * time.Second)
	h.observeAt()
	if r := h.info("a2"); r.st != StoodBy || !r.Terminal {
		t.Fatalf("down for 11s must complete: %+v", r)
	}
}

func TestAFlipBackRestartsTheSettleClock(t *testing.T) {
	t.Parallel()
	h := settleHarness(t)
	h.start(WfStandBy, "a2", ModeForce)
	h.waitFor("a2", AwaitingK3sInactive)

	k3sStops(h, "a2")
	h.observeAt() // down at +0
	h.step(6 * time.Second)
	h.set("a2", func(o *observe.Observation) { o.K3sActive = observe.Yes })
	h.observeAt() // up at +6
	h.step(1 * time.Second)
	k3sStops(h, "a2")
	h.observeAt() // down again at +7
	h.step(6 * time.Second)
	h.observeAt() // +13: 13s since the first down but only 6s since the second
	if h.info("a2").st != AwaitingK3sInactive {
		t.Fatal("the clock must restart after a flip back")
	}
	h.step(5 * time.Second)
	h.observeAt() // +18: 11s since the second down
	if h.info("a2").st != StoodBy {
		t.Fatalf("%+v", h.info("a2"))
	}
}

// The second incident: the automation fired every minute and each trigger
// became a run. With k3s really down the node derives to stood by and the
// guard refuses. With a restarted k3s there was nothing to refuse.
func TestRepeatTriggerIsRefusedOnceTheNodeIsReallyStoodBy(t *testing.T) {
	t.Parallel()
	h := settleHarness(t)
	h.start(WfStandBy, "a2", ModeForce)
	h.waitFor("a2", AwaitingK3sInactive)
	k3sStops(h, "a2")
	h.observeAt()
	h.step(11 * time.Second)
	h.observeAt()
	if h.info("a2").st != StoodBy {
		t.Fatal("setup")
	}
	h.step(time.Minute)
	h.observeAt()
	_, err := h.eng.StartNode(context.Background(), WfStandBy, "a2", ModeForce, "automation")
	if !errors.Is(err, ErrNothingToDo) {
		t.Fatalf("a repeat trigger must be refused, got %v", err)
	}
	if h.ops.count("kill") != 1 {
		t.Fatalf("killed %d times", h.ops.count("kill"))
	}
}

func TestPowerOffAlsoNeedsK3sToStayDown(t *testing.T) {
	t.Parallel()
	h := settleHarness(t)
	h.start(WfPowerOff, "m1", ModeGraceful)
	h.waitFor("m1", AwaitingK3sInactive)
	k3sStops(h, "m1")
	h.observeAt()
	h.step(5 * time.Second)
	h.set("m1", func(o *observe.Observation) { o.K3sActive = observe.Yes })
	h.observeAt()
	h.step(20 * time.Second)
	h.observeAt()
	if h.info("m1").st != AwaitingK3sInactive || h.ops.called("poweroff", "m1") {
		t.Fatalf("power off must wait for a stable stop: %+v %v", h.info("m1"), h.ops.calls)
	}
}
