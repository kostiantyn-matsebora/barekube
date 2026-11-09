package app

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/kostiantyn-matsebora/barekube/console/internal/nodeagent"
)

var errX = errors.New("x")

// fakeAgent is a scriptable node-agent.
type fakeAgent struct {
	mu                            sync.Mutex
	name                          string
	reachable, k3s, ready, cordon bool
	etcdOK, leader                bool
	lhAvail                       bool
	risk                          []string
	err                           map[string]error
	calls                         []string
	probe                         nodeagent.ClusterProbeResponse
	lh                            nodeagent.LonghornHealthResponse
	vault                         nodeagent.VaultSealedResponse
}

func newAgent(name string) *fakeAgent {
	return &fakeAgent{name: name, reachable: true, k3s: true, ready: true, etcdOK: true, lhAvail: true, err: map[string]error{}}
}

func (f *fakeAgent) rec(op string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, op)
	return f.err[op]
}

func (f *fakeAgent) did(op string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, c := range f.calls {
		if c == op {
			return true
		}
	}
	return false
}

func (f *fakeAgent) Reboot(context.Context) error { return f.rec("reboot") }
func (f *fakeAgent) Reachable(context.Context) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.reachable
}
func (f *fakeAgent) StopK3s(context.Context) error  { return f.rec("stop") }
func (f *fakeAgent) KillK3s(context.Context) error  { return f.rec("kill") }
func (f *fakeAgent) StartK3s(context.Context) error { return f.rec("start") }
func (f *fakeAgent) K3sActive(context.Context) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.k3s, f.err["k3s"]
}
func (f *fakeAgent) PowerOff(_ context.Context, force bool) error {
	if force {
		return f.rec("poweroff-force")
	}
	return f.rec("poweroff")
}
func (f *fakeAgent) Cordon(context.Context, string) error   { return f.rec("cordon") }
func (f *fakeAgent) Uncordon(context.Context, string) error { return f.rec("uncordon") }
func (f *fakeAgent) Cordoned(context.Context, string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.cordon, f.err["cordoned"]
}
func (f *fakeAgent) Drain(context.Context, string, bool, bool, time.Duration) error {
	return f.rec("drain")
}
func (f *fakeAgent) NodeReady(context.Context, string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.ready, f.err["ready"]
}
func (f *fakeAgent) SetLonghornScheduling(_ context.Context, _ string, allow bool) error {
	if allow {
		return f.rec("lh-on")
	}
	return f.rec("lh-off")
}
func (f *fakeAgent) ZeroReplicaRisk(context.Context, string) ([]string, error) {
	f.rec("risk")
	return f.risk, f.err["risk"]
}
func (f *fakeAgent) LonghornAvailable(context.Context) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.lhAvail, f.err["lhavail"]
}
func (f *fakeAgent) EtcdReadyz(context.Context) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.etcdOK
}
func (f *fakeAgent) EtcdIsLeader(context.Context) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.leader, f.err["leader"]
}
func (f *fakeAgent) EtcdSnapshot(context.Context) error { return f.rec("snapshot") }

func (f *fakeAgent) VipBootstrap(_ context.Context, on bool) error {
	if on {
		return f.rec("vip-on")
	}
	return f.rec("vip-off")
}
func (f *fakeAgent) ClusterProbe(context.Context, string) (nodeagent.ClusterProbeResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.probe, f.err["probe"]
}
func (f *fakeAgent) LonghornHealth(context.Context) (nodeagent.LonghornHealthResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.lh, f.err["lh"]
}
func (f *fakeAgent) VaultSealed(context.Context, string) (nodeagent.VaultSealedResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.vault, f.err["vault"]
}
