package flows

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	clocktesting "k8s.io/utils/clock/testing"

	"github.com/kostiantyn-matsebora/barekube/console/internal/config"
	"github.com/kostiantyn-matsebora/barekube/console/internal/events"
	"github.com/kostiantyn-matsebora/barekube/console/internal/nodeagent"
	"github.com/kostiantyn-matsebora/barekube/console/internal/observe"
	"github.com/kostiantyn-matsebora/barekube/console/internal/store"
)

// fakeOps is the whole cluster as far as the workflows can tell: it records
// every call and fails the ones a test names.
type fakeOps struct {
	mu          sync.Mutex
	calls       []string
	fail        map[string]error // key: "cordon" or "cordon m1"
	quorum      bool
	risk        []string
	riskErr     error
	leader      string
	leaderErr   error
	snapshotErr error

	// cluster start
	majority  bool
	probe     nodeagent.ClusterProbeResponse
	probeErr  error
	lh        nodeagent.LonghornHealthResponse
	lhErr     error
	vault     nodeagent.VaultSealedResponse
	vaultErr  error
	probeFunc func(call int) nodeagent.ClusterProbeResponse // overrides probe when set
	probes    int
}

func newFakeOps() *fakeOps {
	return &fakeOps{fail: map[string]error{}, quorum: true, majority: true, leader: "m2"}
}

func (f *fakeOps) call(op, node string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, op+" "+node)
	if err, ok := f.fail[op+" "+node]; ok {
		return err
	}
	return f.fail[op]
}

func (f *fakeOps) failOn(key string) {
	f.mu.Lock()
	f.fail[key] = errors.New("boom " + key)
	f.mu.Unlock()
}

func (f *fakeOps) called(op, node string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, c := range f.calls {
		if c == op+" "+node {
			return true
		}
	}
	return false
}

func (f *fakeOps) count(op string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, c := range f.calls {
		if len(c) >= len(op) && c[:len(op)] == op {
			n++
		}
	}
	return n
}

func (f *fakeOps) order(op string) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, c := range f.calls {
		var o, n string
		fmt.Sscanf(c, "%s %s", &o, &n)
		if o == op {
			out = append(out, n)
		}
	}
	return out
}

func (f *fakeOps) QuorumOK(context.Context) bool              { f.mu.Lock(); defer f.mu.Unlock(); return f.quorum }
func (f *fakeOps) Cordon(_ context.Context, n string) error   { return f.call("cordon", n) }
func (f *fakeOps) Uncordon(_ context.Context, n string) error { return f.call("uncordon", n) }
func (f *fakeOps) LonghornScheduling(_ context.Context, n string, allow bool) error {
	return f.call(map[bool]string{true: "lh-on", false: "lh-off"}[allow], n)
}
func (f *fakeOps) ZeroReplicaRisk(_ context.Context, n string) ([]string, error) {
	f.call("risk", n)
	return f.risk, f.riskErr
}
func (f *fakeOps) Drain(_ context.Context, n string, _ time.Duration) error {
	return f.call("drain", n)
}
func (f *fakeOps) Reboot(_ context.Context, n string) error   { return f.call("reboot", n) }
func (f *fakeOps) StopK3s(_ context.Context, n string) error  { return f.call("stop", n) }
func (f *fakeOps) KillK3s(_ context.Context, n string) error  { return f.call("kill", n) }
func (f *fakeOps) StartK3s(_ context.Context, n string) error { return f.call("start", n) }
func (f *fakeOps) PowerOff(_ context.Context, n string, force bool) error {
	return f.call(map[bool]string{true: "poweroff-force", false: "poweroff"}[force], n)
}
func (f *fakeOps) EtcdSnapshot(context.Context) error {
	f.call("snapshot", "")
	return f.snapshotErr
}
func (f *fakeOps) EtcdLeader(context.Context) (string, error) { return f.leader, f.leaderErr }

type fakeObs struct {
	mu     sync.Mutex
	m      map[string]observe.Observation
	hidden map[string]bool
}

func (f *fakeObs) Latest(n string) (observe.Observation, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.hidden[n] {
		return observe.Observation{}, false
	}
	o, ok := f.m[n]
	return o, ok
}

type harness struct {
	t     *testing.T
	eng   *Engine
	ops   *fakeOps
	obs   *fakeObs
	log   *events.Log
	clk   *clocktesting.FakeClock
	facts map[string]observe.Observation
}

func up() observe.Observation {
	return observe.Observation{Reachable: observe.Yes, K3sActive: observe.Yes, Ready: observe.Yes, Cordoned: observe.No}
}

func testConfig() *config.Config {
	c := &config.Config{Nodes: []config.Node{
		{Name: "m1", Role: config.RoleMaster},
		{Name: "m2", Role: config.RoleMaster},
		{Name: "m3", Role: config.RoleMaster},
		{Name: "a1", Role: config.RoleAgent, Operations: []config.Operation{config.OpReboot, config.OpStandBy, config.OpRejoin}},
		{Name: "a2", Role: config.RoleAgent},
	}}
	c.Timing = config.DefaultTiming()
	// Old tests hold a state across two ticks. Each tick moves the clock 3ms.
	c.Timing.Settle = 5 * time.Millisecond
	c.ClusterShutdown.Agents = config.Policy{Action: "standby", Mode: "force"}
	c.ClusterShutdown.Masters = config.Policy{Action: "poweroff", Mode: "graceful", ForceAfter: 90 * time.Second, LeaderLast: true}
	c.ClusterStart.Waits = config.StartWaits{Nodes: 10 * time.Minute, Quorum: 5 * time.Minute, Rejoin: 15 * time.Minute}
	c.Platform.Waits = config.PlatformWaits{Probe: 10 * time.Minute, Vip: 5 * time.Minute, Stable: time.Minute}
	c.Platform.DeadlockGrace = 5 * time.Millisecond
	c.ClusterShutdown.Waits = config.ShutdownWaits{Agents: 3 * time.Minute, MastersDrained: 5 * time.Minute, MasterDown: 4 * time.Minute}
	return c
}

func newHarness(t *testing.T, mutate ...func(*config.Config)) *harness {
	t.Helper()
	h, _ := newHarnessOn(t, nil, mutate...)
	return h
}

// newHarnessOn builds the engine over a store, or in memory when db is nil.
// The returned func stops it, as a dying process would.
func newHarnessOn(t *testing.T, db *store.DB, mutate ...func(*config.Config)) (*harness, func()) {
	t.Helper()
	cfg := testConfig()
	for _, m := range mutate {
		m(cfg)
	}
	clk := clocktesting.NewFakeClock(time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC))
	h := &harness{t: t, ops: newFakeOps(), obs: &fakeObs{m: map[string]observe.Observation{}, hidden: map[string]bool{}}, log: events.NewLog(5000, clk), clk: clk, facts: map[string]observe.Observation{}}
	for _, n := range cfg.Nodes {
		h.facts[n.Name] = up()
	}
	eng, err := New(cfg, h.ops, h.obs, h.log, Options{Clock: clk, Poll: 2 * time.Millisecond, Store: db})
	if err != nil {
		t.Fatal(err)
	}
	h.eng = eng
	ctx, cancel := context.WithCancel(context.Background())
	eng.Run(ctx)
	stop := func() { cancel(); eng.Stop() }
	t.Cleanup(stop)
	h.tick()
	return h, stop
}

// set changes what the next observation of a node reports.
func (h *harness) set(node string, f func(*observe.Observation)) {
	o := h.facts[node]
	f(&o)
	h.facts[node] = o
}

func (h *harness) down(node string) {
	h.set(node, func(o *observe.Observation) {
		*o = observe.Observation{Reachable: observe.No, K3sActive: observe.Unknown, Ready: observe.Yes, Cordoned: observe.Yes}
	})
}

// tick delivers one observation round.
func (h *harness) tick() {
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
	h.clk.Step(3 * time.Millisecond)
}

func (h *harness) info(key string) RunInfo {
	r, _ := h.eng.reg.latestRun(key)
	return r
}

// waitFor ticks until the run reaches the status.
func (h *harness) waitFor(key string, want St) RunInfo {
	h.t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if r := h.info(key); r.st == want {
			return r
		}
		h.tick()
		time.Sleep(2 * time.Millisecond)
	}
	h.t.Fatalf("%s: wanted %s, run is %+v", key, want, h.info(key))
	return RunInfo{}
}

// stays ticks a few rounds and asserts the run did not leave the status.
func (h *harness) stays(key string, st St) {
	h.t.Helper()
	for range 8 {
		h.tick()
		time.Sleep(2 * time.Millisecond)
	}
	if got := h.info(key).st; got != st {
		h.t.Fatalf("%s left %s for %s", key, st, got)
	}
}

// expire advances the fake clock until the wait times out into want.
func (h *harness) expire(key string, d time.Duration, want St) RunInfo {
	h.t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if r := h.info(key); r.st == want {
			return r
		}
		h.clk.Step(d)
		time.Sleep(5 * time.Millisecond)
	}
	h.t.Fatalf("%s: wanted timeout into %s, run is %+v", key, want, h.info(key))
	return RunInfo{}
}

func (h *harness) start(wf, node string, mode Mode) RunInfo {
	h.t.Helper()
	info, err := h.eng.StartNode(context.Background(), wf, node, mode, "test")
	if err != nil {
		h.t.Fatalf("start %s %s: %v", wf, node, err)
	}
	return info
}

func (h *harness) reason(key string) string { return h.info(key).Reason }

func (h *harness) kinds(node string) []string {
	var out []string
	for _, e := range h.log.List(events.Filter{Node: node}) {
		out = append(out, e.Kind)
	}
	return out
}

func (h *harness) hasEvent(kind, node string) bool {
	for _, e := range h.log.List(events.Filter{Node: node}) {
		if e.Kind == kind {
			return true
		}
	}
	return false
}

func (f *fakeOps) QuorumMajority(context.Context) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.majority
}
func (f *fakeOps) ClusterProbe(context.Context, string) (nodeagent.ClusterProbeResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.probes++
	if f.probeFunc != nil {
		return f.probeFunc(f.probes), f.probeErr
	}
	return f.probe, f.probeErr
}
func (f *fakeOps) VipBootstrap(_ context.Context, n string, on bool) error {
	return f.call(map[bool]string{true: "vip-on", false: "vip-off"}[on], n)
}
func (f *fakeOps) LonghornHealth(context.Context) (nodeagent.LonghornHealthResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.lh, f.lhErr
}
func (f *fakeOps) VaultSealed(context.Context, string) (nodeagent.VaultSealedResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.vault, f.vaultErr
}
