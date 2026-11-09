package nodeagent

import (
	"context"
	"log"
	"sync"
	"time"
)

// Mock stands in for a node's real agent during pure unit-speed local
// testing — no process, no network, no node. Tracks just enough state
// (k3s active, cordoned) for Phase 2's stand-by/rejoin/power-off flows to
// be exercisable against config.local.yaml, not just Phase 1's reboot.
type Mock struct {
	NodeName string

	mu        sync.Mutex
	k3sActive bool // zero value false; New sets it true via Reset below
	cordoned  map[string]bool
	init      bool
}

func (m *Mock) ensureInit() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.init {
		m.k3sActive = true
		m.init = true
	}
}

func (m *Mock) setCordoned(node string, v bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.cordoned == nil {
		m.cordoned = map[string]bool{}
	}
	m.cordoned[node] = v
}

func (m *Mock) Reboot(ctx context.Context) error {
	log.Printf("[nodeagent mock] %s: reboot requested (no-op)", m.NodeName)
	return nil
}
func (m *Mock) Reachable(ctx context.Context) bool { return true }

func (m *Mock) StopK3s(ctx context.Context) error {
	m.ensureInit()
	log.Printf("[nodeagent mock] %s: k3s stop (no-op)", m.NodeName)
	m.mu.Lock()
	m.k3sActive = false
	m.mu.Unlock()
	return nil
}
func (m *Mock) KillK3s(ctx context.Context) error {
	m.ensureInit()
	log.Printf("[nodeagent mock] %s: k3s kill (no-op)", m.NodeName)
	m.mu.Lock()
	m.k3sActive = false
	m.mu.Unlock()
	return nil
}
func (m *Mock) StartK3s(ctx context.Context) error {
	m.ensureInit()
	log.Printf("[nodeagent mock] %s: k3s start (no-op)", m.NodeName)
	m.mu.Lock()
	m.k3sActive = true
	m.mu.Unlock()
	return nil
}
func (m *Mock) K3sActive(ctx context.Context) (bool, error) {
	m.ensureInit()
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.k3sActive, nil
}
func (m *Mock) PowerOff(ctx context.Context, force bool) error {
	log.Printf("[nodeagent mock] %s: poweroff requested (no-op)", m.NodeName)
	return nil
}

func (m *Mock) Cordon(ctx context.Context, node string) error {
	log.Printf("[nodeagent mock] %s: cordon %s (no-op)", m.NodeName, node)
	m.setCordoned(node, true)
	return nil
}
func (m *Mock) Uncordon(ctx context.Context, node string) error {
	log.Printf("[nodeagent mock] %s: uncordon %s (no-op)", m.NodeName, node)
	m.setCordoned(node, false)
	return nil
}
func (m *Mock) Cordoned(ctx context.Context, node string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.cordoned[node], nil
}
func (m *Mock) Drain(ctx context.Context, node string, ignoreDaemonSets, deleteEmptyDirData bool, timeout time.Duration) error {
	log.Printf("[nodeagent mock] %s: drain %s (no-op)", m.NodeName, node)
	return nil
}
func (m *Mock) NodeReady(ctx context.Context, node string) (bool, error) { return true, nil }
func (m *Mock) SetLonghornScheduling(ctx context.Context, node string, allow bool) error {
	return nil
}
func (m *Mock) ZeroReplicaRisk(ctx context.Context, node string) ([]string, error) { return nil, nil }
func (m *Mock) LonghornAvailable(ctx context.Context) (bool, error)                { return false, nil }
func (m *Mock) EtcdReadyz(ctx context.Context) bool                                { return true }

func (m *Mock) EtcdIsLeader(ctx context.Context) (bool, error) { return false, nil }
func (m *Mock) EtcdSnapshot(ctx context.Context) error         { return nil }

func (m *Mock) VipBootstrap(ctx context.Context, on bool) error {
	log.Printf("[nodeagent mock] %s: vip-bootstrap on=%v (no-op)", m.NodeName, on)
	return nil
}
func (m *Mock) ClusterProbe(ctx context.Context, vip string) (ClusterProbeResponse, error) {
	return ClusterProbeResponse{VIPAnswers: true, CiliumPresent: true, CiliumReady: true}, nil
}
func (m *Mock) LonghornHealth(ctx context.Context) (LonghornHealthResponse, error) {
	return LonghornHealthResponse{}, nil
}
func (m *Mock) VaultSealed(ctx context.Context, namespace string) (VaultSealedResponse, error) {
	return VaultSealedResponse{}, nil
}
