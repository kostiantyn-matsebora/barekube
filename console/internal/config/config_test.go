package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func write(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

const nodes = `
nodes:
  - {name: m1, ip: 10.0.0.1, role: master}
  - {name: a1, ip: 10.0.0.2, role: agent, operations: [reboot, standby]}
`

func TestLoadAppliesDefaults(t *testing.T) {
	c, err := Load(write(t, nodes))
	if err != nil {
		t.Fatal(err)
	}
	if c.ListenAddr != ":8080" {
		t.Errorf("listen %q", c.ListenAddr)
	}
	if c.Timing != DefaultTiming() {
		t.Errorf("timing %+v", c.Timing)
	}
	cs := c.ClusterShutdown
	if cs.Agents.Action != "standby" || cs.Agents.Mode != "force" {
		t.Errorf("agents %+v", cs.Agents)
	}
	if cs.Masters.Action != "poweroff" || cs.Masters.Mode != "graceful" || !cs.Masters.LeaderLast || cs.Masters.ForceAfter != 90*time.Second {
		t.Errorf("masters %+v", cs.Masters)
	}
	if cs.Waits.Agents != 3*time.Minute || cs.Waits.MastersDrained != 5*time.Minute || cs.Waits.MasterDown != 4*time.Minute {
		t.Errorf("waits %+v", cs.Waits)
	}
	if c.Storage.Path != "/config/console.db" || c.Storage.Retention != 24*time.Hour {
		t.Errorf("storage %+v", c.Storage)
	}
	if c.Timing.Settle != 15*time.Second {
		t.Errorf("settle %s", c.Timing.Settle)
	}
	if !cs.SnapshotEnabled() {
		t.Error("snapshot should default on")
	}
	if !cs.CordonAllEnabled() {
		t.Error("cordonAll should default on")
	}
}

func TestLoadKeepsOverrides(t *testing.T) {
	c, err := Load(write(t, nodes+`
listenAddr: ":9"
timing: {nodeDown: 7s, observeFast: 1s, settle: 4s}
storage: {path: /usb1/x.db, retention: 6h}
clusterShutdown:
  cordonAll: false
  snapshot: false
  agents: {action: poweroff, mode: graceful}
  masters: {action: standby, mode: force, forceAfter: 5s}
  waits: {agents: 1m, mastersDrained: 2m, masterDown: 3m}
`))
	if err != nil {
		t.Fatal(err)
	}
	if c.ListenAddr != ":9" || c.Timing.NodeDown != 7*time.Second || c.Timing.NodeUp != 5*time.Minute {
		t.Errorf("overrides lost: %+v", c.Timing)
	}
	if c.Storage.Path != "/usb1/x.db" || c.Storage.Retention != 6*time.Hour || c.Timing.Settle != 4*time.Second {
		t.Errorf("storage or settle override lost: %+v %s", c.Storage, c.Timing.Settle)
	}
	cs := c.ClusterShutdown
	if cs.SnapshotEnabled() || cs.CordonAllEnabled() || cs.Agents.Action != "poweroff" || cs.Masters.Action != "standby" || cs.Masters.ForceAfter != 5*time.Second || cs.Waits.MasterDown != 3*time.Minute {
		t.Errorf("shutdown overrides lost: %+v", cs)
	}
}

func TestLoadErrors(t *testing.T) {
	cases := map[string]string{
		"no nodes":   "listenAddr: ':1'\n",
		"bad yaml":   "nodes: [",
		"bad action": nodes + "clusterShutdown: {agents: {action: explode}}\n",
		"bad mode":   nodes + "clusterShutdown: {masters: {mode: loud}}\n",
		"vip name":   nodes + "platform: {vip: kube-vip.lan}\n",
		"vip v6":     nodes + "platform: {vip: '::1'}\n",
		"vault ns":   nodes + "clusterStart: {vaultNamespace: 'Vault; x'}\n",
	}
	for name, body := range cases {
		if _, err := Load(write(t, body)); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
	if _, err := Load(filepath.Join(t.TempDir(), "missing.yaml")); err == nil || !strings.Contains(err.Error(), "reading config") {
		t.Errorf("missing file error = %v", err)
	}
}

func TestNodeHelpers(t *testing.T) {
	c, _ := Load(write(t, nodes))
	if len(c.Masters()) != 1 || c.Masters()[0].Name != "m1" {
		t.Error("masters")
	}
	if len(c.Agents()) != 1 || c.Agents()[0].Name != "a1" {
		t.Error("agents")
	}
	if n, ok := c.Node("a1"); !ok || n.IP != "10.0.0.2" {
		t.Error("node lookup")
	}
	if _, ok := c.Node("zzz"); ok {
		t.Error("unknown node found")
	}
	m, _ := c.Node("m1")
	if !m.Supports(OpPowerOff) {
		t.Error("empty operations allow everything")
	}
	a, _ := c.Node("a1")
	if !a.Supports(OpReboot) || a.Supports(OpPowerOff) {
		t.Error("operations allowlist")
	}
}

func TestClusterStartAndPlatformDefaultsAndOverrides(t *testing.T) {
	c, err := Load(write(t, nodes))
	if err != nil {
		t.Fatal(err)
	}
	if c.ClusterStart.VaultNamespace != "" || c.Platform.VIP != "" {
		t.Fatalf("no Vault and no VIP unless configured: %+v %+v", c.ClusterStart, c.Platform)
	}
	if cs := c.ClusterStart; cs.Waits.Nodes != 10*time.Minute || cs.Waits.Quorum != 5*time.Minute || cs.Waits.Rejoin != 15*time.Minute {
		t.Fatalf("cluster start defaults: %+v", cs)
	}
	if p := c.Platform; p.DeadlockGrace != 30*time.Second || p.Waits.Probe != 10*time.Minute || p.Waits.Vip != 5*time.Minute || p.Waits.Stable != time.Minute {
		t.Fatalf("platform defaults: %+v", p)
	}
	c, err = Load(write(t, nodes+"clusterStart: {vaultNamespace: vault, waits: {quorum: 2m}}\nplatform: {vip: 192.168.1.100, deadlockGrace: 5s, waits: {vip: 1m}}\n"))
	if err != nil {
		t.Fatal(err)
	}
	if cs := c.ClusterStart; cs.VaultNamespace != "vault" || cs.Waits.Quorum != 2*time.Minute || cs.Waits.Nodes != 10*time.Minute {
		t.Fatalf("cluster start overrides: %+v", cs)
	}
	if p := c.Platform; p.VIP != "192.168.1.100" || p.DeadlockGrace != 5*time.Second || p.Waits.Vip != time.Minute || p.Waits.Probe != 10*time.Minute {
		t.Fatalf("platform overrides: %+v", p)
	}
}
