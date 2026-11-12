package flows

import (
	"strings"
	"testing"
	"time"
)

func TestPowerOffGracefulHappyPath(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.start(WfPowerOff, "m1", ModeGraceful)
	h.waitFor("m1", AwaitingK3sInactive)
	h.stays("m1", AwaitingK3sInactive)
	k3sStops(h, "m1")
	h.waitFor("m1", AwaitingNodeDown)
	if !h.ops.called("poweroff", "m1") || h.ops.called("poweroff-force", "m1") {
		t.Fatalf("calls %v", h.ops.calls)
	}
	h.stays("m1", AwaitingNodeDown)
	h.down("m1")
	r := h.waitFor("m1", PoweredOff)
	if r.Final != "completed" || r.Reason != "" {
		t.Fatalf("%+v", r)
	}
}

func TestPowerOffEscalatesToForceWhenGracefulIsTooSlow(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.start(WfPowerOff, "m1", ModeGraceful)
	h.waitFor("m1", AwaitingK3sInactive)
	k3sStops(h, "m1")
	h.waitFor("m1", AwaitingNodeDown)
	h.expire("m1", 100*time.Second, AwaitingForcedDown)
	if !h.ops.called("poweroff-force", "m1") {
		t.Fatalf("calls %v", h.ops.calls)
	}
	h.down("m1")
	r := h.waitFor("m1", PoweredOff)
	if !strings.Contains(r.Reason, "forcing") {
		t.Fatalf("reason should record the escalation: %+v", r)
	}
}

func TestPowerOffForceModeFailsWhenStillReachable(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.start(WfPowerOff, "m1", ModeForce)
	h.waitFor("m1", AwaitingK3sInactive)
	k3sStops(h, "m1")
	h.waitFor("m1", AwaitingNodeDown)
	if !h.ops.called("poweroff-force", "m1") || !h.ops.called("kill", "m1") {
		t.Fatalf("calls %v", h.ops.calls)
	}
	r := h.expire("m1", 3*time.Minute, Failed)
	if !strings.Contains(r.Reason, "forced poweroff") {
		t.Fatalf("%+v", r)
	}
}

func TestPowerOffForcedPoweroffTimesOut(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.start(WfPowerOff, "m1", ModeGraceful)
	h.waitFor("m1", AwaitingK3sInactive)
	k3sStops(h, "m1")
	h.waitFor("m1", AwaitingNodeDown)
	h.expire("m1", 100*time.Second, AwaitingForcedDown)
	r := h.expire("m1", 3*time.Minute, Failed)
	if !strings.Contains(r.Reason, "poweroff --force") {
		t.Fatalf("%+v", r)
	}
}

func TestPowerOffCallFailures(t *testing.T) {
	t.Parallel()
	t.Run("poweroff", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t)
		h.ops.failOn("poweroff")
		h.start(WfPowerOff, "m1", ModeGraceful)
		h.waitFor("m1", AwaitingK3sInactive)
		k3sStops(h, "m1")
		r := h.waitFor("m1", Failed)
		if !strings.Contains(r.Reason, "poweroff call failed") {
			t.Fatalf("%+v", r)
		}
	})
	t.Run("forced poweroff", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t)
		h.ops.failOn("poweroff-force")
		h.start(WfPowerOff, "m1", ModeGraceful)
		h.waitFor("m1", AwaitingK3sInactive)
		k3sStops(h, "m1")
		h.waitFor("m1", AwaitingNodeDown)
		r := h.expire("m1", 100*time.Second, Failed)
		if !strings.Contains(r.Reason, "forced poweroff call failed") {
			t.Fatalf("%+v", r)
		}
	})
}

func TestPowerOffRefusedAndRolledBack(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.ops.quorum = false
	h.start(WfPowerOff, "m1", ModeGraceful)
	h.waitFor("m1", Refused)

	h2 := newHarness(t)
	h2.ops.risk = []string{"vol-9"}
	h2.start(WfPowerOff, "m2", ModeGraceful)
	r := h2.waitFor("m2", Failed)
	if !strings.Contains(r.Reason, "vol-9") || h2.ops.called("poweroff", "m2") {
		t.Fatalf("%+v", r)
	}

	h3 := newHarness(t)
	h3.ops.failOn("stop")
	h3.start(WfPowerOff, "m3", ModeGraceful)
	h3.waitFor("m3", Failed)
	if !h3.ops.called("uncordon", "m3") {
		t.Fatal("stop failure must roll back")
	}
}

func TestPowerOffK3sNeverStops(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.start(WfPowerOff, "m1", ModeGraceful)
	h.waitFor("m1", AwaitingK3sInactive)
	r := h.expire("m1", 2*time.Minute, Failed)
	if !strings.Contains(r.Reason, "k3s did not stop") || h.ops.called("poweroff", "m1") {
		t.Fatalf("%+v", r)
	}
}

func TestPowerOffClearanceTimeout(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	cl := &Job{Node: "m1", Role: "master", Mode: ModeGraceful, Cluster: true, Parent: "parent"}
	h.eng.reg.mu.Lock()
	h.eng.reg.active[clusterKey] = &RunInfo{key: clusterKey}
	h.eng.reg.mu.Unlock()
	if _, err := h.eng.start(t.Context(), h.eng.wfs[WfPowerOff], "m1", cl); err != nil {
		t.Fatal(err)
	}
	h.waitFor("m1", AwaitingK3sInactive)
	k3sStops(h, "m1")
	h.waitFor("m1", AwaitingClearance)
	h.stays("m1", AwaitingClearance)
	if h.ops.called("poweroff", "m1") {
		t.Fatal("powered off without clearance")
	}
	r := h.expire("m1", 11*time.Minute, Failed)
	if !strings.Contains(r.Reason, "never released") {
		t.Fatalf("%+v", r)
	}
}

func TestPowerOffClearanceReleases(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	cl := &Job{Node: "m1", Role: "master", Mode: ModeGraceful, Cluster: true, Parent: "parent"}
	h.eng.reg.mu.Lock()
	h.eng.reg.active[clusterKey] = &RunInfo{key: clusterKey}
	h.eng.reg.mu.Unlock()
	if _, err := h.eng.start(t.Context(), h.eng.wfs[WfPowerOff], "m1", cl); err != nil {
		t.Fatal(err)
	}
	h.waitFor("m1", AwaitingK3sInactive)
	k3sStops(h, "m1")
	h.waitFor("m1", AwaitingClearance)
	h.eng.reg.clear("m1")
	h.waitFor("m1", AwaitingNodeDown)
	if !h.ops.called("poweroff", "m1") {
		t.Fatalf("calls %v", h.ops.calls)
	}
	// A cluster child skips the quorum and replica gates.
	if h.ops.called("risk", "m1") {
		t.Fatal("cluster child consulted the replica gate")
	}
}

func TestForceAfterOverridesGracefulWindow(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	j := &Job{Node: "m1", Role: "master", Mode: ModeGraceful, ForceAfter: 10 * time.Second}
	if _, err := h.eng.start(t.Context(), h.eng.wfs[WfPowerOff], "m1", j); err != nil {
		t.Fatal(err)
	}
	h.waitFor("m1", AwaitingK3sInactive)
	k3sStops(h, "m1")
	h.waitFor("m1", AwaitingNodeDown)
	h.expire("m1", 11*time.Second, AwaitingForcedDown)
}
