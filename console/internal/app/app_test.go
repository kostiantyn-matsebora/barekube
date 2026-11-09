package app

import (
	"context"
	"errors"
	"github.com/kostiantyn-matsebora/barekube/console/internal/events"
	"testing"
	"time"

	"github.com/kostiantyn-matsebora/barekube/console/internal/config"
	"github.com/kostiantyn-matsebora/barekube/console/internal/flows"
	"github.com/kostiantyn-matsebora/barekube/console/internal/nodeagent"
	"github.com/kostiantyn-matsebora/barekube/console/internal/observe"
)

func fixture() (*config.Config, map[string]nodeagent.Client, map[string]*fakeAgent) {
	cfg := &config.Config{Nodes: []config.Node{
		{Name: "m1", Role: config.RoleMaster}, {Name: "m2", Role: config.RoleMaster},
		{Name: "a1", Role: config.RoleAgent, Operations: []config.Operation{config.OpReboot}},
	}}
	cfg.Timing = config.DefaultTiming()
	cfg.ClusterShutdown.Agents = config.Policy{Action: "standby", Mode: "force"}
	cfg.ClusterShutdown.Masters = config.Policy{Action: "poweroff", Mode: "graceful"}
	fakes := map[string]*fakeAgent{"m1": newAgent("m1"), "m2": newAgent("m2"), "a1": newAgent("a1")}
	agents := map[string]nodeagent.Client{}
	for k, v := range fakes {
		agents[k] = v
	}
	return cfg, agents, fakes
}

func TestOpsRouteToTheRightAgent(t *testing.T) {
	cfg, agents, f := fixture()
	o := NewOps(cfg, agents)
	ctx := context.Background()
	for name, err := range map[string]error{
		"reboot": o.Reboot(ctx, "a1"), "stop": o.StopK3s(ctx, "a1"), "kill": o.KillK3s(ctx, "a1"),
		"start": o.StartK3s(ctx, "a1"), "poweroff": o.PowerOff(ctx, "a1", false), "poweroff-force": o.PowerOff(ctx, "a1", true),
	} {
		if err != nil || !f["a1"].did(name) {
			t.Errorf("%s: %v", name, err)
		}
	}
	// Kubernetes calls go to a master, never the node itself.
	if err := o.Cordon(ctx, "a1"); err != nil || !f["m1"].did("cordon") || f["a1"].did("cordon") {
		t.Fatal("cordon routing")
	}
	_ = o.Uncordon(ctx, "a1")
	_ = o.Drain(ctx, "a1", time.Second)
	_ = o.LonghornScheduling(ctx, "a1", false)
	if v, err := o.ZeroReplicaRisk(ctx, "a1"); v != nil || err != nil {
		t.Fatal(v, err)
	}
	for _, op := range []string{"uncordon", "drain", "lh-off", "risk"} {
		if !f["m1"].did(op) {
			t.Errorf("master missed %s", op)
		}
	}
}

func TestOpsUnknownNodeAndNoLonghorn(t *testing.T) {
	cfg, agents, f := fixture()
	o := NewOps(cfg, agents)
	ctx := context.Background()
	for _, err := range []error{o.Reboot(ctx, "x"), o.StopK3s(ctx, "x"), o.KillK3s(ctx, "x"), o.StartK3s(ctx, "x"), o.PowerOff(ctx, "x", false)} {
		if err == nil {
			t.Fatal("unknown node accepted")
		}
	}
	f["m1"].lhAvail = false
	if err := o.LonghornScheduling(ctx, "a1", false); err != nil || f["m1"].did("lh-off") {
		t.Fatal("longhorn calls must be skipped when it is not installed")
	}
	f["m1"].risk = []string{"v"}
	if v, _ := o.ZeroReplicaRisk(ctx, "a1"); v != nil || f["m1"].did("risk") {
		t.Fatal("risk check must be skipped without longhorn")
	}
	f["m1"].lhAvail = true
	if v, _ := o.ZeroReplicaRisk(ctx, "a1"); len(v) != 1 {
		t.Fatal("risk lost")
	}
}

func TestQuorumNeedsEveryMaster(t *testing.T) {
	cfg, agents, f := fixture()
	o := NewOps(cfg, agents)
	if !o.QuorumOK(context.Background()) {
		t.Fatal("all ready")
	}
	f["m2"].etcdOK = false
	if o.QuorumOK(context.Background()) {
		t.Fatal("one master down must fail the gate")
	}
	if (&Ops{}).QuorumOK(context.Background()) {
		t.Fatal("no masters")
	}
	o.agents = map[string]nodeagent.Client{}
	if o.QuorumOK(context.Background()) {
		t.Fatal("missing agent")
	}
}

func TestEtcdSnapshotGoesToAMaster(t *testing.T) {
	cfg, agents, f := fixture()
	o := NewOps(cfg, agents)
	if err := o.EtcdSnapshot(context.Background()); err != nil || !f["m1"].did("snapshot") || f["a1"].did("snapshot") {
		t.Fatal(err)
	}
	f["m1"].err["snapshot"] = errX
	if err := o.EtcdSnapshot(context.Background()); err == nil {
		t.Fatal("failure lost")
	}
}

func TestEtcdLeader(t *testing.T) {
	cfg, agents, f := fixture()
	o := NewOps(cfg, agents)
	if _, err := o.EtcdLeader(context.Background()); err == nil {
		t.Fatal("no leader reported")
	}
	f["m1"].err["leader"] = errX
	f["m2"].leader = true
	if l, err := o.EtcdLeader(context.Background()); l != "m2" || err != nil {
		t.Fatal(l, err)
	}
	o.agents = map[string]nodeagent.Client{}
	if _, err := o.EtcdLeader(context.Background()); err == nil {
		t.Fatal("missing agents")
	}
}

func TestProberReportsFactsAndNeverGuesses(t *testing.T) {
	cfg, agents, f := fixture()
	p := NewProber(cfg, agents)
	ctx := context.Background()

	o := p.Probe(ctx, "a1")
	if o.Reachable != observe.Yes || o.K3sActive != observe.Yes || o.Ready != observe.Yes || o.Cordoned != observe.No {
		t.Fatalf("%+v", o)
	}
	f["a1"].k3s, f["m1"].cordon = false, true
	o = p.Probe(ctx, "a1")
	if o.K3sActive != observe.No || o.Cordoned != observe.Yes {
		t.Fatalf("%+v", o)
	}
	f["m1"].err["ready"], f["a1"].err["k3s"] = errX, errX
	o = p.Probe(ctx, "a1")
	if o.Ready != observe.Unknown || o.K3sActive != observe.Unknown {
		t.Fatalf("an error is unknown, not no: %+v", o)
	}
	f["a1"].reachable = false
	o = p.Probe(ctx, "a1")
	if o.Reachable != observe.No || o.K3sActive != observe.Unknown || o.Ready != observe.Unknown {
		t.Fatalf("an unreachable node reports only that: %+v", o)
	}
	if o := p.Probe(ctx, "ghost"); o.Reachable != observe.Unknown {
		t.Fatalf("%+v", o)
	}
}

func TestServiceNodesHeartbeatsAndRuns(t *testing.T) {
	cfg, agents, f := fixture()
	svc, err := NewService(cfg, agents, nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	svc.Observer.Tick(ctx)

	nodes := svc.Nodes()
	if len(nodes) != 3 || nodes[0].State != "healthy" || nodes[2].Operations[0] != "reboot" || len(nodes[2].Operations) != 1 {
		t.Fatalf("%+v", nodes)
	}
	if nodes[0].Heartbeat != nil || nodes[0].Run != nil {
		t.Fatal("nothing yet")
	}
	if err := svc.Heartbeat("ghost", true); err != ErrUnknownNode {
		t.Fatal(err)
	}
	if err := svc.Heartbeat("m1", false); err != nil {
		t.Fatal(err)
	}
	if n := svc.Nodes()[0]; n.State != "stood-by" || n.Heartbeat == nil {
		t.Fatalf("%+v", n)
	}

	svc.Start(ctx)
	info, err := svc.StartNode(ctx, flows.WfReboot, "a1", flows.ModeGraceful, "test")
	if err != nil || info.RunID == "" {
		t.Fatal(info, err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for !f["a1"].did("reboot") && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if !f["a1"].did("reboot") {
		t.Fatal("workflow never rebooted the node")
	}
	if len(svc.Runs()) != 1 || len(svc.Workflows()) != 7 || len(svc.Events(allEvents)) == 0 {
		t.Fatal("views empty")
	}
	if n := svc.Nodes()[2]; n.Run == nil || n.Run.Workflow != flows.WfReboot {
		t.Fatalf("%+v", n)
	}
	if _, err := svc.StartCluster(ctx, "test"); !errors.Is(err, flows.ErrBusy) {
		t.Fatalf("a second root workflow must be refused: %v", err)
	}
}

var allEvents = events.Filter{}

// A cold start needs a majority of masters, not all of them: with two masters
// that is both, with three it is two.
func TestQuorumMajority(t *testing.T) {
	cfg, agents, f := fixture()
	o := NewOps(cfg, agents)
	ctx := context.Background()
	if !o.QuorumMajority(ctx) {
		t.Fatal("both masters ready")
	}
	f["m2"].etcdOK = false
	if o.QuorumMajority(ctx) {
		t.Fatal("one of two masters is not a majority")
	}
	cfg.Nodes = append(cfg.Nodes, config.Node{Name: "m3", Role: config.RoleMaster})
	f["m3"] = newAgent("m3")
	agents["m3"] = f["m3"]
	o = NewOps(cfg, agents)
	if !o.QuorumMajority(ctx) {
		t.Fatal("two of three masters is a majority")
	}
	f["m3"].etcdOK = false
	if o.QuorumMajority(ctx) {
		t.Fatal("one of three is not")
	}
}

// The bootstrap address goes to the named master's own agent, never through
// the pool; the cluster reads go through whichever master answers.
func TestClusterStartCallsRouteToTheRightAgent(t *testing.T) {
	cfg, agents, f := fixture()
	o := NewOps(cfg, agents)
	ctx := context.Background()
	if err := o.VipBootstrap(ctx, "m2", true); err != nil || !f["m2"].did("vip-on") || f["m1"].did("vip-on") {
		t.Fatalf("vip-on routing: %v", err)
	}
	if err := o.VipBootstrap(ctx, "m2", false); err != nil || !f["m2"].did("vip-off") {
		t.Fatalf("vip-off routing: %v", err)
	}
	if err := o.VipBootstrap(ctx, "ghost", true); err == nil {
		t.Fatal("unknown node")
	}
	f["m1"].probe = nodeagent.ClusterProbeResponse{CiliumPresent: true, KubeVipLeader: "m2"}
	if p, err := o.ClusterProbe(ctx, "192.168.1.100"); err != nil || p.KubeVipLeader != "m2" {
		t.Fatalf("%+v %v", p, err)
	}
	f["m1"].lh = nodeagent.LonghornHealthResponse{Available: true, Total: 3, Degraded: 1}
	if h, err := o.LonghornHealth(ctx); err != nil || h.Degraded != 1 {
		t.Fatalf("%+v %v", h, err)
	}
	f["m1"].vault = nodeagent.VaultSealedResponse{Found: 1, Sealed: []string{"vault-0"}}
	if v, err := o.VaultSealed(ctx, "vault"); err != nil || len(v.Sealed) != 1 {
		t.Fatalf("%+v %v", v, err)
	}
}

func TestBuildPlatform(t *testing.T) {
	const vip = "192.168.1.100"
	// Cilium not installed: nothing to show.
	if v := buildPlatform(vip, nodeagent.ClusterProbeResponse{}); v.Shown || len(v.Components) != 0 {
		t.Fatalf("%+v", v)
	}
	// The deadlock: both components down, and the card says so.
	v := buildPlatform(vip, nodeagent.ClusterProbeResponse{CiliumPresent: true, CiliumDesired: 5, CiliumAPIHost: vip, KubeVipPresent: true, KubeVipLeader: "master-1"})
	if !v.Shown || v.State != flows.PlatformDeadlock || len(v.Components) != 2 ||
		v.Components[0].State != "down" || v.Components[1].State != "down" ||
		v.Components[1].Detail != "API VIP 192.168.1.100 does not answer; lease held by master-1" {
		t.Fatalf("%+v", v)
	}
	// Healthy.
	v = buildPlatform(vip, nodeagent.ClusterProbeResponse{CiliumPresent: true, CiliumDesired: 5, CiliumReadyCount: 5, VIPAnswers: true, KubeVipPresent: true})
	if v.State != flows.PlatformOK || v.Components[0].State != "ok" || v.Components[1].State != "ok" {
		t.Fatalf("%+v", v)
	}
	// Some agents not ready: partial, not down.
	v = buildPlatform(vip, nodeagent.ClusterProbeResponse{CiliumPresent: true, CiliumDesired: 5, CiliumReadyCount: 3, VIPAnswers: true, KubeVipPresent: true})
	if v.State != flows.PlatformPartial || v.Components[0].State != "partial" {
		t.Fatalf("%+v", v)
	}
	// The optional k3s add-ons: an installed one that lags makes the layer
	// partial, a missing or scaled-to-zero one is shown as off and counts for nothing.
	v = buildPlatform(vip, nodeagent.ClusterProbeResponse{CiliumPresent: true, CiliumDesired: 5, CiliumReadyCount: 5, VIPAnswers: true,
		KubeVipPresent: true, Packaged: []nodeagent.PackagedComponent{
			{Name: "CoreDNS", Present: true, Desired: 2},
			{Name: "Traefik"},
			{Name: "metrics-server", Present: true, Ready: 0, Desired: 0},
			{Name: "servicelb", Present: true, Ready: 2, Desired: 3}}})
	got := map[string]PlatformComponent{}
	for _, c := range v.Components {
		got[c.Name] = c
	}
	if v.State != flows.PlatformPartial || len(v.Components) != 6 ||
		got["CoreDNS"].State != "down" || got["CoreDNS"].Detail != "0 of 2 ready" ||
		got["Traefik"].State != "off" || got["Traefik"].Detail != "not installed" ||
		got["metrics-server"].State != "off" || got["servicelb"].State != "partial" {
		t.Fatalf("%+v", v)
	}
	// Only absent or off add-ons: healthy.
	v = buildPlatform(vip, nodeagent.ClusterProbeResponse{CiliumPresent: true, CiliumDesired: 5, CiliumReadyCount: 5, VIPAnswers: true,
		KubeVipPresent: true, Packaged: []nodeagent.PackagedComponent{{Name: "Traefik"}, {Name: "servicelb"}}})
	if v.State != flows.PlatformOK || len(v.Components) != 4 || v.Components[2].State != "off" {
		t.Fatalf("a disabled add-on must not count against the platform: %+v", v)
	}
	// Cilium without a configured VIP: no kube-vip row unless it is installed.
	v = buildPlatform("", nodeagent.ClusterProbeResponse{CiliumPresent: true, CiliumDesired: 2, CiliumReadyCount: 2})
	if v.State != flows.PlatformOK || len(v.Components) != 1 {
		t.Fatalf("%+v", v)
	}
}

func TestServicePlatformIsCachedAndUnknownWhenUnreadable(t *testing.T) {
	cfg, agents, f := fixture()
	cfg.Platform.VIP = "192.168.1.100"
	f["m1"].probe = nodeagent.ClusterProbeResponse{CiliumPresent: true, CiliumDesired: 3, CiliumReadyCount: 3, VIPAnswers: true, KubeVipPresent: true}
	svc, err := NewService(cfg, agents, nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	v := svc.Platform(ctx)
	if !v.Shown || v.State != flows.PlatformOK || !v.CanRestore || v.CheckedAt == nil {
		t.Fatalf("%+v", v)
	}
	// Within the cache window the answer does not change, whatever the cluster does.
	f["m1"].mu.Lock()
	f["m1"].probe = nodeagent.ClusterProbeResponse{}
	f["m1"].mu.Unlock()
	if v2 := svc.Platform(ctx); v2.State != flows.PlatformOK {
		t.Fatalf("a probe per poll would hammer the cluster: %+v", v2)
	}

	// A cluster that cannot be read is unknown and still shown: unreadable is not absent.
	cfg2, agents2, f2 := fixture()
	cfg2.Platform.VIP = "192.168.1.100"
	for _, a := range f2 {
		a.err["probe"] = errX
	}
	svc2, _ := NewService(cfg2, agents2, nil)
	if v := svc2.Platform(ctx); !v.Shown || v.State != "unknown" {
		t.Fatalf("%+v", v)
	}
	// And with no VIP configured the restore cannot be offered.
	cfg3, agents3, _ := fixture()
	svc3, _ := NewService(cfg3, agents3, nil)
	if v := svc3.Platform(ctx); v.CanRestore {
		t.Fatalf("%+v", v)
	}
}
