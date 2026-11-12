package flows

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/kostiantyn-matsebora/barekube/console/internal/config"
	"github.com/kostiantyn-matsebora/barekube/console/internal/events"
	"github.com/kostiantyn-matsebora/barekube/console/internal/nodeagent"
	"github.com/kostiantyn-matsebora/barekube/console/internal/observe"
)

const testVIP = "192.168.1.100"

func withVIP(c *config.Config) {
	c.Platform.VIP = testVIP
	c.ClusterStart.VaultNamespace = "vault"
}

// deadProbe is the 2026-11-08 state: Cilium points at the VIP, no agent is
// ready, the VIP is dead, and the kube-vip lease is a leftover.
func deadProbe() nodeagent.ClusterProbeResponse {
	return nodeagent.ClusterProbeResponse{CiliumPresent: true, CiliumAPIHost: testVIP, CiliumDesired: 5,
		KubeVipPresent: true, KubeVipLeader: "m2", KubeVipRenewed: "t0"}
}

// recoveredProbe is what the cluster reports once the bootstrap address is on.
func recoveredProbe(leader string) nodeagent.ClusterProbeResponse {
	return nodeagent.ClusterProbeResponse{VIPAnswers: true, CiliumPresent: true, CiliumReadyCount: 3, CiliumDesired: 5,
		CiliumAPIHost: testVIP, KubeVipPresent: true, KubeVipLeader: leader, KubeVipRenewed: "t1"}
}

// sim plays the cluster: every node is up but cordoned, the platform layer is
// deadlocked, and the cluster reacts to what a run does to it.
type sim struct {
	h *harness
	// afterOn is the probe once the bootstrap address is on, afterOff the probe
	// once it is gone again.
	afterOn, afterOff *nodeagent.ClusterProbeResponse
	onDone, offDone   bool
}

func newSim(t *testing.T, mutate ...func(*config.Config)) *sim {
	t.Helper()
	h := newHarness(t, append([]func(*config.Config){withVIP}, mutate...)...)
	for _, n := range h.eng.cfg.Nodes {
		h.set(n.Name, func(o *observe.Observation) { o.Cordoned = observe.Yes })
	}
	h.tick() // the engine sees the cordoned nodes before a run is asked for
	h.ops.probe = deadProbe()
	up := recoveredProbe("m3")
	return &sim{h: h, afterOn: &up, afterOff: &up}
}

func (s *sim) setProbe(f func(*nodeagent.ClusterProbeResponse)) {
	s.h.ops.mu.Lock()
	f(&s.h.ops.probe)
	s.h.ops.mu.Unlock()
}

func (s *sim) follow() {
	h := s.h
	h.ops.mu.Lock()
	calls := append([]string(nil), h.ops.calls...)
	h.ops.mu.Unlock()
	for _, c := range calls {
		var op, n string
		_, _ = sscan(c, &op, &n)
		switch op {
		case "uncordon":
			h.set(n, func(o *observe.Observation) { o.Cordoned = observe.No })
		case "vip-on":
			if !s.onDone {
				s.onDone = true
				s.setProbe(func(p *nodeagent.ClusterProbeResponse) { *p = *s.afterOn })
			}
		case "vip-off":
			if !s.offDone {
				s.offDone = true
				s.setProbe(func(p *nodeagent.ClusterProbeResponse) { *p = *s.afterOff })
			}
		}
	}
}

func (s *sim) start() {
	s.h.t.Helper()
	if _, err := s.h.eng.StartClusterStart(context.Background(), "test"); err != nil {
		s.h.t.Fatal(err)
	}
}

func (s *sim) startVip() {
	s.h.t.Helper()
	if _, err := s.h.eng.StartVipRestore(context.Background(), "test"); err != nil {
		s.h.t.Fatal(err)
	}
}

// drive plays the cluster until the run filed under key reaches want.
func (s *sim) drive(key string, want St) RunInfo {
	s.h.t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if r := s.h.info(key); r.st == want {
			return r
		}
		s.follow()
		s.h.tick()
		time.Sleep(3 * time.Millisecond)
	}
	s.h.t.Fatalf("wanted %s, run is %+v", want, s.h.info(key))
	return RunInfo{}
}

func (s *sim) vipCalls() int { return s.h.ops.count("vip-") }

// ---------------------------------------------------------------------------
// Cluster start: the nodes only.

func TestClusterStartRejoinsEveryNodeAndNeverTouchesThePlatform(t *testing.T) {
	t.Parallel()
	s := newSim(t) // the platform layer is deadlocked the whole time
	s.start()
	s.drive(clusterKey, Completed)
	un := s.h.ops.order("uncordon")
	if len(un) != 5 || strings.Join(un[:3], ",") != "m1,m2,m3" {
		t.Fatalf("masters rejoin one at a time in order, then the agents: %v", un)
	}
	if s.vipCalls() != 0 {
		t.Fatalf("the cluster start never repairs the platform layer: %v", s.h.ops.calls)
	}
}

func TestClusterStartSaysWhenThePlatformLayerIsNotHealthy(t *testing.T) {
	t.Parallel()
	s := newSim(t)
	s.start()
	r := s.drive(clusterKey, Completed)
	if !strings.Contains(r.Reason, "the platform layer is not healthy") || !strings.Contains(r.Reason, "Restore API VIP") {
		t.Fatalf("a note, not a failure, and it says what to run: %+v", r)
	}
}

func TestClusterStartHasNothingToSayAboutAPlatformThatIsFineOrAbsent(t *testing.T) {
	t.Parallel()
	cases := map[string]func(*nodeagent.ClusterProbeResponse){
		"healthy":       func(p *nodeagent.ClusterProbeResponse) { *p = recoveredProbe("m1"); p.CiliumReadyCount = 5 },
		"cilium absent": func(p *nodeagent.ClusterProbeResponse) { *p = nodeagent.ClusterProbeResponse{} },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			s := newSim(t)
			s.setProbe(mutate)
			s.start()
			r := s.drive(clusterKey, Completed)
			if strings.Contains(r.Reason, "platform") {
				t.Fatalf("%+v", r)
			}
		})
	}
}

func TestClusterStartDoesNotAskAboutAPlatformItDoesNotKnow(t *testing.T) {
	t.Parallel()
	s := newSim(t, func(c *config.Config) { c.Platform.VIP = "" })
	s.start()
	s.drive(clusterKey, Completed)
	if s.h.ops.probes != 0 {
		t.Fatal("no platform VIP configured: nothing to ask")
	}
}

func TestClusterStartDoesNotRefuseOnWhatTheNodesLookLike(t *testing.T) {
	t.Parallel()
	s := newSim(t)
	for _, n := range s.h.eng.cfg.Nodes {
		s.h.set(n.Name, func(o *observe.Observation) { *o = up() })
	}
	s.h.tick()
	s.start() // every node looks Ready and schedulable: it still runs
	s.drive(clusterKey, Completed)
}

func TestClusterStartNeedsAnEtcdMajority(t *testing.T) {
	t.Parallel()
	s := newSim(t)
	s.h.ops.majority = false
	s.start()
	s.drive(clusterKey, AwaitingQuorum)
	r := s.h.expire(clusterKey, time.Minute, Failed)
	if !strings.Contains(r.Reason, "majority") || s.h.ops.count("uncordon") != 0 {
		t.Fatalf("%+v %v", r, s.h.ops.calls)
	}
}

func TestClusterStartGoesOnWithAMajorityOfMasters(t *testing.T) {
	t.Parallel()
	s := newSim(t)
	s.h.down("m3")
	s.start()
	s.drive(clusterKey, AwaitingNodes)
	s.h.expire(clusterKey, time.Minute, AwaitingQuorum)
	r := s.drive(clusterKey, Failed) // m3 never rejoins, so the run ends Failed, after everything else
	if !strings.Contains(r.Reason, "m3") {
		t.Fatalf("the missing master must be named: %+v", r)
	}
	for _, n := range []string{"m1", "m2", "a1", "a2"} {
		if !s.h.ops.called("uncordon", n) {
			t.Errorf("%s was not brought back: %v", n, s.h.ops.calls)
		}
	}
}

func TestClusterStartFailsWithoutAMajorityOfMasters(t *testing.T) {
	t.Parallel()
	s := newSim(t)
	s.h.down("m2")
	s.h.down("m3")
	s.start()
	s.drive(clusterKey, AwaitingNodes)
	r := s.h.expire(clusterKey, time.Minute, Failed)
	if !strings.Contains(r.Reason, "1 of 3") {
		t.Fatalf("%+v", r)
	}
}

func TestClusterStartReportsHealthAndFailsOnAFaultyVolume(t *testing.T) {
	t.Parallel()
	s := newSim(t)
	s.setProbe(func(p *nodeagent.ClusterProbeResponse) { *p = nodeagent.ClusterProbeResponse{} })
	s.h.ops.lh = nodeagent.LonghornHealthResponse{Available: true, Total: 8, Degraded: 2}
	s.h.ops.vault = nodeagent.VaultSealedResponse{Found: 1, Sealed: []string{"vault-0"}}
	s.start()
	r := s.drive(clusterKey, Completed)
	if !strings.Contains(r.Reason, "2 of 8 longhorn volumes are degraded") || !strings.Contains(r.Reason, "vault is sealed (vault-0)") {
		t.Fatalf("degraded volumes and a sealed vault are notes on a completed run: %+v", r)
	}

	s = newSim(t)
	s.h.ops.lh = nodeagent.LonghornHealthResponse{Available: true, Total: 8, Faulty: 1}
	s.start()
	r = s.drive(clusterKey, Failed)
	if !strings.Contains(r.Reason, "1 of 8 longhorn volumes are faulty") {
		t.Fatalf("%+v", r)
	}
}

func TestClusterStartHealthReadsDoNotFailTheRun(t *testing.T) {
	t.Parallel()
	s := newSim(t)
	s.h.ops.lhErr = errBoom
	s.h.ops.vaultErr = errBoom
	s.h.ops.probeErr = errBoom
	s.start()
	r := s.drive(clusterKey, Completed)
	if !strings.Contains(r.Reason, "longhorn health could not be read") || !strings.Contains(r.Reason, "vault state could not be read") {
		t.Fatalf("%+v", r)
	}
	if strings.Contains(r.Reason, "platform") {
		t.Fatalf("a platform that could not be read is not a finding: %+v", r)
	}
}

func TestClusterStartAndShutdownExcludeEachOther(t *testing.T) {
	t.Parallel()
	s := newSim(t)
	s.start()
	if _, err := s.h.eng.StartCluster(context.Background(), "ha"); err != ErrClusterActive {
		t.Fatalf("shutdown during a start: %v", err)
	}
	if _, err := s.h.eng.StartClusterStart(context.Background(), "ui"); err != ErrClusterActive {
		t.Fatalf("second start: %v", err)
	}
	if _, err := s.h.eng.StartNode(context.Background(), WfRejoin, "a1", ModeGraceful, "ui"); err != ErrClusterRunning {
		t.Fatalf("a node run during a start: %v", err)
	}
	for _, wf := range []string{WfClusterStart, WfVipRestore} {
		if _, err := s.h.eng.StartNode(context.Background(), wf, "a1", ModeGraceful, "ui"); err == nil {
			t.Fatalf("%s is not a node workflow", wf)
		}
	}
}

// ---------------------------------------------------------------------------
// Platform: vip-restore, on its own.

func TestVipRestoreBreaksTheDeadlock(t *testing.T) {
	t.Parallel()
	s := newSim(t)
	s.startVip()
	r := s.drive(platformKey, Completed)
	if r.Final != "completed" {
		t.Fatalf("%+v", r)
	}
	h := s.h
	if got := h.ops.order("vip-on"); len(got) != 1 || got[0] != "m1" {
		t.Fatalf("the bootstrap address goes on exactly one master: %v", got)
	}
	if got := h.ops.order("vip-off"); len(got) != 1 || got[0] != "m1" {
		t.Fatalf("and comes off the same one: %v", got)
	}
	if h.ops.count("uncordon") != 0 || h.ops.count("start") != 0 {
		t.Fatalf("the platform repair does not touch the nodes: %v", h.ops.calls)
	}
}

func TestVipRestoreRunsEvenWhenEveryNodeLooksHealthy(t *testing.T) {
	t.Parallel()
	// The 2026-11-08 regression: every node looked up, Ready and schedulable
	// while Cilium and kube-vip were deadlocked and the VIP was dead.
	s := newSim(t)
	for _, n := range s.h.eng.cfg.Nodes {
		s.h.set(n.Name, func(o *observe.Observation) { *o = up() })
	}
	s.h.tick()
	s.startVip()
	s.drive(platformKey, Completed)
	if !s.h.ops.called("vip-on", "m1") || s.h.ops.probes == 0 {
		t.Fatalf("a dead VIP must be probed and fixed whatever the nodes say: %v", s.h.ops.calls)
	}
}

func TestVipRestoreLeavesTheAddressWhenKubeVipLeadsFromTheSameMaster(t *testing.T) {
	t.Parallel()
	s := newSim(t)
	up := recoveredProbe("m1.lan") // a fully qualified holder still names m1
	s.afterOn = &up
	s.startVip()
	s.drive(platformKey, Completed)
	if s.h.ops.called("vip-off", "m1") {
		t.Fatal("removing the address would take it from kube-vip, which leads from m1")
	}
}

func TestVipRestoreIgnoresAStaleLease(t *testing.T) {
	t.Parallel()
	s := newSim(t)
	stale := recoveredProbe("m3")
	stale.KubeVipRenewed = "t0" // same renewal as before: kube-vip has not run
	s.afterOn = &stale
	s.startVip()
	s.drive(platformKey, AwaitingVip)
	s.h.stays(platformKey, AwaitingVip)
	if s.h.ops.called("vip-off", "m1") {
		t.Fatal("a leftover lease is not kube-vip running")
	}
	r := s.h.expire(platformKey, time.Minute, Failed)
	if !strings.Contains(r.Reason, "stays on m1") {
		t.Fatalf("a failed recovery must say the address is still on: %+v", r)
	}
}

func TestVipRestorePutsTheAddressBackWhenTheVipDiesWithoutIt(t *testing.T) {
	t.Parallel()
	s := newSim(t)
	gone := deadProbe()
	gone.KubeVipRenewed = "t1"
	s.afterOff = &gone
	s.startVip()
	s.drive(platformKey, AwaitingVipStable)
	r := s.h.expire(platformKey, 20*time.Second, Failed)
	if got := s.h.ops.order("vip-on"); len(got) != 2 {
		t.Fatalf("the address must go back on: %v", s.h.ops.calls)
	}
	if !strings.Contains(r.Reason, "back on m1") {
		t.Fatalf("%+v", r)
	}
}

func TestVipRestoreTriesTheNextMasterWhenOneRefuses(t *testing.T) {
	t.Parallel()
	s := newSim(t)
	s.h.ops.failOn("vip-on m1")
	s.startVip()
	s.drive(platformKey, Completed)
	if got := s.h.ops.order("vip-on"); len(got) != 2 || got[1] != "m2" {
		t.Fatalf("m1 refused, m2 takes it: %v", got)
	}
}

func TestVipRestoreFailsWhenNoMasterTakesTheAddress(t *testing.T) {
	t.Parallel()
	s := newSim(t)
	for _, m := range []string{"m1", "m2", "m3"} {
		s.h.ops.failOn("vip-on " + m)
	}
	s.startVip()
	r := s.drive(platformKey, Failed)
	if !strings.Contains(r.Reason, "no master accepted") {
		t.Fatalf("%+v", r)
	}
}

func TestVipRestoreActsOnlyOnTheExactDeadlock(t *testing.T) {
	t.Parallel()
	cases := map[string]func(*nodeagent.ClusterProbeResponse){
		"the VIP answers":               func(p *nodeagent.ClusterProbeResponse) { p.VIPAnswers = true },
		"cilium does not use the VIP":   func(p *nodeagent.ClusterProbeResponse) { p.CiliumAPIHost = "127.0.0.1" },
		"cilium has no API host at all": func(p *nodeagent.ClusterProbeResponse) { p.CiliumAPIHost = "" },
		"cilium is not installed":       func(p *nodeagent.ClusterProbeResponse) { *p = nodeagent.ClusterProbeResponse{} },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			s := newSim(t)
			s.setProbe(mutate)
			s.startVip()
			r := s.drive(platformKey, Completed)
			if s.vipCalls() != 0 || !strings.Contains(r.Reason, "nothing to restore") {
				t.Fatalf("no deadlock, no bootstrap address: %v %+v", s.h.ops.calls, r)
			}
		})
	}
}

func TestVipRestoreWaitsWhileKubeVipIsStillComingUp(t *testing.T) {
	t.Parallel()
	s := newSim(t)
	s.setProbe(func(p *nodeagent.ClusterProbeResponse) { p.CiliumReadyCount = 2 }) // Cilium is up, only the VIP is not
	s.startVip()
	s.drive(platformKey, CheckingVip)
	s.h.stays(platformKey, CheckingVip)
	if s.vipCalls() != 0 {
		t.Fatal("Cilium is up: this is not the deadlock")
	}
	s.setProbe(func(p *nodeagent.ClusterProbeResponse) { p.VIPAnswers = true }) // kube-vip got there by itself
	s.drive(platformKey, Completed)
	if s.vipCalls() != 0 {
		t.Fatalf("%v", s.h.ops.calls)
	}
}

func TestVipRestoreGraceKeepsASlowBootFromBeingTreatedAsTheDeadlock(t *testing.T) {
	t.Parallel()
	s := newSim(t, func(c *config.Config) { c.Platform.DeadlockGrace = time.Hour })
	s.startVip()
	s.drive(platformKey, CheckingVip)
	s.h.stays(platformKey, CheckingVip)
	if s.vipCalls() != 0 {
		t.Fatal("the deadlock has not held for the grace period")
	}
	s.setProbe(func(p *nodeagent.ClusterProbeResponse) { p.VIPAnswers = true })
	s.drive(platformKey, Completed)
	if s.vipCalls() != 0 {
		t.Fatal("it resolved by itself")
	}
}

func TestVipRestoreDoesNotGuessWhenTheClusterCannotBeAsked(t *testing.T) {
	t.Parallel()
	s := newSim(t)
	s.h.ops.probeErr = errBoom
	s.startVip()
	s.drive(platformKey, CheckingVip)
	s.h.stays(platformKey, CheckingVip)
	if s.vipCalls() != 0 {
		t.Fatal("an unreadable cluster is not a deadlock")
	}
	r := s.h.expire(platformKey, time.Minute, Failed)
	if !strings.Contains(r.Reason, "no verdict") {
		t.Fatalf("%+v", r)
	}
}

func TestVipRestoreIsRefusedWithoutAConfiguredVip(t *testing.T) {
	t.Parallel()
	s := newSim(t, func(c *config.Config) { c.Platform.VIP = "" })
	s.startVip()
	r := s.drive(platformKey, Refused)
	if !strings.Contains(r.Reason, "platform.vip") || s.h.ops.probes != 0 {
		t.Fatalf("%+v", r)
	}
}

func TestVipRestoreLogsWhatItFoundOnce(t *testing.T) {
	t.Parallel()
	s := newSim(t, func(c *config.Config) { c.Platform.DeadlockGrace = time.Hour })
	s.startVip()
	s.drive(platformKey, CheckingVip)
	s.h.stays(platformKey, CheckingVip) // many rounds with the same finding
	count := 0
	for _, e := range s.h.log.List(events.Filter{}) {
		if strings.HasPrefix(e.Detail, "probe: VIP "+testVIP+" answers=false; cilium present=true ready=0/5") &&
			strings.Contains(e.Detail, "kube-vip present=true") {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("the probe finding must be logged exactly once while it does not change, got %d", count)
	}
}

func TestVipRestoreExcludesOtherRootWorkflows(t *testing.T) {
	t.Parallel()
	s := newSim(t, func(c *config.Config) { c.Platform.DeadlockGrace = time.Hour })
	s.startVip()
	if _, err := s.h.eng.StartVipRestore(context.Background(), "ui"); !errors.Is(err, ErrBusy) {
		t.Fatalf("second restore: %v", err)
	}
	if _, err := s.h.eng.StartClusterStart(context.Background(), "ui"); !errors.Is(err, ErrBusy) {
		t.Fatalf("a cluster start during a platform repair: %v", err)
	}
	if _, err := s.h.eng.StartNode(context.Background(), WfRejoin, "a1", ModeGraceful, "ui"); !errors.Is(err, ErrBusy) {
		t.Fatalf("a node run during a platform repair: %v", err)
	}
}

func TestPlatformState(t *testing.T) {
	t.Parallel()
	mk := func(f func(*nodeagent.ClusterProbeResponse)) nodeagent.ClusterProbeResponse {
		p := nodeagent.ClusterProbeResponse{CiliumPresent: true, CiliumAPIHost: testVIP, CiliumDesired: 5, CiliumReadyCount: 5, VIPAnswers: true}
		f(&p)
		return p
	}
	pk := func(name string, present bool, ready, desired int) []nodeagent.PackagedComponent {
		return []nodeagent.PackagedComponent{{Name: name, Present: present, Ready: ready, Desired: desired}}
	}
	cases := []struct {
		name string
		vip  string
		p    nodeagent.ClusterProbeResponse
		want string
	}{
		{"all well", testVIP, mk(func(*nodeagent.ClusterProbeResponse) {}), PlatformOK},
		{"cilium not installed", testVIP, nodeagent.ClusterProbeResponse{}, PlatformAbsent},
		{"some agents not ready", testVIP, mk(func(p *nodeagent.ClusterProbeResponse) { p.CiliumReadyCount = 3 }), PlatformPartial},
		{"cilium up, vip dead", testVIP, mk(func(p *nodeagent.ClusterProbeResponse) { p.VIPAnswers = false }), PlatformVipDown},
		{"none ready", testVIP, mk(func(p *nodeagent.ClusterProbeResponse) { p.CiliumReadyCount = 0; p.CiliumAPIHost = "127.0.0.1" }), PlatformDown},
		{"the deadlock", testVIP, mk(func(p *nodeagent.ClusterProbeResponse) { p.CiliumReadyCount = 0; p.VIPAnswers = false }), PlatformDeadlock},
		{"coredns not ready", testVIP, mk(func(p *nodeagent.ClusterProbeResponse) { p.Packaged = pk("CoreDNS", true, 1, 2) }), PlatformPartial},
		{"coredns down", testVIP, mk(func(p *nodeagent.ClusterProbeResponse) { p.Packaged = pk("CoreDNS", true, 0, 2) }), PlatformPartial},
		{"coredns ready", testVIP, mk(func(p *nodeagent.ClusterProbeResponse) { p.Packaged = pk("CoreDNS", true, 2, 2) }), PlatformOK},
		{"traefik disabled", testVIP, mk(func(p *nodeagent.ClusterProbeResponse) { p.Packaged = pk("Traefik", false, 0, 0) }), PlatformOK},
		{"an add-on scaled to zero", testVIP, mk(func(p *nodeagent.ClusterProbeResponse) { p.Packaged = pk("metrics-server", true, 0, 0) }), PlatformOK},
		{"no vip known, all ready", "", mk(func(p *nodeagent.ClusterProbeResponse) { p.VIPAnswers = false }), PlatformOK},
	}
	for _, c := range cases {
		if got, detail := PlatformState(c.vip, c.p); got != c.want || detail == "" {
			t.Errorf("%s: got %s (%q), want %s", c.name, got, detail, c.want)
		}
	}
}

func TestVerdictOf(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		vip     string
		present bool
		host    string
		ready   int
		answers bool
		want    verdict
	}{
		{"the deadlock", testVIP, true, testVIP, 0, false, deadlocked},
		{"no vip configured", "", true, testVIP, 0, false, notNeeded},
		{"vip answers", testVIP, true, testVIP, 0, true, notNeeded},
		{"cilium via localhost", testVIP, true, "127.0.0.1", 0, false, notNeeded},
		{"cilium up, vip dead", testVIP, true, testVIP, 1, false, waiting},
		{"cilium not installed", testVIP, false, "", 0, false, notNeeded},
	}
	for _, c := range cases {
		if got, why := verdictOf(c.vip, c.present, c.host, c.ready, c.answers); got != c.want || why == "" {
			t.Errorf("%s: got %v (%q), want %v", c.name, got, why, c.want)
		}
	}
}
