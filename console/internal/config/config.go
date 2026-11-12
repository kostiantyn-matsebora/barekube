// Package config loads console's node registry and connection settings.
package config

import (
	"fmt"
	"net"
	"os"
	"regexp"
	"slices"
	"time"

	"gopkg.in/yaml.v3"
)

type Role string

const (
	RoleMaster Role = "master"
	RoleAgent  Role = "agent"
)

// Operation names one of console's per-node control-plane actions.
type Operation string

const (
	OpReboot   Operation = "reboot"
	OpStandBy  Operation = "standby"
	OpRejoin   Operation = "rejoin"
	OpPowerOff Operation = "poweroff"
)

type Node struct {
	Name          string `yaml:"name"`
	IP            string `yaml:"ip"`
	Role          Role   `yaml:"role"`
	NodeAgentPort int    `yaml:"nodeAgentPort"`

	// Operations lists which control-plane operations this node supports —
	// e.g. the Raspberry Pi agents omit "poweroff" because nothing here can
	// power one back on afterward (owner decision, Phase 2). This is a
	// per-node config fact, not something the orchestrator infers from
	// Role, so a future node whose capabilities don't match its role's
	// usual defaults doesn't need a code change. Empty means "every
	// operation allowed" — the default for config.local.yaml-style configs
	// that don't care about this distinction at all.
	Operations []Operation `yaml:"operations"`
}

func (n Node) Supports(op Operation) bool {
	if len(n.Operations) == 0 {
		return true
	}
	return slices.Contains(n.Operations, op)
}

type NodeAgentConfig struct {
	// Mock stands in for real per-node agent calls during pure local
	// testing — no process, no network, no node.
	Mock bool `yaml:"mock"`

	// Token is the single bearer token shared by every node's agent (not
	// loaded from this file in practice — see CONSOLE_NODE_TOKEN in
	// cmd/console/main.go; "secrets live in the environment").
	Token string `yaml:"-"`

	// CredentialsDir holds one file per node, named after the node,
	// containing just that node's pinned Cockpit^W node-agent cert
	// fingerprint (SHA-256, hex) — not secret, but still per-node
	// identity data, written by `barekube install node-agent`.
	CredentialsDir string `yaml:"credentialsDir"`
}

// Timing holds every wait deadline and poll interval, so nothing about one
// cluster's speed lives in code. Zero fields take the DefaultTiming value.
type Timing struct {
	NodeDown    time.Duration `yaml:"nodeDown"`    // reboot: node-agent must go unreachable
	NodeUp      time.Duration `yaml:"nodeUp"`      // reboot, rejoin: node back and Ready
	K3sInactive time.Duration `yaml:"k3sInactive"` // k3s unit stops
	K3sActive   time.Duration `yaml:"k3sActive"`   // k3s unit starts
	Uncordoned  time.Duration `yaml:"uncordoned"`  // node schedulable again
	GracefulOff time.Duration `yaml:"gracefulOff"` // graceful poweroff, before forcing
	ForcedOff   time.Duration `yaml:"forcedOff"`   // forced poweroff
	Clearance   time.Duration `yaml:"clearance"`   // cluster shutdown: wait for the parent
	Drain       time.Duration `yaml:"drain"`       // graceful drain cap
	ForceDrain  time.Duration `yaml:"forceDrain"`  // force drain cap
	ObserveFast time.Duration `yaml:"observeFast"` // observer interval while a run is active
	ObserveSlow time.Duration `yaml:"observeSlow"` // observer interval at rest
	Settle      time.Duration `yaml:"settle"`      // a stopped k3s must stay stopped this long
}

// DefaultTiming is the value of every Timing field left at zero.
func DefaultTiming() Timing {
	return Timing{
		NodeDown: 2 * time.Minute, NodeUp: 5 * time.Minute,
		K3sInactive: time.Minute, K3sActive: time.Minute, Uncordoned: 30 * time.Second,
		GracefulOff: 90 * time.Second, ForcedOff: 2 * time.Minute, Clearance: 10 * time.Minute,
		Drain: 2 * time.Minute, ForceDrain: 20 * time.Second,
		ObserveFast: 2 * time.Second, ObserveSlow: 5 * time.Second, Settle: 15 * time.Second,
	}
}

func (t *Timing) applyDefaults() {
	d := DefaultTiming()
	fill := func(v *time.Duration, def time.Duration) {
		if *v <= 0 {
			*v = def
		}
	}
	fill(&t.NodeDown, d.NodeDown)
	fill(&t.NodeUp, d.NodeUp)
	fill(&t.K3sInactive, d.K3sInactive)
	fill(&t.K3sActive, d.K3sActive)
	fill(&t.Uncordoned, d.Uncordoned)
	fill(&t.GracefulOff, d.GracefulOff)
	fill(&t.ForcedOff, d.ForcedOff)
	fill(&t.Clearance, d.Clearance)
	fill(&t.Drain, d.Drain)
	fill(&t.ForceDrain, d.ForceDrain)
	fill(&t.Settle, d.Settle)
	fill(&t.ObserveFast, d.ObserveFast)
	fill(&t.ObserveSlow, d.ObserveSlow)
}

// Policy is what the cluster shutdown does to one role.
type Policy struct {
	Action     string        `yaml:"action"`     // standby | poweroff
	Mode       string        `yaml:"mode"`       // graceful | force
	ForceAfter time.Duration `yaml:"forceAfter"` // graceful poweroff turns forced after this
	LeaderLast bool          `yaml:"leaderLast"` // masters only: the etcd leader goes last
}

// ShutdownWaits bounds each phase of the cluster shutdown.
type ShutdownWaits struct {
	Agents         time.Duration `yaml:"agents"`
	MastersDrained time.Duration `yaml:"mastersDrained"`
	MasterDown     time.Duration `yaml:"masterDown"`
}

// ClusterShutdown is the cluster shutdown policy. Defaults: agents stand by
// (force), masters power off (graceful, forced after 90s, leader last).
type ClusterShutdown struct {
	Agents    Policy        `yaml:"agents"`
	Masters   Policy        `yaml:"masters"`
	CordonAll *bool         `yaml:"cordonAll"`
	Snapshot  *bool         `yaml:"snapshot"`
	Waits     ShutdownWaits `yaml:"waits"`
}

// CordonAllEnabled reports whether every node is cordoned first (default true).
func (c ClusterShutdown) CordonAllEnabled() bool { return c.CordonAll == nil || *c.CordonAll }

// SnapshotEnabled reports whether an etcd snapshot is taken first (default true).
func (c ClusterShutdown) SnapshotEnabled() bool { return c.Snapshot == nil || *c.Snapshot }

func (c *ClusterShutdown) applyDefaults() {
	if c.Agents.Action == "" {
		c.Agents.Action = "standby"
	}
	if c.Agents.Mode == "" {
		c.Agents.Mode = "force"
	}
	if c.Masters.Action == "" {
		c.Masters.Action = "poweroff"
		c.Masters.LeaderLast = true
	}
	if c.Masters.Mode == "" {
		c.Masters.Mode = "graceful"
	}
	if c.Masters.ForceAfter <= 0 {
		c.Masters.ForceAfter = 90 * time.Second
	}
	if c.Waits.Agents <= 0 {
		c.Waits.Agents = 3 * time.Minute
	}
	if c.Waits.MastersDrained <= 0 {
		c.Waits.MastersDrained = 5 * time.Minute
	}
	if c.Waits.MasterDown <= 0 {
		c.Waits.MasterDown = 4 * time.Minute
	}
}

func (c ClusterShutdown) validate() error {
	for role, p := range map[string]Policy{"agents": c.Agents, "masters": c.Masters} {
		if p.Action != "standby" && p.Action != "poweroff" {
			return fmt.Errorf("clusterShutdown.%s.action %q: want standby or poweroff", role, p.Action)
		}
		if p.Mode != "graceful" && p.Mode != "force" {
			return fmt.Errorf("clusterShutdown.%s.mode %q: want graceful or force", role, p.Mode)
		}
	}
	return nil
}

// StartWaits bounds each phase of the cluster start. A cold boot takes minutes,
// so these are per-phase ceilings, not polls: each phase is re-checked on every
// observation round and fails with evidence when its ceiling passes.
type StartWaits struct {
	Nodes  time.Duration `yaml:"nodes"`  // masters reachable
	Quorum time.Duration `yaml:"quorum"` // etcd majority answering
	Rejoin time.Duration `yaml:"rejoin"` // one rejoin phase (one master, or all agents)
}

// ClusterStart is the cold start policy. It is about the nodes only: bringing
// every one of them back as a working member. The network layer (Cilium,
// kube-vip, the API VIP) is the platform's and has its own workflow.
type ClusterStart struct {
	// VaultNamespace is where Vault runs. Empty skips the sealed check.
	VaultNamespace string     `yaml:"vaultNamespace"`
	Waits          StartWaits `yaml:"waits"`
}

func (c *ClusterStart) applyDefaults() {
	fill := func(v *time.Duration, def time.Duration) {
		if *v <= 0 {
			*v = def
		}
	}
	fill(&c.Waits.Nodes, 10*time.Minute)
	fill(&c.Waits.Quorum, 5*time.Minute)
	fill(&c.Waits.Rejoin, 15*time.Minute)
}

var vaultNamespacePattern = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)

func (c ClusterStart) validate() error {
	if c.VaultNamespace != "" && !vaultNamespacePattern.MatchString(c.VaultNamespace) {
		return fmt.Errorf("clusterStart.vaultNamespace %q: want a Kubernetes namespace name", c.VaultNamespace)
	}
	return nil
}

// PlatformWaits bounds each phase of the API VIP restore.
type PlatformWaits struct {
	Probe  time.Duration `yaml:"probe"`  // the check reaches a verdict
	Vip    time.Duration `yaml:"vip"`    // Cilium and kube-vip recover once the bootstrap address is on
	Stable time.Duration `yaml:"stable"` // the VIP still answers once the bootstrap address is gone
}

// Platform is the platform layer the console knows about: the cluster network
// (Cilium) and the API virtual IP (kube-vip). It is separate from the nodes.
type Platform struct {
	// VIP is the API server virtual IP (kube-vip), for example 192.168.1.100.
	// Empty means the console knows of none, and the VIP restore is off.
	VIP string `yaml:"vip"`
	// DeadlockGrace is how long the VIP deadlock must hold before the console
	// acts, so a normal slow boot is never mistaken for it.
	DeadlockGrace time.Duration `yaml:"deadlockGrace"`
	Waits         PlatformWaits `yaml:"waits"`
}

func (p *Platform) applyDefaults() {
	fill := func(v *time.Duration, def time.Duration) {
		if *v <= 0 {
			*v = def
		}
	}
	fill(&p.DeadlockGrace, 30*time.Second)
	fill(&p.Waits.Probe, 10*time.Minute)
	fill(&p.Waits.Vip, 5*time.Minute)
	fill(&p.Waits.Stable, time.Minute)
}

func (p Platform) validate() error {
	if p.VIP != "" {
		if ip := net.ParseIP(p.VIP); ip == nil || ip.To4() == nil {
			return fmt.Errorf("platform.vip %q: want a plain IPv4 address", p.VIP)
		}
	}
	return nil
}

// Storage says where state lives and how long it is kept.
type Storage struct {
	// Path is the SQLite file. It must sit on the usb disk the container
	// mounts, never on the router's flash. In the router container /config is
	// that mount.
	Path string `yaml:"path"`
	// Retention is how long finished runs, events and timeouts are kept.
	Retention time.Duration `yaml:"retention"`
}

// DefaultStorage is the value of every Storage field left empty.
func DefaultStorage() Storage { return Storage{Path: "/config/console.db", Retention: 24 * time.Hour} }

func (s *Storage) applyDefaults() {
	d := DefaultStorage()
	if s.Path == "" {
		s.Path = d.Path
	}
	if s.Retention <= 0 {
		s.Retention = d.Retention
	}
}

type Config struct {
	Storage         Storage         `yaml:"storage"`
	NodeAgent       NodeAgentConfig `yaml:"nodeAgent"`
	Nodes           []Node          `yaml:"nodes"`
	ListenAddr      string          `yaml:"listenAddr"`
	Timing          Timing          `yaml:"timing"`
	ClusterShutdown ClusterShutdown `yaml:"clusterShutdown"`
	ClusterStart    ClusterStart    `yaml:"clusterStart"`
	Platform        Platform        `yaml:"platform"`
}

func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading config %s: %w", path, err)
	}
	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parsing config %s: %w", path, err)
	}
	if cfg.ListenAddr == "" {
		cfg.ListenAddr = ":8080"
	}
	if len(cfg.Nodes) == 0 {
		return nil, fmt.Errorf("config %s: no nodes defined", path)
	}
	cfg.Timing.applyDefaults()
	cfg.Storage.applyDefaults()
	cfg.ClusterShutdown.applyDefaults()
	if err := cfg.ClusterShutdown.validate(); err != nil {
		return nil, fmt.Errorf("config %s: %w", path, err)
	}
	cfg.ClusterStart.applyDefaults()
	if err := cfg.ClusterStart.validate(); err != nil {
		return nil, fmt.Errorf("config %s: %w", path, err)
	}
	cfg.Platform.applyDefaults()
	if err := cfg.Platform.validate(); err != nil {
		return nil, fmt.Errorf("config %s: %w", path, err)
	}
	return &cfg, nil
}

func (c *Config) Masters() []Node {
	var out []Node
	for _, n := range c.Nodes {
		if n.Role == RoleMaster {
			out = append(out, n)
		}
	}
	return out
}

// Agents returns the non-master nodes in config order.
func (c *Config) Agents() []Node {
	var out []Node
	for _, n := range c.Nodes {
		if n.Role != RoleMaster {
			out = append(out, n)
		}
	}
	return out
}

func (c *Config) Node(name string) (Node, bool) {
	for _, n := range c.Nodes {
		if n.Name == name {
			return n, true
		}
	}
	return Node{}, false
}
