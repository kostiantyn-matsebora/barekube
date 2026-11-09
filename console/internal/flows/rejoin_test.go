package flows

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/kostiantyn-matsebora/barekube/console/internal/observe"
)

func parked(h *harness, node string) {
	h.set(node, func(o *observe.Observation) {
		*o = observe.Observation{Reachable: observe.Yes, K3sActive: observe.No, Ready: observe.No, Cordoned: observe.Yes}
	})
	h.tick()
}

func TestRejoinHappyPath(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	parked(h, "a1")
	h.start(WfRejoin, "a1", ModeGraceful)
	h.waitFor("a1", AwaitingK3sActive)
	if !h.ops.called("start", "a1") {
		t.Fatal("k3s was not started")
	}
	h.stays("a1", AwaitingK3sActive)
	h.set("a1", func(o *observe.Observation) { o.K3sActive = observe.Yes })
	h.waitFor("a1", AwaitingNodeReady)
	h.set("a1", func(o *observe.Observation) { o.Ready = observe.Yes })
	h.waitFor("a1", AwaitingUncordoned)
	h.set("a1", func(o *observe.Observation) { o.Cordoned = observe.No })
	r := h.waitFor("a1", Completed)
	if !h.ops.called("lh-on", "a1") || !h.ops.called("uncordon", "a1") || r.Final != "completed" {
		t.Fatalf("%+v %v", r, h.ops.calls)
	}
}

func TestRejoinPassesWaitsAlreadySatisfied(t *testing.T) {
	t.Parallel()
	// A manual power-on already auto-started k3s: the next observation is enough.
	h := newHarness(t)
	h.set("a1", func(o *observe.Observation) { o.Cordoned = observe.Yes })
	h.tick()
	h.start(WfRejoin, "a1", ModeGraceful)
	h.waitFor("a1", AwaitingUncordoned)
}

func TestRejoinRefused(t *testing.T) {
	t.Parallel()
	t.Run("node unreachable", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t)
		h.down("a1")
		h.tick()
		h.start(WfRejoin, "a1", ModeGraceful)
		r := h.waitFor("a1", Refused)
		if !strings.Contains(r.Reason, "unreachable") || h.ops.called("start", "a1") {
			t.Fatalf("%+v", r)
		}
	})
	t.Run("never observed", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t)
		h.obs.mu.Lock()
		h.obs.hidden["a1"] = true
		h.obs.mu.Unlock()
		h.start(WfRejoin, "a1", ModeGraceful)
		r := h.waitFor("a1", Refused)
		if !strings.Contains(r.Reason, "not observed") {
			t.Fatalf("%+v", r)
		}
	})
	t.Run("reachability unknown", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t)
		h.set("a1", func(o *observe.Observation) { o.Reachable = observe.Unknown })
		h.tick()
		h.start(WfRejoin, "a1", ModeGraceful)
		h.waitFor("a1", Refused)
	})
}

func TestRejoinFailures(t *testing.T) {
	t.Parallel()
	t.Run("start call fails", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t)
		parked(h, "a1")
		h.ops.failOn("start")
		h.start(WfRejoin, "a1", ModeGraceful)
		r := h.waitFor("a1", Failed)
		if !strings.Contains(r.Reason, "starting k3s failed") {
			t.Fatalf("%+v", r)
		}
	})
	t.Run("uncordon call fails", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t)
		parked(h, "a1")
		h.start(WfRejoin, "a1", ModeGraceful)
		h.waitFor("a1", AwaitingK3sActive)
		h.ops.failOn("uncordon")
		h.set("a1", func(o *observe.Observation) { o.K3sActive = observe.Yes; o.Ready = observe.Yes })
		r := h.waitFor("a1", Failed)
		if !strings.Contains(r.Reason, "uncordon failed") {
			t.Fatalf("%+v", r)
		}
	})
}

func TestRejoinTimeouts(t *testing.T) {
	t.Parallel()
	t.Run("k3s never starts", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t)
		parked(h, "a1")
		h.start(WfRejoin, "a1", ModeGraceful)
		h.waitFor("a1", AwaitingK3sActive)
		r := h.expire("a1", 2*time.Minute, Failed)
		if !strings.Contains(r.Reason, "did not start") {
			t.Fatalf("%+v", r)
		}
	})
	t.Run("node never Ready", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t)
		parked(h, "a1")
		h.start(WfRejoin, "a1", ModeGraceful)
		h.waitFor("a1", AwaitingK3sActive)
		h.set("a1", func(o *observe.Observation) { o.K3sActive = observe.Yes })
		h.waitFor("a1", AwaitingNodeReady)
		r := h.expire("a1", 6*time.Minute, Failed)
		if !strings.Contains(r.Reason, "Ready") {
			t.Fatalf("%+v", r)
		}
	})
	t.Run("node stays cordoned", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t)
		h.set("a1", func(o *observe.Observation) { o.Cordoned = observe.Yes })
		h.tick()
		h.start(WfRejoin, "a1", ModeGraceful)
		h.waitFor("a1", AwaitingUncordoned)
		h.expire("a1", time.Minute, Failed)
	})
}

func TestOnlyOneRootWorkflowAtATime(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	ctx := context.Background()
	h.start(WfReboot, "a1", ModeGraceful)
	h.waitFor("a1", AwaitingNodeDown)
	// Same node, another node, and the cluster shutdown are all refused.
	for _, c := range []struct{ wf, node string }{{WfStandBy, "a1"}, {WfStandBy, "a2"}, {WfReboot, "m1"}} {
		_, err := h.eng.StartNode(ctx, c.wf, c.node, ModeForce, "test")
		if !errors.Is(err, ErrBusy) || !strings.Contains(err.Error(), "reboot on a1") {
			t.Errorf("%s %s while a root runs: %v", c.wf, c.node, err)
		}
	}
	if _, err := h.eng.StartCluster(ctx, "test"); !errors.Is(err, ErrBusy) {
		t.Errorf("cluster shutdown while a root runs: %v", err)
	}
	if len(h.eng.Runs()) != 1 {
		t.Fatalf("a refused start left a run: %+v", h.eng.Runs())
	}
	// A finished root frees the slot.
	h.expire("a1", 3*time.Minute, Failed)
	if _, err := h.eng.StartNode(ctx, WfStandBy, "a2", ModeForce, "test"); err != nil {
		t.Fatalf("a finished root must free the slot: %v", err)
	}
}

func TestChildrenAreNotRootsAndNeverRefused(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.eng.StartCluster(context.Background(), "ha")
	h.driveCluster(Completed) // five children ran under one root
	roots := 0
	for _, r := range h.eng.Runs() {
		if r.Parent == "" {
			roots++
		}
	}
	if roots != 1 || len(h.eng.Runs()) < 6 {
		t.Fatalf("want one root and its children: %+v", h.eng.Runs())
	}
}
